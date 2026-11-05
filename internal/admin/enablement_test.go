package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sdrahn/custos/internal/journal"
)

const diagPerms = "- {server: systemd, tool: list_units, read_only: true, category: diagnostics}\n"

// A fresh host with a diagnostics agent becomes diagnostics-only.
func diagnosticsHost(t *testing.T) (*Admin, *journal.Memory) {
	t.Helper()
	a, mem, _ := withCategoriesRec(t)
	if err := a.SetPermissions("patch-pilot", strings.NewReader(diagPerms)); err != nil {
		t.Fatal(err)
	}
	if err := a.InitDiagnosticsOnly("setup"); err != nil {
		t.Fatal(err)
	}
	return a, mem
}

func records(mem *journal.Memory, event string) []journal.Record {
	var out []journal.Record
	for _, r := range mem.Records {
		if r.Event == event {
			out = append(out, r)
		}
	}
	return out
}

func TestDiagnosticsOnly(t *testing.T) {
	a, mem := diagnosticsHost(t)
	e, err := a.GetEnablement()
	if err != nil || e == nil || e.State != DiagnosticsOnly || len(e.Enabled) != 1 || e.Enabled[0] != Diagnostics || e.Record == "" {
		t.Fatalf("state %+v, %v", e, err)
	}
	b, _ := os.ReadFile(a.p(GatewayEnablement))
	if !strings.Contains(string(b), "_enablement:\n  state: diagnostics-only") {
		t.Errorf("gateway data:\n%s", b)
	}
	rec := records(mem, journal.CategoriesEnabled)
	if len(rec) != 1 || rec[0].Fields["ENABLEMENT"] != e.Record || rec[0].Fields["ENABLEMENT_SHA256"] != e.Digest() ||
		rec[0].Fields["BY"] != "setup" || !strings.Contains(rec[0].Fields["ENABLEMENT_TO"], "diagnostics-only") {
		t.Errorf("record %+v", rec)
	}
	// Not a way back.
	if err := a.InitDiagnosticsOnly("setup"); err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Errorf("twice: %v", err)
	}
}

// Only a fresh host: an agent with other permissions keeps it from
// becoming diagnostics-only.
func TestDiagnosticsOnlyNeedsAFreshHost(t *testing.T) {
	a, _, _ := withCategoriesRec(t) // patch-pilot has patch-errata and config-lifecycle
	if err := a.InitDiagnosticsOnly("setup"); err == nil || !strings.Contains(err.Error(), "outside diagnostics") {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(a.p(GatewayEnablement)); !os.IsNotExist(err) {
		t.Error("written all the same")
	}
}

// On a diagnostics-only host, agent-admin refuses to load other
// categories, and records each refusal with the person.
func TestDisabledCategoriesRefused(t *testing.T) {
	a, mem := diagnosticsHost(t)
	a.By = "alice"
	for _, perms := range []string{
		"- {server: zypp, tool: patch, category: patch-errata}\n",
		"- {server: zypp, tool: 'list_*', read_only: true}\n", // without a category
	} {
		if err := a.SetPermissions("patch-pilot", strings.NewReader(perms)); err == nil || !strings.Contains(err.Error(), "diagnostics-only") {
			t.Errorf("%q: %v", perms, err)
		}
	}
	if err := promote(a, "config-lifecycle", sat); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("promote: %v", err)
	}
	rec := records(mem, journal.CategoryRefused)
	if len(rec) != 3 || rec[0].Fields["CATEGORY"] != "patch-errata" || rec[0].Fields["BY"] != "alice" ||
		rec[0].Fields["REASON"] != "permissions" || rec[2].Fields["REASON"] != "promote" {
		t.Errorf("records %+v", rec)
	}
	// diagnostics itself is fine.
	if err := a.SetPermissions("patch-pilot", strings.NewReader(diagPerms)); err != nil {
		t.Error(err)
	}
}

// diagnostics only reads, and is never promoted.
func TestDiagnosticsReadsOnly(t *testing.T) {
	a, _, _ := withCategoriesRec(t)
	err := a.SetPermissions("patch-pilot", strings.NewReader("- {server: systemd, tool: restart_unit, category: diagnostics}\n"))
	if err == nil || !strings.Contains(err.Error(), "read_only") {
		t.Errorf("%v", err)
	}
	a.SetPermissions("patch-pilot", strings.NewReader(diagPerms))
	if err := promote(a, Diagnostics, sat); err == nil || !strings.Contains(err.Error(), "never promoted") {
		t.Errorf("promote: %v", err)
	}
}

