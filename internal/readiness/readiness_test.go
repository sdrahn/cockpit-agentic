package readiness

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

var perms = []map[string]any{
	{"server": "zypp", "tool": "list_*", "read_only": true, "category": "patch-errata"},
	{"server": "zypp", "tool": "patch", "category": "patch-errata"},
	{"server": "systemd", "tool": "restart_unit", "category": "config-lifecycle"},
	{"server": "fs", "tool": "write_file"},
}

// log builds an agent's records; each call is one proposal of tool.
type log struct {
	recs      []Record
	decisions map[string]Decision
	n         int
}

func (l *log) rec(at time.Time, event string, f map[string]string) {
	r := Record{"AGENT_EVENT": event, "__REALTIME_TIMESTAMP": strconv.FormatInt(at.UnixMicro(), 10)}
	for k, v := range f {
		r["AGENT_"+k] = v
	}
	l.recs = append(l.recs, r)
}

// proposal: a call of tool in invocation inv at, its approval item, and
// the gateway's decision (decided false: none, it expired).
func (l *log) proposal(at time.Time, tool, args string, decided, ok bool) (inv, id string) {
	l.n++
	inv, call, id := fmt.Sprintf("e-%d", l.n), "c1", fmt.Sprintf("a-%d", l.n)
	f := map[string]string{"INVOCATION": inv, "CALL_ID": call, "TOOL": tool, "ARGS_SHA256": args}
	l.rec(at, "tool.call", f)
	l.rec(at.Add(time.Second), "approval.pending", map[string]string{"INVOCATION": inv, "CALL_ID": call, "TOOL": tool, "APPROVAL_ID": id})
	if decided {
		if l.decisions == nil {
			l.decisions = map[string]Decision{}
		}
		l.decisions[id] = Decision{OK: ok, By: "alice", At: at.Add(time.Minute)}
	}
	return inv, id
}

func (l *log) result(at time.Time, inv, outcome string) {
	l.rec(at, "tool.result", map[string]string{"INVOCATION": inv, "CALL_ID": "c1", "OUTCOME": outcome})
	l.rec(at.Add(time.Second), "invocation.end", map[string]string{"INVOCATION": inv})
}

func TestMeasures(t *testing.T) {
	var l log
	// 10 patches, approved and run, the first 20 days ago.
	for i := 0; i < 10; i++ {
		at := t0.Add(time.Duration(i) * time.Hour)
		inv, _ := l.proposal(at, "zypp__patch", fmt.Sprintf("args-%d", i%3), true, true)
		l.result(at.Add(2*time.Minute), inv, "ok")
	}
	// One declined.
	l.proposal(t0.Add(20*time.Hour), "zypp__patch", "args-0", true, false)
	// One approved, then cancelled before it ran.
	inv, _ := l.proposal(t0.Add(21*time.Hour), "zypp__patch", "args-1", true, true)
	l.rec(t0.Add(21*time.Hour+2*time.Minute), "activity.cancel", map[string]string{"INVOCATION": inv})
	// One approved that failed: an incident.
	inv, _ = l.proposal(t0.Add(22*time.Hour), "zypp__patch", "args-2", true, true)
	l.result(t0.Add(22*time.Hour+2*time.Minute), inv, "error")
	// One approved whose activity reached a limit: an incident.
	inv, _ = l.proposal(t0.Add(23*time.Hour), "zypp__patch", "args-0", true, true)
	l.rec(t0.Add(23*time.Hour+90*time.Second), "limit.exceeded", map[string]string{"INVOCATION": inv})
	// One approved during which the agent was paused: an incident.
	inv, _ = l.proposal(t0.Add(24*time.Hour), "zypp__patch", "args-1", true, true)
	l.rec(t0.Add(24*time.Hour+90*time.Second), "agent.paused", nil)
	l.result(t0.Add(24*time.Hour+3*time.Minute), inv, "ok")
	// Not counted: expired (no decision), another category, a tool no
	// category's permission matches, a read-only tool of the category.
	l.proposal(t0.Add(25*time.Hour), "zypp__patch", "args-0", false, false)
	l.proposal(t0.Add(26*time.Hour), "systemd__restart_unit", "x", true, false)
	l.proposal(t0.Add(27*time.Hour), "fs__write_file", "x", true, false)
	l.proposal(t0.Add(28*time.Hour), "zypp__list_updates", "x", true, false)

	now := t0.Add(20 * 24 * time.Hour)
	m := Measure("patch-errata", perms, l.recs, l.decisions, Defaults, now)
	want := Measures{Proposals: 15, Approved: 14, Declined: 1, Cancelled: 1, Overrides: 2, Incidents: 3, Tools: 1, Calls: 3}
	if m.Proposals != want.Proposals || m.Approved != want.Approved || m.Declined != want.Declined || m.Cancelled != want.Cancelled ||
		m.Overrides != want.Overrides || m.Incidents != want.Incidents || m.Tools != want.Tools || m.Calls != want.Calls {
		t.Fatalf("measures %+v\nwant      %+v", m, want)
	}
	if fmt.Sprintf("%.4f %.4f", m.OverrideRate, m.IncidentRate) != fmt.Sprintf("%.4f %.4f", 2.0/15, 3.0/13) {
		t.Errorf("rates %v %v", m.OverrideRate, m.IncidentRate)
	}
	if m.ObservedDays < 19.9 || m.ObservedDays > 20.1 || m.FirstProposal != "2026-11-01T09:00:01Z" {
		t.Errorf("observed %v since %s", m.ObservedDays, m.FirstProposal)
	}
	if m.Ready {
		t.Error("ready with 15 proposals and 13 % overrides")
	}
	got := strings.Join(m.Unmet, "; ")
	for _, s := range []string{"15 proposals of 50", "override rate 13.3 % above 5.0 %", "incident rate 23.1 % above 2.0 %"} {
		if !strings.Contains(got, s) {
			t.Errorf("unmet %q, want %q", got, s)
		}
	}
	if strings.Contains(got, "observed") {
		t.Errorf("20 days observed reported unmet: %q", got)
	}
	// The other category's own measures.
	c := Measure("config-lifecycle", perms, l.recs, l.decisions, Defaults, now)
	if c.Proposals != 1 || c.Declined != 1 || c.OverrideRate != 1 {
		t.Errorf("config-lifecycle %+v", c)
	}
}

