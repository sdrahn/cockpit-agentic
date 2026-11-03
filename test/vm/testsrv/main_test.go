package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTools(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 2 {
		t.Fatalf("tools %v %v", list, err)
	}
	for name, args := range map[string]map[string]any{"probe": {}, "change": {"value": "x"}} {
		want := map[string]string{"probe": "probe ok", "change": "changed x"}[name]
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError || res.Content[0].(*mcp.TextContent).Text != want {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
}
