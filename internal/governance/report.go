package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/sdrahn/custos/internal/journal"
)

// The diagnosis report (CONTRACT.md §4a). The model writes it with the
// host's tool ReportTool; the host checks it and verifies each evidence
// quote against what the tool of that name returned in this activity.

// ReportTool is the host's tool the model reports with. Its name has no
// "__": it is no gateway tool, and it changes nothing.
const ReportTool = "report_diagnosis"

// EvReport is the AG-UI CUSTOM event of a report (§11).
const EvReport = "agent.report"

// Report is a diagnosis report as the activity and the journal hold it.
type Report struct {
	Kind       string       `json:"kind"`
	Playbook   string       `json:"playbook,omitempty"`
	Summary    string       `json:"summary"`
	Findings   []Finding    `json:"findings"`
	Hypotheses []Hypothesis `json:"hypotheses"`
	Evidence   []Evidence   `json:"evidence"`
	Plan       []PlanStep   `json:"plan"`
}

type Finding struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Evidence []string `json:"evidence"`
}

type Hypothesis struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Confidence string   `json:"confidence"`
	Evidence   []string `json:"evidence"`
}

type Evidence struct {
	ID     string `json:"id"`
	CallID string `json:"call_id"`
	Tool   string `json:"tool"`
	Quote  string `json:"quote"`
	// Verified is the host's: the quote is in the result of CallID.
	Verified bool `json:"verified"`
}

type PlanStep struct {
	Step    int    `json:"step"`
	Text    string `json:"text"`
	Command string `json:"command,omitempty"`
	Risk    string `json:"risk,omitempty"`
}

// ReportInput is what the model gives the tool: evidence names the tool
// and the quote; the host finds the call and verifies it.
type ReportInput struct {
	Summary    string          `json:"summary" jsonschema:"what you found, in one or two sentences"`
	Findings   []Finding       `json:"findings" jsonschema:"what you observed, each with the ids of the evidence it rests on"`
	Hypotheses []Hypothesis    `json:"hypotheses" jsonschema:"your root-cause hypotheses, each with confidence low, medium or high and the ids of its evidence"`
	Evidence   []EvidenceInput `json:"evidence" jsonschema:"quotes from the output of the tools you called in this activity"`
	Plan       []PlanStepInput `json:"plan" jsonschema:"a remediation plan for the administrator, step by step"`
}

type EvidenceInput struct {
	ID    string `json:"id" jsonschema:"an id such as e1, which findings and hypotheses cite"`
	Tool  string `json:"tool" jsonschema:"the tool whose output you quote, as you called it"`
	Quote string `json:"quote" jsonschema:"the line or value, copied exactly from the tool's output, at most 500 characters"`
}

type PlanStepInput struct {
	Text    string `json:"text" jsonschema:"what to do"`
	Command string `json:"command,omitempty" jsonschema:"the command that does it, if any"`
	Risk    string `json:"risk,omitempty" jsonschema:"what it risks, if anything"`
}

const (
	maxQuote      = 500
	maxReport     = 64 << 10 // AGENT_REPORT
	maxResultText = 1 << 20  // kept per activity for the verification
)

// NewReportTool is the host's report tool for the agent.
func (g *Governor) NewReportTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: ReportTool,
		Description: "Records your diagnosis report: findings, root-cause hypotheses with your confidence, the evidence " +
			"(exact quotes from the tools' output in this activity) and a remediation plan for the administrator. " +
			"It changes nothing on the host. The host checks each quote against the tool's output; call it once, at the end.",
	}, func(tctx agent.ToolContext, in ReportInput) (map[string]any, error) {
		r, err := g.SubmitReport(tctx.InvocationID(), in)
		if err != nil {
			return map[string]any{"error": err.Error() + "; correct it and call " + ReportTool + " again"}, nil
		}
		var unverified []string
		for _, e := range r.Evidence {
			if !e.Verified {
				unverified = append(unverified, e.ID)
			}
		}
		out := map[string]any{"recorded": true}
		if len(unverified) > 0 {
			out["unverified"] = unverified
			out["note"] = "these quotes are not in the output of the tool they name: they are shown as unverified"
		}
		return out, nil
	})
}

// SubmitReport checks a report for the activity, verifies its evidence,
// records it (the activity's report; diagnosis.report) and publishes it.
// A later report replaces an earlier one.
func (g *Governor) SubmitReport(invID string, in ReportInput) (Report, error) {
	r, err := checkReport(in)
	if err != nil {
		return Report{}, err
	}
	g.mu.Lock()
	inv := g.inv[invID]
	if inv == nil {
		g.mu.Unlock()
		return Report{}, errors.New("no running activity")
	}
	r.Playbook = inv.act.Playbook
	for i := range r.Evidence {
		r.Evidence[i].CallID, r.Evidence[i].Verified = inv.verify(r.Evidence[i].Tool, r.Evidence[i].Quote)
	}
	b, _ := json.Marshal(r)
	if len(b) > maxReport {
		g.mu.Unlock()
		return Report{}, fmt.Errorf("the report is %d bytes, more than %d: shorten it", len(b), maxReport)
	}
	cp := r
	inv.act.Report = &cp
	session := inv.session
	g.publish(Event{Kind: EvReport, Session: session, Invocation: invID, Report: &cp})
	g.mu.Unlock()
	verified := 0
	for _, e := range r.Evidence {
		if e.Verified {
			verified++
		}
	}
	g.log.Log(journal.DiagnosisReport, fmt.Sprintf("Diagnosis report of %s: %d hypotheses, %d of %d quotes verified", invID, len(r.Hypotheses), verified, len(r.Evidence)),
		journal.Fields{"SESSION": session, "INVOCATION": invID, "PLAYBOOK": r.Playbook, "REPORT": string(b)})
	return r, nil
}