func TestReady(t *testing.T) {
	var l log
	for i := 0; i < 60; i++ {
		at := t0.Add(time.Duration(i) * 6 * time.Hour)
		inv, _ := l.proposal(at, "zypp__patch", "a", true, true)
		l.result(at.Add(time.Minute+time.Second), inv, "ok")
	}
	m := Measure("patch-errata", perms, l.recs, l.decisions, Defaults, t0.Add(16*24*time.Hour))
	if !m.Ready || len(m.Unmet) != 0 || m.Proposals != 60 || m.OverrideRate != 0 || m.IncidentRate != 0 {
		t.Errorf("%+v", m)
	}
	// Too recent for the observation period.
	m = Measure("patch-errata", perms, l.recs, l.decisions, Thresholds{ObservationDays: 30, Proposals: 50, OverrideRate: 0.05, IncidentRate: 0.02}, t0.Add(16*24*time.Hour))
	if m.Ready || !strings.Contains(strings.Join(m.Unmet, ";"), "observed 16.0 days of 30") {
		t.Errorf("%+v", m)
	}
}

func TestEmpty(t *testing.T) {
	m := Measure("patch-errata", perms, nil, nil, Defaults, t0)
	if m.Ready || m.Proposals != 0 || m.OverrideRate != 0 || m.FirstProposal != "" {
		t.Errorf("%+v", m)
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "0001-01-01") {
		t.Errorf("zero time in %s", b)
	}
}

func TestParse(t *testing.T) {
	agent := `{"AGENT_EVENT":"approval.pending","AGENT_APPROVAL_ID":"a-1","__REALTIME_TIMESTAMP":"1762000000000000","_PID":"12"}
not json
{"AGENT_EVENT":"tool.result","AGENT_OUTCOME":"ok","__REALTIME_TIMESTAMP":"1762000001000000"}`
	r := ParseRecords([]byte(agent))
	if len(r) != 2 || r[0]["AGENT_APPROVAL_ID"] != "a-1" || !At(r[1]).Equal(time.UnixMicro(1762000001000000)) {
		t.Errorf("%v", r)
	}
	msg := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		o, _ := json.Marshal(map[string]string{"MESSAGE": string(b), "__REALTIME_TIMESTAMP": "1762000002000000"})
		return string(o)
	}
	gw := strings.Join([]string{
		msg(map[string]any{"msg": "mcp", "event": "mcp-approval", "id": "a-1", "ok": true, "by": "alice", "principal": "agent-pp"}),
		msg(map[string]any{"msg": "mcp", "event": "mcp-approval", "id": "a-2", "ok": false, "by": "bob", "principal": "agent-pp"}),
		msg(map[string]any{"msg": "mcp", "event": "mcp-approval", "id": "a-3", "ok": true, "by": "bob", "principal": "agent-other"}),
		msg(map[string]any{"msg": "mcp", "event": "mcp-grant-revoke", "id": "g-1", "ok": true, "principal": "agent-pp"}),
		`{"MESSAGE":"time=… level=INFO msg=\"session started\""}`,
	}, "\n")
	d := ParseDecisions([]byte(gw), "agent-pp")
	if len(d) != 2 || !d["a-1"].OK || d["a-1"].By != "alice" || d["a-2"].OK || d["a-1"].At.IsZero() {
		t.Errorf("%v", d)
	}
}

func TestCategories(t *testing.T) {
	if got := strings.Join(Categories(perms), ","); got != "config-lifecycle,patch-errata" {
		t.Errorf("%s", got)
	}
}
