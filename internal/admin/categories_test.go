package admin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sdrahn/custos/internal/journal"
	"github.com/sdrahn/custos/internal/readiness"
)

// catalog writes the shipped categories (packaging/categories) into the
// test root, and an agent with permissions in two of them.
func withCategories(t *testing.T) (*Admin, *journal.Memory) {
	t.Helper()
	a, _ := newAdmin(t)
	m, err := filepath.Glob("../../packaging/categories/*.yaml")
	if err != nil || len(m) == 0 {
		t.Fatalf("no shipped categories: %v", err)
	}
	for _, f := range m {
		b, _ := os.ReadFile(f)
		writeFile(a.p(filepath.Join(CategoriesVendor, filepath.Base(f))), b, 0o644)
	}
	mem := &journal.Memory{}
	a.Journal = func(string) journal.Logger { return mem }
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(yml), 0o644)
	if err := a.Create("patch-pilot", f); err != nil {
		t.Fatal(err)
	}
	perms := "- {server: zypp, tool: 'list_*', read_only: true}\n- {server: zypp, tool: install_patches, category: patch-errata}\n- {server: systemd, tool: restart_unit, category: config-lifecycle}\n"
	if err := a.SetPermissions("patch-pilot", strings.NewReader(perms)); err != nil {
		t.Fatal(err)
	}
	return a, mem
}

const sat = "windows:\n  - {days: [sat], from: '02:00', to: '04:00', tz: Europe/Berlin}\n"

func promote(a *Admin, cat, env string) error {
	return a.Promote("patch-pilot", cat, "alice", Promotion{Envelope: []byte(env), Reason: "patching window", DespiteReadiness: true})
}

func TestShippedCatalog(t *testing.T) {
	a, _ := withCategories(t)
	c, err := a.Categories()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"patch-errata", "security-response", "config-lifecycle"} {
		if c[n].Title == "" {
			t.Errorf("%s: missing or without a title: %v", n, c)
		}
	}
}

func TestSiteCatalogReplacesAndDisables(t *testing.T) {
	a, _ := withCategories(t)
	writeFile(a.p(filepath.Join(CategoriesSite, "patch-errata.yaml")), []byte("patch-errata: {title: Patches (site)}\nbackups: {title: Backups}\n"), 0o644)
	writeFile(a.p(filepath.Join(CategoriesSite, "security-response.yaml")), nil, 0o644)
	c, err := a.Categories()
	if err != nil {
		t.Fatal(err)
	}
	if c["patch-errata"].Title != "Patches (site)" || c["backups"].Title != "Backups" {
		t.Errorf("site entries: %v", c)
	}
	if _, ok := c["security-response"]; ok {
		t.Error("an empty site file did not disable security-response")
	}
	writeFile(a.p(filepath.Join(CategoriesSite, "bad.yaml")), []byte("uncategorized: {title: x}\n"), 0o644)
	if _, err := a.Categories(); err == nil {
		t.Error("uncategorized accepted as a category")
	}
}

func TestPermissionsCategory(t *testing.T) {
	a, _ := withCategories(t)
	for _, c := range []struct{ perms, says string }{
		{"- {server: zypp, tool: x, category: no-such}\n", `no task category "no-such"`},
		{"- {server: zypp, tool: x, category: [a]}\n", "category is a name"},
		{"- {server: zypp, tool: x, category: patch-errata, autonomous: true}\n", "autonomous is for permissions without a category"},
	} {
		if err := a.SetPermissions("patch-pilot", strings.NewReader(c.perms)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: %v, want %q", c.perms, err, c.says)
		}
	}
}

