// Package delegate is the "agents" MCP server the gateway runs for
// agents that delegate (CONTRACT.md §9): one tool, delegate, which
// starts an activity on another agent and waits for it.
//
// The gateway runs it with run_as: principal, so it runs as the
// delegating agent's user and the callee identifies the caller by
// SO_PEERCRED. Whether an agent may delegate to another is gateway
// policy on the tool agents__delegate and its argument "agent".
package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sdrahn/cockpit-agentic/internal/config"
)

type Args struct {
	Agent string `json:"agent" jsonschema:"the agent to delegate to"`
	Task  string `json:"task" jsonschema:"what the agent should do, in plain words"`
	// Trace is added by the delegating agent's governance plugin.
	Trace map[string]string `json:"_trace,omitempty" jsonschema:"set by the agent host; leave out"`
}

// Serve runs the server on stdio.
func Serve(ctx context.Context, socketDir, version string) error {
	s := mcp.NewServer(&mcp.Implementation{Name: "cockpit-agentic-agents", Version: version}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delegate",
		Description: "Ask another agent on this host to do a task, and wait for its answer. The other agent works under its own permissions and approvals.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a Args) (*mcp.CallToolResult, any, error) {
		if !config.ValidName(a.Agent) {
			return errResult("unknown agent %q", a.Agent), nil, nil
		}
		res, err := Run(ctx, filepath.Join(socketDir, a.Agent, "api.sock"), map[string]any{"prompt": a.Task, "trace": a.Trace, "wait": true})
		if err != nil {
			return errResult("%v", err), nil, nil
		}
		b, _ := json.Marshal(map[string]any{"agent": a.Agent, "activity": res["id"], "state": res["state"], "answer": res["summary"]})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, IsError: res["state"] != "finished"}, nil, nil
	})
	return s.Run(ctx, &mcp.StdioTransport{})
}

func ptr[T any](v T) *T { return &v }

func errResult(f string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(f, a...)}}, IsError: true}
}

func client(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
}

// Run POSTs /v1/activities on an agent's socket and returns the answer.
func Run(ctx context.Context, sock string, body map[string]any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent/v1/activities", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	res, err := client(sock).Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent not reachable: %w", err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("agent answered %d: %s", res.StatusCode, bytes.TrimSpace(rb))
	}
	var out map[string]any
	if err := json.Unmarshal(rb, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Start starts an activity without waiting (schedules).
func Start(ctx context.Context, sock string, body map[string]any) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err := Run(ctx, sock, body)
	return err
}

// Post sends an empty POST to an agent's control API (pause, resume).
func Post(ctx context.Context, sock, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+path, nil)
	if err != nil {
		return err
	}
	res, err := client(sock).Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("agent answered %d", res.StatusCode)
	}
	return nil
}
