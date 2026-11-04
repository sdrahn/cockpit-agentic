// Package gateway connects an agent to mcp-gateway's aggregated MCP
// endpoint on its unix socket (CONTRACT.md §6) and recognises the
// gateway's approval notices (§7).
package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Transport dials the gateway's socket afresh for every connection, so
// ADK's MCP toolset can reconnect after a gateway restart.
type Transport struct {
	Socket string
}

func (t *Transport) Connect(ctx context.Context) (mcp.Connection, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", t.Socket)
	if err != nil {
		return nil, fmt.Errorf("mcp-gateway: %w", err)
	}
	return (&mcp.IOTransport{Reader: c, Writer: c}).Connect(ctx)
}

// HTTPTransport connects to mcp-gateway's Streamable HTTP endpoint. The
// bearer token is read from the environment variable tokenEnv for every
// request: under OpenShell it holds a placeholder the supervisor
// replaces on the way out (CONTRACT.md §3a).
func HTTPTransport(endpoint, tokenEnv string, base http.RoundTripper) mcp.Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: bearer{env: tokenEnv, next: base}},
	}
}

type bearer struct {
	env  string
	next http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.env != "" {
		if tok := os.Getenv(b.env); tok != "" {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	res, err := b.next.RoundTrip(r)
	if err == nil && res.StatusCode == http.StatusInternalServerError && r.Header.Get(sessionHeader) != "" {
		// mcp-gateway answers 500 only to an initialize it cannot serve,
		// never on a session: a 500 on one comes from between, OpenShell's
		// proxy, which forgets the sessions it inspected when the provider's
		// token is renewed. As "session not found" (404) the MCP client
		// gives up the session, and the toolset opens a new one and retries
		// the request once.
		io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		res = &http.Response{
			Status: "404 Not Found", StatusCode: http.StatusNotFound, Proto: res.Proto, ProtoMajor: res.ProtoMajor, ProtoMinor: res.ProtoMinor,
			Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("session lost on the way (500)")), Request: r,
		}
	}
	return res, err
}

// sessionHeader is MCP's Streamable HTTP session header.
const sessionHeader = "Mcp-Session-Id"

// Notice is an approval notice of the gateway's broker.
type Notice struct {
	Server, Tool, ApprovalID string
	// Where is the place named by the gateway: a URL or a description.
	Where string
}

// ToolName is the tool's name on the aggregated endpoint.
func (n Notice) ToolName() string { return n.Server + "__" + n.Tool }

// The text of mcp-gateway's broker (internal/broker, notifyOOB):
// "Waiting for approval of <server>/<tool> <where> (id <id>)."
var noticeRE = regexp.MustCompile(`^Waiting for approval of ([^/\s]+)/(\S+) (.*) \(id ([A-Za-z0-9._:-]+)\)\.$`)

// ParseNotice recognises an approval notice in a logging message.
func ParseNotice(p *mcp.LoggingMessageParams) (Notice, bool) {
	if p == nil || p.Logger != "mcp-gateway" {
		return Notice{}, false
	}
	s, ok := p.Data.(string)
	if !ok {
		return Notice{}, false
	}
	m := noticeRE.FindStringSubmatch(s)
	if m == nil {
		return Notice{}, false
	}
	where := m[3]
	if u, ok := cutPrefix(where, "at "); ok {
		where = u
	}
	return Notice{Server: m[1], Tool: m[2], Where: where, ApprovalID: m[4]}, true
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return s, false
}

// NewClient returns the agent's MCP client. It declares no elicitation
// capability (no ElicitationHandler), so the gateway never asks the
// agent to approve its own call (§6). Logging messages go to onLog.
func NewClient(agentName, version string, onLog func(context.Context, *mcp.LoggingMessageParams)) *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "custos/" + agentName, Version: version}, &mcp.ClientOptions{
		LoggingMessageHandler: func(ctx context.Context, r *mcp.LoggingMessageRequest) {
			if onLog != nil {
				onLog(ctx, r.Params)
			}
		},
	})
}
