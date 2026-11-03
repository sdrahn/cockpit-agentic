package gateway

import (
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