func TestPromoteAndDemote(t *testing.T) {
	a, mem := withCategories(t)
	if err := promote(a, "patch-errata", sat); err != nil {
		t.Fatal(err)
	}
	d, _ := a.readGateway("patch-pilot")
	c := d.Categories["patch-errata"]
	if c == nil || c.Mode != "bounded" || c.PromotedBy != "alice 2026-11-03T12:00:00Z" || !strings.HasPrefix(c.Promotion, "p-") || len(c.Envelope.Windows) != 1 {
		t.Fatalf("after promote: %+v", c)
	}
	if d.Categories["config-lifecycle"] != nil || d.Mode != "supervised" {
		t.Errorf("promotion touched more than its category: %+v", d)
	}
	// The data as the gateway's policy reads it.
	b, _ := os.ReadFile(a.gatewayFile("patch-pilot"))
	var doc map[string]map[string]any
	yaml.Unmarshal(b, &doc)
	got := doc["patch-pilot"]["categories"].(map[string]any)["patch-errata"].(map[string]any)
	w := got["envelope"].(map[string]any)["windows"].([]any)[0].(map[string]any)
	if got["mode"] != "bounded" || w["tz"] != "Europe/Berlin" || w["from"] != "02:00" || w["to"] != "04:00" {
		t.Errorf("gateway data: %v", got)
	}
	r := mem.Events(journal.CategoryPromoted)
	if len(r) != 1 {
		t.Fatalf("promotion records: %v", mem.Records)
	}
	f := r[0].Fields
	if f["CATEGORY"] != "patch-errata" || f["MODE_FROM"] != "supervised" || f["MODE_TO"] != "bounded" || f["BY"] != "alice" ||
		f["REASON"] != "patching window" || f["PROMOTION"] != c.Promotion || len(f["ENVELOPE_SHA256"]) != 64 {
		t.Errorf("promotion record: %v", f)
	}
	// Other changes keep the category.
	if err := a.SetMode("patch-pilot", "supervised"); err != nil {
		t.Fatal(err)
	}
	if err := a.Pause("patch-pilot", "bob", true); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.readGateway("patch-pilot"); d.Categories["patch-errata"].Mode != "bounded" {
		t.Error("mode or pause dropped the category")
	}
	if err := promote(a, "patch-errata", sat); err == nil || !strings.Contains(err.Error(), "already bounded") {
		t.Errorf("promoted twice with the same envelope: %v", err)
	}
	if err := a.Demote("patch-pilot", "patch-errata", "bob", "override rate"); err != nil {
		t.Fatal(err)
	}
	d, _ = a.readGateway("patch-pilot")
	if c := d.Categories["patch-errata"]; c.Mode != "supervised" || c.DemotedBy != "bob 2026-11-03T12:00:00Z" || len(c.Envelope.Windows) != 1 {
		t.Errorf("after demote: %+v", c)
	}
	if r := mem.Events(journal.CategoryDemoted); len(r) != 1 || r[0].Fields["BY"] != "bob" || r[0].Fields["MODE_TO"] != "supervised" || r[0].Fields["REASON"] != "override rate" {
		t.Errorf("demotion records: %v", r)
	}
	if err := a.Demote("patch-pilot", "patch-errata", "bob", ""); err == nil {
		t.Error("demoted a supervised category")
	}
}

func TestPromoteRefuses(t *testing.T) {
	a, mem := withCategories(t)
	for _, c := range []struct {
		name string
		do   func() error
		says string
	}{
		{"unknown category", func() error { return promote(a, "no-such", sat) }, `no task category "no-such"`},
		{"no permission in it", func() error { return promote(a, "security-response", sat) }, "no permission in category security-response"},
		{"no readiness", func() error {
			return a.Promote("patch-pilot", "patch-errata", "alice", Promotion{Envelope: []byte(sat)})
		}, "category patch-errata is not ready (observed 0.0 days of 14; 0 proposals of 50)"},
		{"no reason", func() error {
			return a.Promote("patch-pilot", "patch-errata", "alice", Promotion{Envelope: []byte(sat), DespiteReadiness: true})
		}, "needs --reason"},
		{"no windows", func() error { return promote(a, "patch-errata", "windows: []\n") }, "no windows"},
		{"rate", func() error { return promote(a, "patch-errata", sat+"rate: {calls: 20, per: 1h}\n") }, "not enforced by mcp-gateway yet (CONTRACT.md O12)"},
		{"rollback", func() error { return promote(a, "patch-errata", sat+"rollback: {on: [tool_error]}\n") }, "demotion evaluator"},
		{"hosts", func() error { return promote(a, "patch-errata", sat+"hosts: [web1]\n") }, "parked (CONTRACT.md O13)"},
		{"blast radius", func() error { return promote(a, "patch-errata", sat+"blast_radius: {hosts: 2}\n") }, "parked (CONTRACT.md O13)"},
		{"unknown key", func() error { return promote(a, "patch-errata", sat+"whenever: true\n") }, `unknown key "whenever"`},
		{"bad time", func() error { return promote(a, "patch-errata", "windows: [{from: '2:00', to: '04:00'}]\n") }, "HH:MM"},
		{"across midnight", func() error { return promote(a, "patch-errata", "windows: [{from: '22:00', to: '02:00'}]\n") }, "is not before to"},
		{"bad day", func() error {
			return promote(a, "patch-errata", "windows: [{days: [saturday], from: '02:00', to: '04:00'}]\n")
		}, `day "saturday"`},
		{"bad zone", func() error {
			return promote(a, "patch-errata", "windows: [{from: '02:00', to: '04:00', tz: Mars/Olympus}]\n")
		}, `tz "Mars/Olympus"`},
		{"window key", func() error {
			return promote(a, "patch-errata", "windows: [{from: '02:00', to: '04:00', every: day}]\n")
		}, "every"},
	} {
		if err := c.do(); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want %q", c.name, err, c.says)
		}
	}
	if err := a.SetMode("patch-pilot", "advisory"); err != nil {
		t.Fatal(err)
	}
	if err := promote(a, "patch-errata", sat); err == nil || !strings.Contains(err.Error(), "advisory, which caps every category") {
		t.Errorf("advisory agent: %v", err)
	}
	if len(mem.Records) != 0 {
		t.Errorf("a refused promotion was recorded: %v", mem.Records)
	}
	if d, _ := a.readGateway("patch-pilot"); d.Categories != nil {
		t.Errorf("a refused promotion was written: %v", d.Categories)
	}
}

