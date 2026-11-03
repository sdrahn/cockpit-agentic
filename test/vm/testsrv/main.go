// testsrv is the MCP server of the functional tests (package
// cockpit-agentic-tests), registered with mcp-gateway as agentic-test.
// It speaks MCP on stdio and has two tools: probe, which reads nothing
// and says so, and change, which stands for a call that changes the
// system (it only answers; the gateway's audit records show that it
// ran). The tests' permissions declare probe read-only and change not.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type changeArgs struct {
	Value string `json:"value" jsonschema:"any text"`
}

// New returns the server.
func New() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "agentic-test", Version: "1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "probe", Description: "Reads nothing; answers probe ok."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "probe ok"}}}, nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "change", Description: "Stands for a change to the system."},
		func(_ context.Context, _ *mcp.CallToolRequest, a changeArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "changed " + a.Value}}}, nil, nil
		})
	return s
}

func main() {
	if err := New().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "testsrv:", err)
		os.Exit(1)
	}
}