func checkReport(in ReportInput) (Report, error) {
	r := Report{Kind: "diagnosis", Summary: strings.TrimSpace(in.Summary),
		Findings: []Finding{}, Hypotheses: []Hypothesis{}, Evidence: []Evidence{}, Plan: []PlanStep{}}
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	if r.Summary == "" {
		bad("summary: missing")
	}
	ids := map[string]bool{}
	for i, e := range in.Evidence {
		switch {
		case e.ID == "" || ids[e.ID]:
			bad("evidence[%d].id: missing or used twice", i)
		case strings.TrimSpace(e.Tool) == "":
			bad("evidence[%d].tool: missing", i)
		case strings.TrimSpace(e.Quote) == "" || len(e.Quote) > maxQuote:
			bad("evidence[%d].quote: 1 to %d characters", i, maxQuote)
		}
		ids[e.ID] = true
		r.Evidence = append(r.Evidence, Evidence{ID: e.ID, Tool: strings.TrimSpace(e.Tool), Quote: e.Quote})
	}
	cites := func(what string, ev []string) {
		for _, id := range ev {
			if !ids[id] {
				bad("%s: evidence %q is not in evidence", what, id)
			}
		}
	}
	seen := map[string]bool{}
	for i, f := range in.Findings {
		if f.ID == "" || seen[f.ID] || strings.TrimSpace(f.Text) == "" {
			bad("findings[%d]: needs a unique id and a text", i)
		}
		seen[f.ID] = true
		cites(fmt.Sprintf("findings[%d]", i), f.Evidence)
		if f.Evidence == nil {
			f.Evidence = []string{}
		}
		r.Findings = append(r.Findings, f)
	}
	for i, h := range in.Hypotheses {
		if h.ID == "" || seen[h.ID] || strings.TrimSpace(h.Text) == "" {
			bad("hypotheses[%d]: needs a unique id and a text", i)
		}
		seen[h.ID] = true
		h.Confidence = strings.ToLower(strings.TrimSpace(h.Confidence))
		if h.Confidence != "low" && h.Confidence != "medium" && h.Confidence != "high" {
			bad("hypotheses[%d].confidence: want low, medium or high", i)
		}
		cites(fmt.Sprintf("hypotheses[%d]", i), h.Evidence)
		if h.Evidence == nil {
			h.Evidence = []string{}
		}
		r.Hypotheses = append(r.Hypotheses, h)
	}
	for i, p := range in.Plan {
		if strings.TrimSpace(p.Text) == "" {
			bad("plan[%d].text: missing", i)
		}
		r.Plan = append(r.Plan, PlanStep{Step: i + 1, Text: p.Text, Command: p.Command, Risk: p.Risk})
	}
	return r, errors.Join(errs...)
}

// toolResult is a tool's output in an activity, as text, kept for the
// verification of the report's quotes.
type toolResult struct {
	callID, tool, text string
}

// keepResult keeps a result's strings for the verification, up to
// maxResultText per activity; g.mu must be held.
func (in *invocation) keepResult(callID, tool string, result map[string]any) {
	var parts []string
	collectStrings(result, &parts)
	text := normalize(strings.Join(parts, "\n"))
	if in.resultBytes+len(text) > maxResultText {
		text = text[:max(0, maxResultText-in.resultBytes)]
	}
	in.resultBytes += len(text)
	in.results = append(in.results, toolResult{callID: callID, tool: tool, text: text})
}

// verify finds the quote in the output of the tool: the newest call whose
// output has it; otherwise the newest call of the tool, unverified.
func (in *invocation) verify(tool, quote string) (callID string, ok bool) {
	q := normalize(quote)
	for i := len(in.results) - 1; i >= 0; i-- {
		r := in.results[i]
		if r.tool != tool {
			continue
		}
		if callID == "" {
			callID = r.callID
		}
		if q != "" && strings.Contains(r.text, q) {
			return r.callID, true
		}
	}
	return callID, false
}

// collectStrings gathers every string of a result (MCP content texts,
// structured values), in a stable order.
func collectStrings(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []any:
		for _, e := range x {
			collectStrings(e, out)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectStrings(x[k], out)
		}
	case nil, bool, float64, int, int64, json.Number:
		if x != nil {
			*out = append(*out, fmt.Sprint(x))
		}
	default:
		// Typed values (MCP content structs): through JSON.
		b, err := json.Marshal(x)
		if err == nil {
			var y any
			if json.Unmarshal(b, &y) == nil {
				collectStrings(y, out)
			}
		}
	}
}

// normalize makes runs of white space one space, so that a quote matches
// whatever the line breaks and column alignment.
func normalize(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}
