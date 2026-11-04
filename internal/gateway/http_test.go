package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// OpenShell's proxy answers 500 on a session after the provider's token
// is renewed. The client must see that as a lost session (what ADK's
// toolset reconnects on), and a new session works.
func TestSessionLostOnTheWay(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "gw"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "probe"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "probe ok"}}}, nil, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	var fail atomic.Bool
	var sawToken atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawToken.Store(r.Header.Get("Authorization"))
		if fail.Load() && r.Header.Get("Mcp-Session-Id") != "" && r.Method == http.MethodPost {
			fail.Store(false)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	t.Setenv("GW_TOKEN", "placeholder-1")

	client := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil)
	ctx := context.Background()
	s, err := client.Connect(ctx, HTTPTransport(proxy.URL, "GW_TOKEN", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if sawToken.Load() != "Bearer placeholder-1" {
		t.Errorf("token %v", sawToken.Load())
	}

	fail.Store(true)
	// Without the mapping the client waits for an answer that never comes.
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = s.ListTools(tctx, nil)
	if !errors.Is(err, mcp.ErrSessionMissing) {
		t.Fatalf("a 500 on the session: %v, want ErrSessionMissing (the toolset reconnects on it)", err)
	}
	s2, err := client.Connect(ctx, HTTPTransport(proxy.URL, "GW_TOKEN", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	r, err := s2.CallTool(ctx, &mcp.CallToolParams{Name: "probe"})
	if err != nil || r.Content[0].(*mcp.TextContent).Text != "probe ok" {
		t.Fatalf("the new session: %v %v", r, err)
	}
}

// A 500 to an initialize (no session) stays a 500: that one is mcp-gateway's.
func TestInitialize500Stays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	rt := bearer{next: http.DefaultTransport}
	req, _ := http.NewRequest("POST", srv.URL, nil)
	res, err := rt.RoundTrip(req)
	if err != nil || res.StatusCode != 500 {
		t.Fatalf("%v %v", res, err)
	}
	res.Body.Close()
}
