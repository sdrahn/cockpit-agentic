package delegate

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
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