// enable is the one action: recorded, and categories start supervised:
// the agent data is what a broader install with them has.
func TestEnable(t *testing.T) {
	a, mem := diagnosticsHost(t)
	if err := a.Enable("alice", []string{"no-such"}, false); err == nil {
		t.Error("an unknown category enabled")
	}
	if err := a.Enable("alice", []string{"patch-errata"}, false); err != nil {
		t.Fatal(err)
	}
	e, _ := a.GetEnablement()
	if e.State != SomeCategories || strings.Join(e.Enabled, ",") != "diagnostics,patch-errata" {
		t.Errorf("after enable: %+v", e)
	}
	rec := records(mem, journal.CategoriesEnabled)
	last := rec[len(rec)-1]
	if last.Fields["BY"] != "alice" || !strings.Contains(last.Fields["ENABLEMENT_FROM"], "diagnostics-only") ||
		last.Fields["ENABLEMENT"] != e.Record || !strings.Contains(last.Message, "patch-errata") {
		t.Errorf("record %+v", last)
	}
	perms := "- {server: zypp, tool: patch, category: patch-errata}\n"
	if err := a.SetPermissions("patch-pilot", strings.NewReader(perms)); err != nil {
		t.Errorf("an enabled category: %v", err)
	}
	if err := a.SetPermissions("patch-pilot", strings.NewReader("- {server: x, tool: y, category: config-lifecycle}\n")); err == nil {
		t.Error("a category still disabled")
	}
	if err := a.Enable("alice", []string{"patch-errata"}, false); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("twice: %v", err)
	}

	// --all: every category, supervised; the agent's data is untouched.
	before, _ := os.ReadFile(a.gatewayFile("patch-pilot"))
	if err := a.Enable("alice", nil, true); err != nil {
		t.Fatal(err)
	}
	e, _ = a.GetEnablement()
	cats, _ := a.Categories()
	if len(e.Enabled) != len(cats) {
		t.Errorf("--all: %v of %d", e.Enabled, len(cats))
	}
	for c := range cats {
		if !e.Usable(c) {
			t.Errorf("%s not usable after --all", c)
		}
	}
	after, _ := os.ReadFile(a.gatewayFile("patch-pilot"))
	if string(before) != string(after) {
		t.Errorf("enable changed the agent's data:\n%s\n%s", before, after)
	}
	d, _ := a.readGateway("patch-pilot")
	if len(d.Categories) != 0 {
		t.Errorf("a category left supervised: %+v", d.Categories)
	}
}

// Without enablement data, every category is enabled already.
func TestEnableOnBroaderInstall(t *testing.T) {
	a, _, _ := withCategoriesRec(t)
	if err := a.Enable("alice", []string{"patch-errata"}, false); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("%v", err)
	}
	var e *Enablement
	for _, c := range []string{"", "patch-errata", Diagnostics} {
		if !e.Usable(c) {
			t.Errorf("%q not usable without enablement data", c)
		}
	}
}

// show reports the state, and a state changed without its record.
func TestShowEnablementTampered(t *testing.T) {
	a, mem := diagnosticsHost(t)
	journalOf := func(...string) ([]byte, error) {
		var out []string
		for _, r := range records(mem, journal.CategoriesEnabled) {
			b, _ := json.Marshal(map[string]string{"AGENT_ENABLEMENT": r.Fields["ENABLEMENT"], "AGENT_ENABLEMENT_SHA256": r.Fields["ENABLEMENT_SHA256"]})
			out = append(out, string(b))
		}
		return []byte(strings.Join(out, "\n")), nil
	}
	a.JournalJSON = journalOf
	p, err := a.Show()
	if err != nil {
		t.Fatal(err)
	}
	if p.Enablement == nil || p.Enablement.State != DiagnosticsOnly || p.Enablement.Tampered != "" {
		t.Errorf("show: %+v", p.Enablement)
	}
	// Edited by hand: every category "enabled" without agent-admin.
	f := a.p(GatewayEnablement)
	b, _ := os.ReadFile(f)
	os.WriteFile(f, []byte(strings.Replace(strings.Replace(string(b), "state: diagnostics-only", "state: categories", 1),
		"- diagnostics", "- diagnostics\n    - patch-errata", 1)), 0o644)
	p, _ = a.Show()
	if p.Enablement.State != SomeCategories || !strings.Contains(p.Enablement.Tampered, "differs from its record") {
		t.Errorf("tampered: %+v", p.Enablement)
	}
	// Without a record id at all.
	os.WriteFile(f, []byte("_enablement:\n  state: categories\n  enabled: [diagnostics, patch-errata]\n"), 0o644)
	p, _ = a.Show()
	if !strings.Contains(p.Enablement.Tampered, "no record") {
		t.Errorf("no record: %+v", p.Enablement)
	}
}

// create --template refuses a template whose permissions name a disabled
// category.
func TestTemplateOfDisabledCategoryRefused(t *testing.T) {
	a, mem := diagnosticsHost(t)
	dir := a.p(filepath.Join(TemplatesSite, "patcher"))
	writeFile(filepath.Join(dir, "template.yaml"), []byte("title: Patcher\ndescription: x\nsystem: any\nmode: supervised\n"), 0o644)
	writeFile(filepath.Join(dir, "agent.yaml"), []byte(strings.Replace(yml, "patch-pilot", "patcher", 1)), 0o644)
	// As the shipped patch-pilot: read-only permissions without a category
	// first, then two disabled categories.
	writeFile(filepath.Join(dir, "permissions.yaml"), []byte("- {server: zypp, tool: check_updates, read_only: true}\n"+
		"- {server: zypp, tool: patch, category: patch-errata}\n- {server: systemd, tool: restart_unit, category: config-lifecycle}\n"+
		"- {server: zypp, tool: install, category: patch-errata}\n"), 0o644)
	err := a.CreateFromTemplate("patcher", "patcher", "")
	if err == nil || !strings.Contains(err.Error(), "task categories config-lifecycle, patch-errata and permissions without a category are not enabled") ||
		!strings.Contains(err.Error(), "agent-admin enable config-lifecycle patch-errata") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(a.agentDir("patcher")); !os.IsNotExist(err) {
		t.Error("created all the same")
	}
	// One record per disabled category, the one without a category too.
	rec := records(mem, journal.CategoryRefused)
	if len(rec) != 3 || rec[0].Fields["REASON"] != "create --template patcher" || rec[0].Fields["CATEGORY"] != "(none)" ||
		rec[1].Fields["CATEGORY"] != "patch-errata" || rec[2].Fields["CATEGORY"] != "config-lifecycle" {
		t.Errorf("records %+v", rec)
	}
}
