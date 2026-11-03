package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestRoundTrip(t *testing.T) {
	var got request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("path %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"zypp__list_updates","arguments":"{\"repo\":\"main\"}"}}]}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer srv.Close()
	m := &Model{Endpoint: srv.URL + "/v1/", Name: "qwen", APIKey: "k"}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("patch it", genai.RoleUser),
			{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "c0", Name: "zypp__x", Args: map[string]any{}}}}},
			{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "c0", Name: "zypp__x", Response: map[string]any{"output": "ok"}}}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText("be careful", genai.RoleUser),
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{
				{Name: "zypp__list_updates", ParametersJsonSchema: map[string]any{"type": "object"}},
				{Name: "g", Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{"n": {Type: genai.TypeInteger}}}},
			}}},
		},
	}
	var resp *model.LLMResponse
	for r, err := range m.LLM().GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		resp = r
	}
	fc := resp.Content.Parts[0].FunctionCall
	if fc == nil || fc.ID != "c1" || fc.Args["repo"] != "main" || resp.UsageMetadata.PromptTokenCount != 7 {
		t.Errorf("response %+v", resp.Content.Parts[0])
	}
	roles := []string{}
	for _, m := range got.Messages {
		roles = append(roles, m.Role)
	}
	if want := []string{"system", "user", "assistant", "tool"}; len(roles) != 4 || roles[0] != want[0] || roles[2] != want[2] || roles[3] != want[3] {
		t.Errorf("roles %v", roles)
	}
	if got.Messages[2].ToolCalls[0].ID != "c0" || got.Messages[3].ToolCallID != "c0" {
		t.Error("tool call ids not carried")
	}
	p := got.Tools[1].Function.Parameters.(map[string]any)
	if p["type"] != "object" || p["properties"].(map[string]any)["n"].(map[string]any)["type"] != "integer" {
		t.Errorf("schema types not lowered: %v", p)
	}
}

func TestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"model not loaded"}}`))
	}))
	defer srv.Close()
	for _, err := range (&Model{Endpoint: srv.URL, Name: "x"}).LLM().GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err == nil || err.Error() != "model endpoint: model not loaded" {
			t.Errorf("err %v", err)
		}
	}
}
