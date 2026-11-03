package governance

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sdrahn/cockpit-agentic/internal/config"
	"github.com/sdrahn/cockpit-agentic/internal/journal"
)

func TestArgsDigest(t *testing.T) {
	a := ArgsDigest(map[string]any{"b": 1, "a": []any{"x"}})
	b := ArgsDigest(map[string]any{"a": []any{"x"}, "b": 1})
	if a != b || len(a) != 64 {
		t.Errorf("digest not canonical: %s %s", a, b)
	}
	if a == ArgsDigest(map[string]any{"a": []any{"y"}, "b": 1}) {
		t.Error("different arguments, same digest")
	}
}

func TestIsDelegation(t *testing.T) {
	if !IsDelegation("agents__delegate") || IsDelegation("agents__other") || IsDelegation("x__delegate") {
		t.Error("IsDelegation")
	}
}

func newGov() (*Governor, *journal.Memory) {
	cfg, _ := config.Parse([]byte("version: 1\nname: pp\nmodel: {provider: vertex, name: m}\ngateway: {servers: [zypp]}\n"))
	log := &journal.Memory{}
	return New(cfg, log), log
}

// A notice that matches no call in flight is still journaled (the
// approval exists at the gateway), but published to nobody.
func TestNoticeWithoutCall(t *testing.T) {
	g, log := newGov()
	ch, cancel := g.Subscribe("s1")
	defer cancel()
	g.OnGatewayLog(context.Background(), &mcp.LoggingMessageParams{Logger: "mcp-gateway", Level: "notice",
		Data: "Waiting for approval of zypp/install on the mcp-gateway approvals page (id a-1)."})
	r := log.Events(journal.ApprovalPending)
	if len(r) != 1 || r[0].Fields["APPROVAL_ID"] != "a-1" || r[0].Fields["SESSION"] != "" {
		t.Errorf("records %+v", r)
	}
	select {
	case e := <-ch:
		t.Errorf("published %+v", e)
	default:
	}
	// Other logging messages are ignored.
	g.OnGatewayLog(context.Background(), &mcp.LoggingMessageParams{Logger: "zypp", Data: "hello"})
	if len(log.Records) != 1 {
		t.Errorf("records %d", len(log.Records))
	}
}

func TestPauseResumeCancel(t *testing.T) {
	g, log := newGov()
	g.Pause("alice")
	if !g.Paused() {
		t.Fatal("not paused")
	}
	done := make(chan error)
	go func() { done <- g.hold(context.Background()) }()
	select {
	case <-done:
		t.Fatal("hold returned while paused")
	default:
	}
	g.Resume("alice")
	if err := <-done; err != nil || g.Paused() {
		t.Errorf("after resume: %v %v", err, g.Paused())
	}
	ctx, c := context.WithCancel(context.Background())
	g.Pause("bob")
	c()
	if err := g.hold(ctx); err == nil {
		t.Error("hold ignores its context")
	}
	if g.Cancel("nope", "bob") {
		t.Error("cancelled an unknown activity")
	}
	if len(log.Events(journal.AgentPaused)) != 2 || len(log.Events(journal.AgentResumed)) != 1 {
		t.Error("pause/resume not journaled")
	}
}
