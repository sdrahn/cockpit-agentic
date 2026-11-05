package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sdrahn/custos/internal/journal"
)

// GatewayEnablement is the enablement state (CONTRACT.md §6b), gateway
// data next to the emergency stop: data.mcp.agents._enablement.
const GatewayEnablement = "/etc/mcp-gateway/policy/agents/_enablement.yaml"

// Diagnostics is the category a diagnostics-only host may use.
const Diagnostics = "diagnostics"

// Enablement states (§6b).
const (
	DiagnosticsOnly = "diagnostics-only"
	SomeCategories  = "categories"
)

// Enablement is the enablement state. Absent (nil): every category is
// enabled, as on every host before §6b.
type Enablement struct {
	State     string   `yaml:"state" json:"state"`
	Enabled   []string `yaml:"enabled" json:"enabled"`
	ChangedBy string   `yaml:"changed_by,omitempty" json:"changed_by,omitempty"`
	Record    string   `yaml:"record,omitempty" json:"record,omitempty"`
	// Tampered: show's check against the last enablement record; "" when
	// it matches or the journal cannot be read.
	Tampered string `yaml:"-" json:"tampered,omitempty"`
}

// Usable reports whether a permission of the category (""; none) may be
// used: the gateway's rule (agents.rego, category_usable).
func (e *Enablement) Usable(cat string) bool {
	switch {
	case e == nil:
		return true
	case e.State == DiagnosticsOnly:
		return cat == Diagnostics
	case e.State == SomeCategories:
		if cat == "" || cat == Diagnostics {
			return true
		}
		for _, c := range e.Enabled {
			if c == cat {
				return true
			}
		}
	}
	return false
}

