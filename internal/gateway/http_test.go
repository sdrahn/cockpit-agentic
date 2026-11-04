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

// proxied is an MCP server behind a stand-in for OpenShell's proxy, which
// answers 500 to the next fail requests (all of them while fail < 0).
func proxied(t *testing.T) (url string, fail *atomic.Int32, token *atomic.Value) {
	t.Helper()
	old1, old2 := retryFirst, retryLast
	retryFirst, retryLast = 5*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { retryFirst, retryLast = old1, old2 })

	srv := mcp.NewServer(&mcp.Implementation{Name: "gw"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "probe"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "probe ok"}}}, nil, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	fail, token = &atomic.Int32{}, &atomic.Value{}
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token.Store(r.Header.Get("Authorization"))
		if r.Method == http.MethodPost {
			if n := fail.Load(); n < 0 || (n > 0 && fail.CompareAndSwap(n, n-1)) {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(p.Close)
	t.Setenv("GW_TOKEN", "placeholder-1")
	return p.URL, fail, token
}

// A short burst of 500s, as OpenShell's proxy sends around a token
// renewal, is retried: the session and its calls go on.
func TestProxy500BurstRetried(t *testing.T) {
	url, fail, token := proxied(t)
	ctx := context.Background()
	s, err := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil).Connect(ctx, HTTPTransport(url, "GW_TOKEN", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if token.Load() != "Bearer placeholder-1" {
		t.Errorf("token %v", token.Load())
	}
	fail.Store(3)
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "probe"})
	if err != nil || r.Content[0].(*mcp.TextContent).Text != "probe ok" {
		t.Fatalf("call through a 500 burst: %v %v", r, err)
	}
	if fail.Load() != 0 {
		t.Errorf("not every 500 was retried: %d left", fail.Load())
	}
}

// 500s that last past the retries, on a session: reported as a lost
// session (what ADK's toolset reconnects on), not a hang.
func TestProxy500OnSessionIsLostSession(t *testing.T) {
	url, fail, _ := proxied(t)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil)
	s, err := client.Connect(ctx, HTTPTransport(url, "GW_TOKEN", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(-1)
	// Without the mapping the client waits for an answer that never comes.
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = s.ListTools(tctx, nil)
	if !errors.Is(err, mcp.ErrSessionMissing) {
		t.Fatalf("lasting 500s on the session: %v, want ErrSessionMissing", err)
	}
	fail.Store(0)
	s2, err := client.Connect(ctx, HTTPTransport(url, "GW_TOKEN", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.ListTools(ctx, nil); err != nil {
		t.Fatalf("the new session: %v", err)
	}
}

// Lasting 500s to an initialize (no session) stay a 500 once the retries
// are spent.
func TestInitialize500Stays(t *testing.T) {
	old1, old2 := retryFirst, retryLast
	retryFirst, retryLast = time.Millisecond, 4*time.Millisecond
	defer func() { retryFirst, retryLast = old1, old2 }()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL, nil)
	res, err := (bearer{next: http.DefaultTransport}).RoundTrip(req)
	if err != nil || res.StatusCode != 500 {
		t.Fatalf("%v %v", res, err)
	}
	res.Body.Close()
	if n.Load() != 4 {
		t.Errorf("tried %d times, want 4 (1 ms doubling to 4 ms: 3 retries)", n.Load())
	}
}