func TestShowHasCategories(t *testing.T) {
	a, _ := withCategories(t)
	if err := promote(a, "patch-errata", sat); err != nil {
		t.Fatal(err)
	}
	p, err := a.Show("patch-pilot")
	if err != nil {
		t.Fatal(err)
	}
	if c := p.Agents["patch-pilot"].Categories["patch-errata"]; c == nil || c.Mode != "bounded" {
		t.Errorf("show: %+v", p.Agents["patch-pilot"])
	}
}

// promote waits, as mode does, until the gateway's OPA has the change.
func TestPromoteWaitsForThePolicy(t *testing.T) {
	a, _ := withCategories(t)
	var data map[string]any
	a.OPAData = func(string) (any, error) { return data, nil }
	a.PolicyWait = 0
	if err := promote(a, "patch-errata", sat); err == nil || !strings.Contains(err.Error(), "did not load the change") {
		t.Errorf("not loaded: %v", err)
	}
	d, _ := a.readGateway("patch-pilot")
	id := d.Categories["patch-errata"].Promotion
	data = map[string]any{"mode": "supervised", "paused": false, "permissions": []any{1, 2, 3},
		"categories": map[string]any{"patch-errata": map[string]any{"mode": "bounded", "promotion": id}}}
	if err := a.Demote("patch-pilot", "patch-errata", "bob", ""); err == nil {
		t.Error("demote returned while the gateway still had bounded")
	}
	// As with mode: the file is written, the gateway is late.
	if d, _ := a.readGateway("patch-pilot"); d.Categories["patch-errata"].Mode != "supervised" {
		t.Error("the demotion was not written")
	}
	// Once the gateway has it, the next change returns at once.
	data["categories"] = map[string]any{"patch-errata": map[string]any{"mode": "supervised", "promotion": id}}
	if err := a.SetMode("patch-pilot", "supervised"); err != nil {
		t.Errorf("loaded: %v", err)
	}
}

// Promoting again keeps the mode and changes the envelope: promote waits
// for the new promotion, not only for bounded.
func TestPromoteAgainWaitsForTheNewEnvelope(t *testing.T) {
	a, _ := withCategories(t)
	if err := promote(a, "patch-errata", sat); err != nil {
		t.Fatal(err)
	}
	d, _ := a.readGateway("patch-pilot")
	first := d.Categories["patch-errata"].Promotion
	// The gateway still has the first promotion.
	a.OPAData = func(string) (any, error) {
		return map[string]any{"mode": "supervised", "paused": false, "permissions": []any{1, 2, 3},
			"categories": map[string]any{"patch-errata": map[string]any{"mode": "bounded", "promotion": first}}}, nil
	}
	a.PolicyWait = 0
	n := 0
	a.Secret = func() string { n++; return fmt.Sprintf("%032d", n) }
	sun := "windows:\n  - {days: [sun], from: '02:00', to: '04:00', tz: Europe/Berlin}\n"
	if err := promote(a, "patch-errata", sun); err == nil || !strings.Contains(err.Error(), "did not load the change") {
		t.Errorf("returned while the gateway had the old envelope: %v", err)
	}
}

