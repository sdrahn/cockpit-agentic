package admin

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sdrahn/custos/internal/journal"
)

// fakeJournal holds an agent's records and the gateway's decisions, as
// journalctl -o json gives them.
type fakeJournal struct{ agent, gw []string }

// rootsEvent: the records agent-admin writes, as root.
func rootsEvent(e string) bool {
	return e == "category.review" || e == "category.promoted" || e == "category.demoted" || e == "categories.enabled" || e == "diagnosis.feedback"
}

func (j *fakeJournal) rec(at time.Time, event string, f map[string]string) {
	m := map[string]string{"AGENT_EVENT": event, "__REALTIME_TIMESTAMP": strconv.FormatInt(at.UnixMicro(), 10)}
	for k, v := range f {
		m["AGENT_"+k] = v
	}
	if rootsEvent(event) {
		m["_UID"] = "0" // agent-admin's records are root's (journald's trusted field)
	} else {
		m["_UID"] = "981" // the agent's
	}
	b, _ := json.Marshal(m)
	j.agent = append(j.agent, string(b))
}

func (j *fakeJournal) decision(at time.Time, id string, ok bool) {
	msg, _ := json.Marshal(map[string]any{"msg": "mcp", "event": "mcp-approval", "id": id, "ok": ok, "by": "alice", "principal": "agent-patch-pilot"})
	b, _ := json.Marshal(map[string]string{"MESSAGE": string(msg), "__REALTIME_TIMESTAMP": strconv.FormatInt(at.UnixMicro(), 10)})
	j.gw = append(j.gw, string(b))
}

// proposal: a call of the category that waited for an approval, decided ok.
func (j *fakeJournal) proposal(at time.Time, inv, id string, ok bool) {
	f := map[string]string{"INVOCATION": inv, "CALL_ID": "c1", "TOOL": "zypp__install_patches"}
	j.rec(at, "tool.call", f)
	j.rec(at.Add(time.Second), "approval.pending", map[string]string{"INVOCATION": inv, "CALL_ID": "c1", "TOOL": "zypp__install_patches", "APPROVAL_ID": id})
	j.decision(at.Add(time.Minute), id, ok)
}

// autonomous: a call of the category that ran without an approval.
func (j *fakeJournal) autonomous(at time.Time, inv, outcome string) {
	f := map[string]string{"INVOCATION": inv, "CALL_ID": "c1", "TOOL": "zypp__install_patches"}
	j.rec(at, "tool.call", f)
	r := map[string]string{"INVOCATION": inv, "CALL_ID": "c1", "TOOL": "zypp__install_patches", "OUTCOME": outcome}
	if outcome == "error" {
		r["REASON"] = "zypper: conflict"
	}
	j.rec(at.Add(time.Second), "tool.result", r)
}

func (j *fakeJournal) read(args ...string) ([]byte, error) {
	for _, a := range args {
		if a == "mcp-gateway.service" {
			return []byte(strings.Join(j.gw, "\n")), nil
		}
	}
	return []byte(strings.Join(j.agent, "\n")), nil
}

// promoted: patch-pilot with patch-errata promoted a day before now, with
// the given rollback, and the journal j.
func promoted(t *testing.T, rollback string) (*Admin, *journal.Memory, *fakeJournal, *rec) {
	t.Helper()
	a, mem, r := withCategoriesRec(t)
	// Promoted at the test clock, then evaluated a day later.
	if err := promote(a, "patch-errata", sat+rollback); err != nil {
		t.Fatal(err)
	}
	mem.Records = nil
	day := a.Now().Add(24 * time.Hour)
	a.Now = func() time.Time { return day }
	j := &fakeJournal{}
	a.JournalJSON = j.read
	return a, mem, j, r
}