// Digest is the SHA-256 of state and enabled, JSON with sorted keys
// (AGENT_ENABLEMENT_SHA256).
func (e *Enablement) Digest() string {
	en := append([]string{}, e.Enabled...)
	if en == nil {
		en = []string{}
	}
	b, _ := json.Marshal(map[string]any{"enabled": en, "state": e.State})
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (e *Enablement) summary() string {
	b, _ := json.Marshal(map[string]any{"state": e.State, "enabled": e.Enabled})
	return string(b)
}

// GetEnablement reads the enablement state; nil when there is none.
func (a *Admin) GetEnablement() (*Enablement, error) {
	b, err := os.ReadFile(a.p(GatewayEnablement))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		E *Enablement `yaml:"_enablement"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", GatewayEnablement, err)
	}
	if doc.E == nil {
		return nil, fmt.Errorf("%s: no _enablement", GatewayEnablement)
	}
	return doc.E, nil
}

// writeEnablement writes the state with a new record id, records the
// change (categories.enabled) and waits for the gateway's OPA.
func (a *Admin) writeEnablement(from, to *Enablement, by, msg string) error {
	id := "n-" + a.Secret()
	if len(id) > 18 {
		id = id[:18]
	}
	to.ChangedBy = by + " " + a.Now().UTC().Format(time.RFC3339Nano)
	to.Record = id
	var buf bytes.Buffer
	buf.WriteString("# The task categories this host may use (custos CONTRACT.md §6b), written\n" +
		"# by agent-admin. Change it only with agent-admin enable: a change without\n" +
		"# its record is reported as tampered.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]*Enablement{"_enablement": to}); err != nil {
		return err
	}
	if err := writeFile(a.p(GatewayEnablement), buf.Bytes(), 0o644); err != nil {
		return err
	}
	fromJSON := `{"state":"","enabled":"all"}` // the broader install's
	if from != nil {
		fromJSON = from.summary()
	}
	a.record(journal.CategoriesEnabled, "", msg, journal.Fields{
		"BY": by, "ENABLEMENT": id, "ENABLEMENT_FROM": fromJSON, "ENABLEMENT_TO": to.summary(),
		"ENABLEMENT_SHA256": to.Digest(),
	})
	return a.waitPolicy("mcp/agents/_enablement/record", func(v any) bool { return v == id })
}

// InitDiagnosticsOnly makes a fresh host diagnostics-only (the bundled
// install, custos-setup, §17a). It is not a way back: it refuses a host
// that has enablement data, or an agent that uses another category or a
// permission without one.
func (a *Admin) InitDiagnosticsOnly(by string) error {
	e, err := a.GetEnablement()
	if err != nil {
		return err
	}
	if e != nil {
		return fmt.Errorf("this host's enablement is set already (%s, by %s): going back to diagnostics-only is not offered", e.State, e.ChangedBy)
	}
	p, err := a.Show()
	if err != nil {
		return err
	}
	for name, d := range p.Agents {
		for _, perm := range d.Permissions {
			if c, _ := perm["category"].(string); c != Diagnostics {
				return fmt.Errorf("agent %s has permissions outside diagnostics: diagnostics-only is for a fresh install", name)
			}
		}
	}
	to := &Enablement{State: DiagnosticsOnly, Enabled: []string{Diagnostics}}
	if err := a.writeEnablement(nil, to, by, by+" made this host diagnostics-only"); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "this host is diagnostics-only: other task categories are disabled until agent-admin enable")
	return nil
}

// Enable enables task categories (§6b): the one administrator action
// from diagnostics-only (or a partly enabled host) towards the broader
// capability. Enabled categories start supervised: nothing else changes.
func (a *Admin) Enable(by string, cats []string, all bool) error {
	catalog, err := a.Categories()
	if err != nil {
		return err
	}
	if all {
		cats = cats[:0]
		for c := range catalog {
			cats = append(cats, c)
		}
	}
	if len(cats) == 0 {
		return errors.New("enable: name the categories, or --all")
	}
	for _, c := range cats {
		if err := a.checkCategory(c); err != nil {
			return err
		}
	}
	from, err := a.GetEnablement()
	if err != nil {
		return err
	}
	if from == nil {
		return errors.New("every task category is enabled on this host already (no enablement state)")
	}
	set := map[string]bool{Diagnostics: true}
	if from.State == SomeCategories {
		for _, c := range from.Enabled {
			set[c] = true
		}
	}
	var added []string
	for _, c := range cats {
		if !set[c] {
			added = append(added, c)
		}
		set[c] = true
	}
	if len(added) == 0 && from.State == SomeCategories {
		return fmt.Errorf("enabled already: %s", strings.Join(cats, ", "))
	}
	to := &Enablement{State: SomeCategories}
	for c := range set {
		to.Enabled = append(to.Enabled, c)
	}
	sort.Strings(to.Enabled)
	sort.Strings(added)
	if err := a.writeEnablement(from, to, by, fmt.Sprintf("%s enabled the task categories %s", by, strings.Join(added, ", "))); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "enabled: %s; they start supervised (agent-admin readiness, promote)\n", strings.Join(added, ", "))
	return nil
}

// checkEnabled refuses permissions of a disabled category, recording the
// refusal (category.refused) for the person, once per disabled category.
// what names the action. The error names everything that is disabled.
func (a *Admin) checkEnabled(name, what string, perms []map[string]any) error {
	e, err := a.GetEnablement()
	if err != nil || e == nil {
		return err
	}
	var cats []string
	seen := map[string]bool{}
	for _, p := range perms {
		c, _ := p["category"].(string)
		if e.Usable(c) || seen[c] {
			continue
		}
		seen[c] = true
		shown := c
		if c == "" {
			shown = "(none)"
		} else {
			cats = append(cats, c)
		}
		a.record(journal.CategoryRefused, name, fmt.Sprintf("%s: category %s is not enabled on this host (%s)", what, shown, e.State),
			journal.Fields{"CATEGORY": shown, "REASON": what, "BY": a.By})
	}
	if len(seen) == 0 {
		return nil
	}
	sort.Strings(cats)
	var parts []string
	if len(cats) > 0 {
		parts = append(parts, "task categories "+strings.Join(cats, ", "))
	}
	if seen[""] {
		parts = append(parts, "permissions without a category")
	}
	msg := fmt.Sprintf("%s are not enabled on this host (%s)", strings.Join(parts, " and "), e.State)
	if len(cats) > 0 {
		msg += ": agent-admin enable " + strings.Join(cats, " ")
	} else {
		msg += ": every permission needs category: diagnostics, or agent-admin enable --all"
	}
	return errors.New(msg)
}

// checkCategoryEnabled refuses a disabled category for promote.
func (a *Admin) checkCategoryEnabled(name, cat string) error {
	e, err := a.GetEnablement()
	if err != nil || e.Usable(cat) {
		return err
	}
	a.record(journal.CategoryRefused, name, fmt.Sprintf("promote: category %s is not enabled on this host (%s)", cat, e.State),
		journal.Fields{"CATEGORY": cat, "REASON": "promote", "BY": a.By})
	return fmt.Errorf("task category %s is not enabled on this host (%s): agent-admin enable %s", cat, e.State, cat)
}

// enablementTampered compares the state in force with its record in the
// journal; "" when they match, or when the journal cannot be read.
func (a *Admin) enablementTampered(e *Enablement) string {
	read := a.JournalJSON
	if read == nil {
		read = func(args ...string) ([]byte, error) {
			return exec.Command("journalctl", append([]string{"-o", "json", "--no-pager"}, args...)...).Output()
		}
	}
	if e.Record == "" {
		return "the state has no record: it was not written by agent-admin"
	}
	out, err := read("_UID=0", "SYSLOG_IDENTIFIER=custos-promotion", "AGENT_EVENT="+journal.CategoriesEnabled, "AGENT_ENABLEMENT="+e.Record)
	if err != nil {
		return ""
	}
	digest := ""
	for _, line := range bytes.Split(out, []byte("\n")) {
		var r map[string]any
		// Root's records only: an agent could write one with any other
		// field (readiness.ByRoot).
		if json.Unmarshal(line, &r) == nil && r["_UID"] == "0" {
			if d, ok := r["AGENT_ENABLEMENT_SHA256"].(string); ok {
				digest = d
			}
		}
	}
	switch {
	case digest == "":
		return "no enablement record " + e.Record + " in the journal"
	case digest != e.Digest():
		return "the state differs from its record " + e.Record
	}
	return ""
}
