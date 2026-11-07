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

	"github.com/sdrahn/custos/internal/config"
)

type Args struct {
	Agent string `json:"agent" jsonschema:"the agent to delegate to"`
	Task  string `json:"task" jsonschema:"what the agent should do, in plain words"`
	// Trace is added by the delegating agent's governance plugin.
	Trace map[string]string `json:"_trace,omitempty" jsonschema:"set by the agent host; leave out"`
}

// Serve runs the server on stdio.
func Serve(ctx context.Context, socketDir, version string) error {
	s := mcp.NewServer(&mcp.Implementation{Name: "custos-agents", Version: version}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delegate",
		Description: "Ask another agent on this host to do a task, and wait for its answer: its full last response, and its diagnosis report if it wrote one. The other agent works under its own permissions and approvals.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a Args) (*mcp.CallToolResult, any, error) {
		return Ask(ctx, socketDir, a), nil, nil
	})
	return s.Run(ctx, &mcp.StdioTransport{})
}

// Ask is the tool: it starts the task on the agent's socket under
// socketDir, waits for the activity to end, and answers with its result.
func Ask(ctx context.Context, socketDir string, a Args) *mcp.CallToolResult {
	if !config.ValidName(a.Agent) {
		return errResult("unknown agent %q", a.Agent)
	}
	res, err := Run(ctx, filepath.Join(socketDir, a.Agent, "api.sock"), map[string]any{"prompt": a.Task, "trace": a.Trace, "wait": true})
	if err != nil {
		return errResult("%v", err)
	}
	return answer(a.Agent, res)
}

// answer is the delegating agent's result (CONTRACT.md §9): the other
// activity's id and state, its answer, which is its full response (§4;
// up to 32 KiB) or, from an agent before 0.4.2, its 500-byte summary, and
// its diagnosis report (§4a) when it wrote one. An activity that did not
// finish is an error.
func answer(agent string, res map[string]any) *mcp.CallToolResult {
	out := map[string]any{"agent": agent, "activity": res["id"], "state": res["state"], "answer": res["summary"]}
	if r, ok := res["response"].(string); ok && r != "" {
		out["answer"] = r
	}
	if rep, ok := res["report"]; ok && rep != nil {
		out["report"] = rep
	}
	if reason, ok := res["reason"].(string); ok && reason != "" {
		out["reason"] = reason
	}
	b, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, IsError: res["state"] != "finished"}
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

// Activities reads an agent's activities (GET /v1/activities), as root.
func Activities(ctx context.Context, sock string) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent/v1/activities", nil)
	if err != nil {
		return nil, err
	}
	res, err := client(sock).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("agent answered %d", res.StatusCode)
	}
	var out struct {
		Activities []map[string]any `json:"activities"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Activities, nil
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