func evaluate(t *testing.T, a *Admin) []Demotion {
	t.Helper()
	d, err := a.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mode(a *Admin, cat string) string {
	d, _ := a.readGateway("patch-pilot")
	if c := d.Categories[cat]; c != nil {
		return c.Mode
	}
	return ""
}

func TestPromoteEnablesTheEvaluator(t *testing.T) {
	_, _, _, r := promoted(t, "")
	if !strings.Contains(strings.Join(r.cmds, "\n"), "systemctl enable --now custos-demote.timer") {
		t.Errorf("custos-demote.timer not enabled: %q", r.cmds)
	}
}

func TestNothingToDemote(t *testing.T) {
	a, mem, j, _ := promoted(t, "rollback: {on: [tool_error, limit_exceeded]}\n")
	at := a.Now().Add(-time.Hour)
	j.autonomous(at, "e-1", "ok")
	j.proposal(at, "e-2", "a-2", true)
	if d := evaluate(t, a); len(d) != 0 || mode(a, "patch-errata") != "bounded" || len(mem.Records) != 0 {
		t.Errorf("demoted %v, records %v", d, mem.Records)
	}
}

func TestDemotesOnTheOverrideRate(t *testing.T) {
	a, mem, j, _ := promoted(t, "")
	at := a.Now().Add(-time.Hour)
	// Declined before the promotion: the supervised period, not counted.
	j.proposal(a.Now().Add(-48*time.Hour), "e-0", "a-0", false)
	if d := evaluate(t, a); len(d) != 0 {
		t.Fatalf("demoted for a decline before the promotion: %v", d)
	}
	// Outside its windows, a call of the bounded category asks, and is declined.
	j.proposal(at, "e-1", "a-1", false)
	d := evaluate(t, a)
	if len(d) != 1 || d[0].Category != "patch-errata" || !strings.Contains(d[0].Reason, "override rate 100.0 % above 5.0 %") {
		t.Fatalf("demotions %v", d)
	}
	if mode(a, "patch-errata") != "supervised" {
		t.Error("not demoted in the gateway data")
	}
	r := mem.Events(journal.CategoryDemoted)
	if len(r) != 1 || r[0].Fields["BY"] != DemoteBy || !strings.Contains(r[0].Fields["REASON"], "override rate") {
		t.Errorf("record %v", r)
	}
	// Demoted once: a supervised category is not evaluated.
	if d := evaluate(t, a); len(d) != 0 {
		t.Errorf("demoted again: %v", d)
	}
}

func TestRollbackToolError(t *testing.T) {
	a, _, j, _ := promoted(t, "rollback: {on: [tool_error]}\n")
	at := a.Now().Add(-time.Hour)
	// An error of a call that was approved is not the promotion's.
	j.proposal(at, "e-1", "a-1", true)
	j.rec(at.Add(2*time.Minute), "tool.result", map[string]string{"INVOCATION": "e-1", "CALL_ID": "c1", "TOOL": "zypp__install_patches", "APPROVAL_ID": "a-1", "OUTCOME": "error"})
	if d := evaluate(t, a); len(d) != 0 {
		t.Fatalf("demoted for an approved call's error: %v", d)
	}
	j.autonomous(at.Add(10*time.Minute), "e-2", "error")
	d := evaluate(t, a)
	if len(d) != 1 || !strings.Contains(d[0].Reason, "rollback tool_error: zypp__install_patches failed") || !strings.Contains(d[0].Reason, "zypper: conflict") {
		t.Fatalf("demotions %v", d)
	}
}

func TestRollbackOnlyWhatIsListed(t *testing.T) {
	a, _, j, _ := promoted(t, "rollback: {on: [limit_exceeded]}\n")
	at := a.Now().Add(-time.Hour)
	j.autonomous(at, "e-1", "error")
	if d := evaluate(t, a); len(d) != 0 {
		t.Fatalf("demoted for tool_error, which is not listed: %v", d)
	}
	j.autonomous(at.Add(time.Minute), "e-2", "ok")
	j.rec(at.Add(2*time.Minute), "limit.exceeded", map[string]string{"INVOCATION": "e-2", "REASON": "max_tool_calls=40"})
	d := evaluate(t, a)
	if len(d) != 1 || !strings.Contains(d[0].Reason, "rollback limit_exceeded: max_tool_calls=40 in activity e-2") {
		t.Fatalf("demotions %v", d)
	}
}

func TestRollbackHealthCheck(t *testing.T) {
	a, _, _, _ := promoted(t, "rollback: {on: [health_check_failed], health_check: 'systemctl is-system-running --quiet'}\n")
	var ran []string
	healthy := true
	a.HealthCheck = func(cmd string) error {
		ran = append(ran, cmd)
		if healthy {
			return nil
		}
		return errors.New("exit status 1")
	}
	if d := evaluate(t, a); len(d) != 0 || len(ran) != 1 || ran[0] != "systemctl is-system-running --quiet" {
		t.Fatalf("healthy: demoted %v, ran %q", d, ran)
	}
	healthy = false
	d := evaluate(t, a)
	if len(d) != 1 || d[0].Reason != `rollback health_check_failed: "systemctl is-system-running --quiet": exit status 1` {
		t.Fatalf("demotions %v", d)
	}
}

func TestRealHealthCheck(t *testing.T) {
	if err := runHealthCheck("true"); err != nil {
		t.Error(err)
	}
	if err := runHealthCheck("echo broken >&2; exit 3"); err == nil || !strings.Contains(err.Error(), "exit status 3: broken") {
		t.Errorf("%v", err)
	}
}

// A proposal declined in the same second just before the promotion is the
// supervised period's: the promotion time is kept to the fraction of the
// second (as the functional tests found, demoting at once for 100 %).
func TestDeclineInTheSecondBeforeThePromotion(t *testing.T) {
	a, _, _ := withCategoriesRec(t)
	at := time.Date(2026, 11, 3, 12, 0, 0, 900_000_000, time.UTC)
	a.Now = func() time.Time { return at }
	j := &fakeJournal{}
	a.JournalJSON = j.read
	f := map[string]string{"INVOCATION": "e-1", "CALL_ID": "c1", "TOOL": "zypp__install_patches"}
	j.rec(at.Add(-600*time.Millisecond), "tool.call", f)
	j.rec(at.Add(-500*time.Millisecond), "approval.pending", map[string]string{"INVOCATION": "e-1", "CALL_ID": "c1",
		"TOOL": "zypp__install_patches", "APPROVAL_ID": "a-1"})
	j.decision(at.Add(-400*time.Millisecond), "a-1", false)
	if err := promote(a, "patch-errata", sat); err != nil {
		t.Fatal(err)
	}
	d, _ := a.readGateway("patch-pilot")
	if pb := d.Categories["patch-errata"].PromotedBy; pb != "alice 2026-11-03T12:00:00.9Z" {
		t.Errorf("promoted_by %q", pb)
	}
	later := at.Add(time.Hour)
	a.Now = func() time.Time { return later }
	if d := evaluate(t, a); len(d) != 0 || mode(a, "patch-errata") != "bounded" {
		t.Errorf("demoted for a decline before the promotion: %v", d)
	}
}
