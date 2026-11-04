// Package readiness measures a task category's promotion readiness
// (CONTRACT.md §6a): from the agent's journal records and the gateway's
// approval decisions, how often approvers overrode the category's
// proposals, how often approved calls went wrong, and how much was
// observed.
package readiness

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Thresholds a category must meet to be promoted without
// --despite-readiness (/etc/custos/promotion.yaml).
type Thresholds struct {
	// ObservationDays: since the category's first decided proposal.
	ObservationDays int `yaml:"observation_days" json:"observation_days"`
	// Proposals: decided approval items of the category.
	Proposals int `yaml:"proposals" json:"proposals"`
	// OverrideRate and IncidentRate are fractions (0.05 is 5 %).
	OverrideRate float64 `yaml:"override_rate" json:"override_rate"`
	IncidentRate float64 `yaml:"incident_rate" json:"incident_rate"`
	// WindowDays is how far back the records are read.
	WindowDays int `yaml:"window_days" json:"window_days"`
}

// Defaults are §6a's.
var Defaults = Thresholds{ObservationDays: 14, Proposals: 50, OverrideRate: 0.05, IncidentRate: 0.02, WindowDays: 30}

// Record is one journal record of the agent (journalctl -o json):
// AGENT_* fields and __REALTIME_TIMESTAMP.
type Record map[string]string

// Decision is the gateway's mcp-approval record for one approval id.
type Decision struct {
	OK bool
	By string
	At time.Time
}

// Measures are one category's readiness (§6a).
type Measures struct {
	// Proposals: approval items of the category the gateway's approvers
	// decided (approved or declined); expired ones have no decision.
	Proposals int `json:"proposals"`
	Approved  int `json:"approved"`
	Declined  int `json:"declined"`
	// Cancelled: approved, then the activity was cancelled before the
	// call ran.
	Cancelled int `json:"cancelled"`
	// Overrides = Declined + Cancelled; OverrideRate = Overrides / Proposals.
	Overrides    int     `json:"overrides"`
	OverrideRate float64 `json:"override_rate"`
	// Incidents among the approved calls: the tool failed, a limit was
	// reached in its activity, or the agent was paused during it.
	// IncidentRate = Incidents / Approved.
	Incidents    int     `json:"incidents"`
	IncidentRate float64 `json:"incident_rate"`
	// Diversity: distinct tools, and distinct calls (tool and arguments'
	// digest), among the proposals.
	Tools int `json:"tools"`
	Calls int `json:"distinct_calls"`
	// ObservedDays: from the first decided proposal to now.
	ObservedDays float64   `json:"observed_days"`
	First        time.Time `json:"-"`
	// FirstProposal is First as RFC 3339, when there is one.
	FirstProposal string `json:"first_proposal,omitempty"`
	// Ready: every threshold met; Unmet says which are not.
	Ready bool     `json:"ready"`
	Unmet []string `json:"unmet,omitempty"`
}

