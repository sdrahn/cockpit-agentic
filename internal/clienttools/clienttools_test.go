package clienttools

import (
	"testing"

	"google.golang.org/adk/model"
)

func TestProcessRequest(t *testing.T) {
	a := &clientTool{Def{Name: "a", Parameters: map[string]any{"type": "object"}}}
	b := &clientTool{Def{Name: "b"}}
	req := &model.LLMRequest{}
	if err := a.ProcessRequest(nil, req); err != nil {
		t.Fatal(err)
	}
	if err := b.ProcessRequest(nil, req); err != nil {
		t.Fatal(err)
	}
	if n := len(req.Config.Tools); n != 1 || len(req.Config.Tools[0].FunctionDeclarations) != 2 {
		t.Errorf("declarations not packed into one tool: %+v", req.Config.Tools)
	}
	if err := a.ProcessRequest(nil, req); err == nil {
		t.Error("duplicate accepted")
	}
	if !a.IsLongRunning() || !a.ClientTool() {
		t.Error("client tools are long-running client tools")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	clear := r.Set("s1", []Def{{Name: "x"}})
	r.mu.Lock()
	n := len(r.defs["s1"])
	r.mu.Unlock()
	if n != 1 {
		t.Fatal("not set")
	}
	clear()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.defs["s1"]; ok {
		t.Error("not cleared")
	}
}