// journal is an agent's records and the gateway's decisions as
// journalctl -o json gives them, for agent-admin readiness.
func readinessJournal(n int, declined int, start time.Time) func(args ...string) ([]byte, error) {
	var agent, gw []string
	line := func(m map[string]any) string { b, _ := json.Marshal(m); return string(b) }
	us := func(t time.Time) string { return strconv.FormatInt(t.UnixMicro(), 10) }
	for i := 0; i < n; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		inv, id := fmt.Sprintf("e-%d", i), fmt.Sprintf("a-%d", i)
		agent = append(agent,
			line(map[string]any{"AGENT_EVENT": "tool.call", "AGENT_INVOCATION": inv, "AGENT_CALL_ID": "c1", "AGENT_TOOL": "zypp__install_patches", "AGENT_ARGS_SHA256": "x", "__REALTIME_TIMESTAMP": us(at)}),
			line(map[string]any{"AGENT_EVENT": "approval.pending", "AGENT_INVOCATION": inv, "AGENT_CALL_ID": "c1", "AGENT_TOOL": "zypp__install_patches", "AGENT_APPROVAL_ID": id, "__REALTIME_TIMESTAMP": us(at.Add(time.Second))}),
			line(map[string]any{"AGENT_EVENT": "tool.result", "AGENT_INVOCATION": inv, "AGENT_CALL_ID": "c1", "AGENT_OUTCOME": "ok", "__REALTIME_TIMESTAMP": us(at.Add(2 * time.Minute))}))
		msg, _ := json.Marshal(map[string]any{"msg": "mcp", "event": "mcp-approval", "id": id, "ok": i >= declined, "by": "alice", "principal": "agent-patch-pilot"})
		gw = append(gw, line(map[string]any{"MESSAGE": string(msg), "__REALTIME_TIMESTAMP": us(at.Add(time.Minute))}))
	}
	return func(args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "mcp-gateway.service" {
				return []byte(strings.Join(gw, "\n")), nil
			}
		}
		return []byte(strings.Join(agent, "\n")), nil
	}
}

func TestReadinessReport(t *testing.T) {
	a, _ := withCategories(t)
	start := a.Now().AddDate(0, 0, -20)
	a.JournalJSON = readinessJournal(60, 4, start)
	r, err := a.Readiness("patch-pilot")
	if err != nil {
		t.Fatal(err)
	}
	pe := r.Categories["patch-errata"]
	if pe.Proposals != 60 || pe.Declined != 4 || pe.Approved != 56 || pe.Ready || !strings.Contains(strings.Join(pe.Unmet, ";"), "override rate 6.7 % above 5.0 %") {
		t.Errorf("patch-errata %+v", pe)
	}
	if cl, ok := r.Categories["config-lifecycle"]; !ok || cl.Proposals != 0 {
		t.Errorf("config-lifecycle %+v (every category of the permissions)", cl)
	}
	if r.Thresholds != readiness.Defaults {
		t.Errorf("thresholds %+v", r.Thresholds)
	}
	// The site's thresholds: 4 of 60 overrides is within 10 %.
	writeFile(a.p(PromotionConfig), []byte("override_rate: 0.1\nproposals: 40\n"), 0o644)
	r, _ = a.Readiness("patch-pilot", "patch-errata")
	if pe := r.Categories["patch-errata"]; !pe.Ready || r.Thresholds.Proposals != 40 || len(r.Categories) != 1 {
		t.Errorf("with the site's thresholds: %+v %+v", r.Thresholds, pe)
	}
}

func TestThresholdsFile(t *testing.T) {
	a, _ := withCategories(t)
	for body, says := range map[string]string{
		"override_rate: 5\n":  "fractions from 0 to 1",
		"window_days: 0\n":    "window_days 1 or more",
		"overide_rate: 0.1\n": "overide_rate",
	} {
		writeFile(a.p(PromotionConfig), []byte(body), 0o644)
		if _, err := a.Thresholds(); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%q: %v, want %q", body, err, says)
		}
	}
}

// A ready category is promoted without --despite-readiness, and the
// record carries the measures.
func TestPromoteWhenReady(t *testing.T) {
	a, mem := withCategories(t)
	a.JournalJSON = readinessJournal(60, 0, a.Now().AddDate(0, 0, -20))
	if err := a.Promote("patch-pilot", "patch-errata", "alice", Promotion{Envelope: []byte(sat)}); err != nil {
		t.Fatal(err)
	}
	r := mem.Events(journal.CategoryPromoted)
	var m readiness.Measures
	if len(r) != 1 || json.Unmarshal([]byte(r[0].Fields["READINESS"]), &m) != nil || !m.Ready || m.Proposals != 60 {
		t.Errorf("record %v", r)
	}
}
