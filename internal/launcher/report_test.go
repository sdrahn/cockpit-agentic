package launcher

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"

	"github.com/sdrahn/custos/internal/governance"
	"github.com/sdrahn/custos/internal/journal"
	"github.com/sdrahn/custos/internal/testutil"
)

// The fake gateway answers "zypp__list_updates done with {}".
func reportArgs(confidence string) map[string]any {
	return map[string]any{
		"summary":  "No updates are pending.",
		"findings": []any{map[string]any{"id": "f1", "text": "The update list is empty.", "evidence": []any{"e1"}}},
		"hypotheses": []any{map[string]any{"id": "h1", "text": "The host is up to date.", "confidence": confidence,
			"evidence": []any{"e1", "e2"}}},
		"evidence": []any{
			// White space differs from the output: still the same text.
			map[string]any{"id": "e1", "tool": "zypp__list_updates", "quote": "list_updates   done\n with"},
			map[string]any{"id": "e2", "tool": "zypp__list_updates", "quote": "0 security patches"},
			map[string]any{"id": "e3", "tool": "zypp__install_patches", "quote": "installed"},
		},
		"plan": []any{map[string]any{"text": "Nothing to do.", "command": "zypper lu"}},
	}
}

// The model reports with the host's tool; the host verifies each quote
// against the output of the tool it names in this activity, and records
// the report in the activity, the journal and on AG-UI.
func TestDiagnosisReport(t *testing.T) {
	var answer map[string]any
	e := newEnv(t,
		testutil.Call("c1", "zypp__list_updates", map[string]any{}),
		testutil.Call("r1", governance.ReportTool, reportArgs("High")),
		func(req *model.LLMRequest) *genai.Content {
			answer = testutil.LastFunctionResponse(req).Response
			return testutil.Text("Reported.")(req)
		})
	ch := e.agui(map[string]any{"threadId": "t1", "messages": []any{map[string]any{"role": "user", "content": "Any updates?"}}})
	_, ev := until(t, ch, "CUSTOM", governance.EvReport)
	until(t, ch, "RUN_FINISHED", "")
	acts := e.l.Governor.Activities()
	r := acts[0].Report
	if r == nil {
		t.Fatalf("no report: %+v", acts[0])
	}
	if r.Kind != "diagnosis" || r.Hypotheses[0].Confidence != "high" || r.Plan[0].Step != 1 || r.Plan[0].Command != "zypper lu" {
		t.Errorf("report %+v", r)
	}
	ev1, ev2, ev3 := r.Evidence[0], r.Evidence[1], r.Evidence[2]
	if !ev1.Verified || ev1.CallID != "c1" {
		t.Errorf("e1 (in the output) %+v", ev1)
	}
	if ev2.Verified || ev2.CallID != "c1" {
		t.Errorf("e2 (not in the output) %+v", ev2)
	}
	if ev3.Verified || ev3.CallID != "" {
		t.Errorf("e3 (a tool not called) %+v", ev3)
	}
	if u, _ := answer["unverified"].([]any); len(u) != 2 || answer["recorded"] != true {
		t.Errorf("the model was told %v", answer)
	}
	for _, c := range acts[0].Calls {
		if c.Tool == governance.ReportTool {
			t.Error("the report tool is listed as a call")
		}
	}
	rec := e.log.Events(journal.DiagnosisReport)
	if len(rec) != 1 {
		t.Fatalf("%d diagnosis.report records", len(rec))
	}
	var jr governance.Report
	if err := json.Unmarshal([]byte(rec[0].Fields["REPORT"]), &jr); err != nil || len(jr.Evidence) != 3 || !jr.Evidence[0].Verified ||
		rec[0].Fields["INVOCATION"] != acts[0].ID {
		t.Errorf("journal %v (%v)", rec[0].Fields, err)
	}
	if v, _ := ev["value"].(map[string]any); v == nil || v["summary"] != "No updates are pending." {
		t.Errorf("AG-UI event %v", ev)
	}
}

// A report that does not hold together is refused, and the model is told
// why; nothing is recorded.
func TestDiagnosisReportRefused(t *testing.T) {
	var answer map[string]any
	bad := reportArgs("certain")
	bad["hypotheses"] = append(bad["hypotheses"].([]any), map[string]any{"id": "h2", "text": "x", "confidence": "low", "evidence": []any{"e9"}})
	e := newEnv(t,
		testutil.Call("r1", governance.ReportTool, bad),
		func(req *model.LLMRequest) *genai.Content {
			answer = testutil.LastFunctionResponse(req).Response
			return testutil.Text("Sorry.")(req)
		})
	res, b := e.do("POST", "/v1/activities", "", map[string]any{"prompt": "Report.", "wait": true})
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	msg, _ := answer["error"].(string)
	if !strings.Contains(msg, "confidence: want low, medium or high") || !strings.Contains(msg, `evidence "e9"`) {
		t.Errorf("the model was told %v", answer)
	}
	if a := e.l.Governor.Activities()[0]; a.Report != nil {
		t.Errorf("recorded %+v", a.Report)
	}
	if n := len(e.log.Events(journal.DiagnosisReport)); n != 0 {
		t.Errorf("%d records", n)
	}
}
