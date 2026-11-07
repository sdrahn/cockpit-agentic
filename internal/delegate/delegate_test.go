package delegate

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func serve(t *testing.T, h http.HandlerFunc) string {
	dir, _ := os.MkdirTemp("", "dlg")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "api.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func TestRun(t *testing.T) {
	var got map[string]any
	sock := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/activities" {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"id":"e-1","state":"finished","summary":"done"}`))
	})
	res, err := Run(context.Background(), sock, map[string]any{"prompt": "x", "wait": true, "trace": map[string]string{"parent_agent": "a"}})
	if err != nil || res["summary"] != "done" {
		t.Fatalf("%v %v", res, err)
	}
	if got["prompt"] != "x" || got["wait"] != true || got["trace"].(map[string]any)["parent_agent"] != "a" {
		t.Errorf("request %v", got)
	}
	if err := Post(context.Background(), sock, "/v1/activities"); err != nil {
		t.Errorf("Post on a 200: %v", err)
	}
}

func TestRunErrors(t *testing.T) {
	sock := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the agent is paused", http.StatusConflict)
	})
	if _, err := Run(context.Background(), sock, map[string]any{"prompt": "x"}); err == nil || err.Error() != "agent answered 409: the agent is paused" {
		t.Errorf("err %v", err)
	}
	if err := Post(context.Background(), sock, "/v1/pause"); err == nil {
		t.Error("409 not an error")
	}
	if _, err := Run(context.Background(), "/nonexistent/api.sock", map[string]any{}); err == nil {
		t.Error("unreachable agent not an error")
	}
}

// What the delegating agent gets: the other activity's full response
// (§4), not its 500-byte summary; the summary from an agent before the
// response was kept; the diagnosis report when there is one; and an
// error, with the reason, when the activity did not finish.
func TestAnswer(t *testing.T) {
	text := func(r *mcp.CallToolResult) map[string]any {
		var m map[string]any
		json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &m)
		return m
	}
	long := strings.Repeat("| /dev/vda2 | 98 % |\n", 200) // 4 KB, far beyond a summary
	r := answer("t1", map[string]any{"id": "e-1", "state": "finished", "summary": long[:500] + "…", "response": long,
		"report": map[string]any{"kind": "diagnosis", "summary": "/var is full."}})
	m := text(r)
	if r.IsError || m["answer"] != long || m["activity"] != "e-1" || m["agent"] != "t1" {
		t.Errorf("full response: %v %v", r.IsError, m)
	}
	if rep, _ := m["report"].(map[string]any); rep["summary"] != "/var is full." {
		t.Errorf("report %v", m["report"])
	}
	if m := text(answer("t1", map[string]any{"id": "e-2", "state": "finished", "summary": "done"})); m["answer"] != "done" || m["report"] != nil {
		t.Errorf("an older agent's summary: %v", m)
	}
	r = answer("t1", map[string]any{"id": "e-3", "state": "limit", "summary": "I stopped here.", "reason": "max_tool_calls (5)"})
	if m := text(r); !r.IsError || m["reason"] != "max_tool_calls (5)" || m["state"] != "limit" {
		t.Errorf("an unfinished activity: %v %v", r.IsError, m)
	}
}
