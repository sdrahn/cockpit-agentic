package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sdrahn/cockpit-agentic/internal/openai"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func req(text string, tools ...string) *request {
	r := &request{Messages: []message{{Role: "user", Content: &text}}}
	for _, t := range tools {
		var x struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		x.Function.Name = t
		r.Tools = append(r.Tools, x)
	}
	return r
}

func TestScript(t *testing.T) {
	m := &Model{Prefix: "agentic-test"}
	for _, tc := range []struct {
		in, tool, text string
		tools          []string
	}{
		{"please PROBE", "agentic-test__probe", "", []string{"agentic-test__probe"}},
		{"CHANGE it", "agentic-test__change", "Writing the test marker; no other impact.", []string{"agentic-test__change"}},
		{"CHANGE it", "", "not offered: agentic-test__change", nil},
		{"DELEGATE t1 PROBE now", "agents__delegate", "Handing this to t1.", []string{"agents__delegate"}},
		{"hello", "", "ok", []string{"agentic-test__probe"}},
	} {
		got := m.Respond(req(tc.in, tc.tools...))
		tool := ""
		if len(got.ToolCalls) > 0 {
			tool = got.ToolCalls[0].Function.Name
		}
		text := ""
		if got.Content != nil {
			text = *got.Content
		}
		if tool != tc.tool || text != tc.text {
			t.Errorf("%q: tool %q text %q, want %q %q", tc.in, tool, text, tc.tool, tc.text)
		}
	}
	d := m.Respond(req("DELEGATE t1 PROBE now", "agents__delegate"))
	var args map[string]string
	json.Unmarshal([]byte(d.ToolCalls[0].Function.Arguments), &args)
	if args["agent"] != "t1" || args["task"] != "PROBE now" {
		t.Errorf("delegate args %v", args)
	}
	res := "probe ok"
	r := &request{Messages: []message{{Role: "tool", Content: &res}}}
	if got := m.Respond(r); *got.Content != "done: probe ok" {
		t.Errorf("after tool: %q", *got.Content)
	}
}

// The launcher's own model adapter understands it: the same path the
// installed agents use.
func TestWithLauncherAdapter(t *testing.T) {
	srv := httptest.NewServer(&Model{Prefix: "agentic-test"})
	defer srv.Close()
	llm := (&openai.Model{Endpoint: srv.URL + "/v1", Name: "fake"}).LLM()
	lr := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("PROBE", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{
			{Name: "agentic-test__probe", ParametersJsonSchema: map[string]any{"type": "object"}},
		}}}},
	}
	for resp, err := range llm.GenerateContent(t.Context(), lr, false) {
		if err != nil {
			t.Fatal(err)
		}
		if fc := resp.Content.Parts[0].FunctionCall; fc == nil || fc.Name != "agentic-test__probe" {
			t.Errorf("response %+v", resp.Content.Parts[0])
		}
	}
	res, _ := http.Post(srv.URL+"/v1/other", "application/json", bytes.NewReader(nil))
	if res.StatusCode != 404 {
		t.Errorf("other path: %d", res.StatusCode)
	}
}