// Measure computes a category's readiness. perms are the agent's
// permissions (the gateway data); a call belongs to the category when a
// permission of the category that is not read-only matches its tool.
func Measure(category string, perms []map[string]any, recs []Record, decisions map[string]Decision, th Thresholds, now time.Time) Measures {
	type key struct{ inv, call string }
	calls := map[key]Record{}
	results := map[key]Record{}
	var proposals []Record
	cancels := map[string][]time.Time{} // invocation → cancel times
	limits := map[string]bool{}         // invocations with a limit reached
	ends := map[string]time.Time{}      // invocation → its end
	var pauses []time.Time
	for _, r := range recs {
		k := key{r["AGENT_INVOCATION"], r["AGENT_CALL_ID"]}
		switch r["AGENT_EVENT"] {
		case "tool.call":
			calls[k] = r
		case "tool.result":
			results[k] = r
		case "approval.pending":
			if r["AGENT_APPROVAL_ID"] != "" && InCategory(category, r["AGENT_TOOL"], perms) {
				proposals = append(proposals, r)
			}
		case "activity.cancel":
			cancels[r["AGENT_INVOCATION"]] = append(cancels[r["AGENT_INVOCATION"]], At(r))
		case "limit.exceeded":
			limits[r["AGENT_INVOCATION"]] = true
		case "invocation.end":
			ends[r["AGENT_INVOCATION"]] = At(r)
		case "agent.paused":
			pauses = append(pauses, At(r))
		}
	}
	var m Measures
	tools, distinct := map[string]bool{}, map[string]bool{}
	seen := map[string]bool{}
	for _, p := range proposals {
		id := p["AGENT_APPROVAL_ID"]
		d, ok := decisions[id]
		if !ok || seen[id] {
			continue // expired or still pending; or a second notice
		}
		seen[id] = true
		k := key{p["AGENT_INVOCATION"], p["AGENT_CALL_ID"]}
		m.Proposals++
		if m.First.IsZero() || At(p).Before(m.First) {
			m.First = At(p)
		}
		tools[p["AGENT_TOOL"]] = true
		distinct[p["AGENT_TOOL"]+" "+calls[k]["AGENT_ARGS_SHA256"]] = true
		if !d.OK {
			m.Declined++
			continue
		}
		m.Approved++
		res, ran := results[k]
		if cancelledBefore(cancels[k.inv], d.At, res, ran) {
			m.Cancelled++
			continue
		}
		end, ended := ends[k.inv]
		if !ended {
			end = now
		}
		switch {
		case ran && res["AGENT_OUTCOME"] == "error":
			m.Incidents++
		case limits[k.inv]:
			m.Incidents++
		case pausedBetween(pauses, At(p), end):
			m.Incidents++
		}
	}
	m.Overrides = m.Declined + m.Cancelled
	if m.Proposals > 0 {
		m.OverrideRate = float64(m.Overrides) / float64(m.Proposals)
	}
	if m.Approved-m.Cancelled > 0 {
		m.IncidentRate = float64(m.Incidents) / float64(m.Approved-m.Cancelled)
	}
	m.Tools, m.Calls = len(tools), len(distinct)
	if !m.First.IsZero() {
		m.ObservedDays = now.Sub(m.First).Hours() / 24
		m.FirstProposal = m.First.UTC().Format(time.RFC3339)
	}
	if m.ObservedDays < float64(th.ObservationDays) {
		m.Unmet = append(m.Unmet, fmt.Sprintf("observed %.1f days of %d", m.ObservedDays, th.ObservationDays))
	}
	if m.Proposals < th.Proposals {
		m.Unmet = append(m.Unmet, fmt.Sprintf("%d proposals of %d", m.Proposals, th.Proposals))
	}
	if m.OverrideRate > th.OverrideRate {
		m.Unmet = append(m.Unmet, fmt.Sprintf("override rate %s above %s", pct(m.OverrideRate), pct(th.OverrideRate)))
	}
	if m.IncidentRate > th.IncidentRate {
		m.Unmet = append(m.Unmet, fmt.Sprintf("incident rate %s above %s", pct(m.IncidentRate), pct(th.IncidentRate)))
	}
	m.Ready = len(m.Unmet) == 0
	return m
}

// cancelledBefore: the activity was cancelled after the approval and
// before the call had a result.
func cancelledBefore(cancels []time.Time, approved time.Time, res Record, ran bool) bool {
	for _, c := range cancels {
		if !c.Before(approved) && (!ran || c.Before(At(res))) {
			return true
		}
	}
	return false
}

func pausedBetween(pauses []time.Time, from, to time.Time) bool {
	for _, p := range pauses {
		if !p.Before(from) && !p.After(to) {
			return true
		}
	}
	return false
}

func pct(f float64) string { return strconv.FormatFloat(f*100, 'f', 1, 64) + " %" }

// InCategory: a permission of the category that changes the system
// matches the tool (server__name; globs as the gateway's).
func InCategory(category, tool string, perms []map[string]any) bool {
	srv, name, ok := strings.Cut(tool, "__")
	if !ok {
		return false
	}
	for _, p := range perms {
		if p["category"] != category || p["read_only"] == true {
			continue
		}
		ps, _ := p["server"].(string)
		pt, _ := p["tool"].(string)
		if pt == "" {
			pt = "*"
		}
		if okS, _ := path.Match(ps, srv); !okS {
			continue
		}
		if okT, _ := path.Match(pt, name); okT {
			return true
		}
	}
	return false
}

// Categories are the categories named by perms, sorted.
func Categories(perms []map[string]any) []string {
	set := map[string]bool{}
	for _, p := range perms {
		if c, ok := p["category"].(string); ok {
			set[c] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// At is a record's time (__REALTIME_TIMESTAMP, microseconds).
func At(r Record) time.Time {
	us, err := strconv.ParseInt(r["__REALTIME_TIMESTAMP"], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(us)
}

// ParseRecords reads journalctl -o json lines.
func ParseRecords(b []byte) []Record {
	var out []Record
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		r := Record{}
		for k, v := range raw {
			if s, ok := v.(string); ok {
				r[k] = s
			}
		}
		out = append(out, r)
	}
	return out
}

// ParseDecisions reads the gateway's journal (journalctl -o json -u
// mcp-gateway.service) for one principal's mcp-approval records: its
// MESSAGE is a JSON line of the gateway's log, with event, id, ok, by.
func ParseDecisions(b []byte, principal string) map[string]Decision {
	out := map[string]Decision{}
	for _, r := range ParseRecords(b) {
		var msg struct {
			Event     string `json:"event"`
			ID        string `json:"id"`
			OK        bool   `json:"ok"`
			By        string `json:"by"`
			Principal string `json:"principal"`
		}
		if json.Unmarshal([]byte(r["MESSAGE"]), &msg) != nil || msg.Event != "mcp-approval" || msg.ID == "" || msg.Principal != principal {
			continue
		}
		out[msg.ID] = Decision{OK: msg.OK, By: msg.By, At: At(r)}
	}
	return out
}
