package gateway

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseNotice(t *testing.T) {
	for _, tc := range []struct {
		data   any
		logger string
		ok     bool
		want   Notice
	}{
		{"Waiting for approval of zypp/install_patches on the mcp-gateway approvals page (id a-1f2e).", "mcp-gateway", true,
			Notice{Server: "zypp", Tool: "install_patches", Where: "on the mcp-gateway approvals page", ApprovalID: "a-1f2e"}},
		{"Waiting for approval of fs/write_file at https://host:9090/cockpit/@localhost/mcp-gateway/index.html#/approvals/a-9 (id a-9).", "mcp-gateway", true,
			Notice{Server: "fs", Tool: "write_file", Where: "https://host:9090/cockpit/@localhost/mcp-gateway/index.html#/approvals/a-9", ApprovalID: "a-9"}},
		{"Waiting for approval of zypp/install on the page (id a-1).", "zypp", false, Notice{}},
		{"something else", "mcp-gateway", false, Notice{}},
		{map[string]any{"x": 1}, "mcp-gateway", false, Notice{}},
	} {
		got, ok := ParseNotice(&mcp.LoggingMessageParams{Logger: tc.logger, Data: tc.data, Level: "notice"})
		if ok != tc.ok || got != tc.want {
			t.Errorf("%v: got %+v %v, want %+v %v", tc.data, got, ok, tc.want, tc.ok)
		}
	}
	if n := (Notice{Server: "zypp", Tool: "install"}); n.ToolName() != "zypp__install" {
		t.Error(n.ToolName())
	}
}

// The notice's _meta names the call (mcp-gateway's MetaApproval), for
// the approval the text names.
func TestParseNoticeNamesCall(t *testing.T) {
	text := "Waiting for approval of zypp/install on the mcp-gateway approvals page (id a-2)."
	n, ok := ParseNotice(&mcp.LoggingMessageParams{Logger: "mcp-gateway", Data: text,
		Meta: mcp.Meta{MetaApproval: map[string]any{"id": "a-2", "requestId": 7.0, "progressToken": "custos:e-1/c-2"}}})
	if !ok || n.ApprovalID != "a-2" || n.ProgressToken != "custos:e-1/c-2" {
		t.Errorf("got %+v %v", n, ok)
	}
	// Another approval's _meta is not this one's.
	n, _ = ParseNotice(&mcp.LoggingMessageParams{Logger: "mcp-gateway", Data: text,
		Meta: mcp.Meta{MetaApproval: map[string]any{"id": "a-3", "progressToken": "custos:e-1/c-3"}}})
	if n.ProgressToken != "" {
		t.Errorf("took another approval's call: %+v", n)
	}
}

// adkCtx stands in for ADK's tool.Context.
type adkCtx struct {
	context.Context
	inv, call string
}

func (c adkCtx) InvocationID() string   { return c.inv }
func (c adkCtx) FunctionCallID() string { return c.call }

// A tools/call from an ADK tool carries its call's progress token; one
// from elsewhere carries none.
func TestCallsCarryToken(t *testing.T) {
	ctx := context.Background()
	got := make(chan any, 2)
	srv := mcp.NewServer(&mcp.Implementation{Name: "gw"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "probe"}, func(_ context.Context, r *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		got <- r.Params.GetProgressToken()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	s, err := NewClient("pp", "test", nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CallTool(adkCtx{ctx, "e-1", "c-9"}, &mcp.CallToolParams{Name: "probe"}); err != nil {
		t.Fatal(err)
	}
	if tok := <-got; tok != CallToken("e-1", "c-9") {
		t.Errorf("token %v, want %s", tok, CallToken("e-1", "c-9"))
	}
	if _, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "probe"}); err != nil {
		t.Fatal(err)
	}
	if tok := <-got; tok != nil {
		t.Errorf("a call from outside ADK got token %v", tok)
	}
}
