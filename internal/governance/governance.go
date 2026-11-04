// Package governance is the ADK plugin every agent loads (CONTRACT.md
// §8): local limits, audit records (§9), and the gateway's approval
// notices (§7) passed on to the journal and to AG-UI streams (§11).
//
// It runs inside the agent's process. Its limits keep an honest agent
// within budget; they are not a security boundary (§1).
package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/sdrahn/custos/internal/config"
	"github.com/sdrahn/custos/internal/gateway"
	"github.com/sdrahn/custos/internal/journal"
)

// Event kinds published to subscribers (the AG-UI CUSTOM event names).
const (
	EvApprovalPending  = "agent.approval.pending"
	EvApprovalResolved = "agent.approval.resolved"
	EvLimitExceeded    = "agent.limit.exceeded"
)

// Event is published to the subscribers of a session.
type Event struct {
	Kind        string
	Session     string
	Invocation  string
	CallID      string
	Tool        string
	ApprovalID  string
	ApprovalURL string
	Outcome     string
	Limit       string
	Value       int
}

// Call is a tool call of an activity.
type Call struct {
	Session    string     `json:"session"`
	Invocation string     `json:"activity"`
	CallID     string     `json:"call_id"`
	Tool       string     `json:"tool"`
	ArgsSHA256 string     `json:"args_sha256"`
	Since      time.Time  `json:"since"`
	Ended      *time.Time `json:"ended,omitempty"`
	State      string     `json:"state"` // running | waiting_approval | ok | error
	ApprovalID string     `json:"approval_id,omitempty"`
	Approval   string     `json:"approval,omitempty"` // pending | approved | declined
	Reason     string     `json:"reason,omitempty"`
	// Rationale is the model's text that came with the call: the agent's
	// stated reason and expected impact. Self-asserted (CONTRACT.md §7).
	Rationale string `json:"rationale,omitempty"`
}

// Origin says what started an activity (CONTRACT.md §4).
type Origin struct {
	Kind     string `json:"kind"`               // chat | schedule | event | delegation | api
	By       string `json:"by"`                 // a user, a schedule's name, an event source, an agent principal
	Schedule string `json:"schedule,omitempty"` // schedules only
}

// Ref names an activity of an agent, possibly on another agent.
type Ref struct {
	Agent    string `json:"agent"`
	Activity string `json:"activity"`
}

// Activity is one invocation of the agent: one unit of work with its
// tool calls, as /v1/activities shows it (CONTRACT.md §4).
type Activity struct {
	ID       string     `json:"id"`
	Agent    string     `json:"agent"`
	Session  string     `json:"session"`
	User     string     `json:"user"`
	Origin   Origin     `json:"origin"`
	Parent   *Ref       `json:"parent,omitempty"`
	Root     Ref        `json:"root"`
	State    string     `json:"state"`
	Started  time.Time  `json:"started"`
	Ended    *time.Time `json:"ended,omitempty"`
	Summary  string     `json:"summary,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	Calls    []Call     `json:"calls"`
	LLMCalls int        `json:"llm_calls"`
}

// Activity states.
const (
	StateRunning   = "running"
	StateWaiting   = "waiting_approval"
	StatePaused    = "paused"
	StateFinished  = "finished"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateLimit     = "limit"
)

// Start describes an activity about to start in a session: who started
// it and how to cancel it. Without one, an activity is origin "api".
type Start struct {
	Origin Origin
	Parent *Ref
	Root   *Ref
	Cancel context.CancelFunc
	// Started, if set, is told the activity's id when it starts.
	Started func(id string)
}

// ClientTool marks tools defined by an AG-UI frontend (§11).
type ClientTool interface {
	tool.Tool
	ClientTool() bool
}

type invocation struct {
	session, user string
	start         time.Time
	llmCalls      int
	toolCalls     int
	stopped       bool
	act           *Activity
	calls         []*Call
	cancel        context.CancelFunc
	cancelled     bool
}

// History is how many ended activities are kept in memory; the journal
// keeps them all.
const History = 200

type Governor struct {
	cfg *config.Config
	log journal.Logger
	now func() time.Time
	// ApprovalURL makes the link to the gateway's approval page.
	ApprovalURL func(id string) string

	mu       sync.Mutex
	inv      map[string]*invocation
	inflight []*Call // in start order
	subs     map[string]map[chan Event]struct{}
	expect   map[string]Start
	ended    []*Activity // oldest first, at most History
	paused   bool
	resume   chan struct{}     // closed on Resume
	reasons  map[string]string // function call id -> rationale
}

// New returns a governor for the agent cfg, writing records to log.
func New(cfg *config.Config, log journal.Logger) *Governor {
	return &Governor{
		cfg: cfg, log: log, now: time.Now,
		ApprovalURL: func(id string) string {
			return "/cockpit/@localhost/mcp-gateway/index.html#/approvals/" + id
		},
		inv:     map[string]*invocation{},
		subs:    map[string]map[chan Event]struct{}{},
		expect:  map[string]Start{},
		reasons: map[string]string{},
		resume:  make(chan struct{}),
	}
}

// Plugin returns the ADK plugin.
func (g *Governor) Plugin() (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name:                "custos-governance",
		BeforeRunCallback:   g.beforeRun,
		AfterRunCallback:    g.afterRun,
		BeforeModelCallback: g.beforeModel,
		AfterModelCallback:  g.afterModel,
		BeforeToolCallback:  g.beforeTool,
		AfterToolCallback:   g.afterTool,
	})
}

// Subscribe returns the events of a session until cancel is called.
func (g *Governor) Subscribe(session string) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	g.mu.Lock()
	if g.subs[session] == nil {
		g.subs[session] = map[chan Event]struct{}{}
	}
	g.subs[session][ch] = struct{}{}
	g.mu.Unlock()
	return ch, func() {
		g.mu.Lock()
		delete(g.subs[session], ch)
		if len(g.subs[session]) == 0 {
			delete(g.subs, session)
		}
		g.mu.Unlock()
	}
}

// publish must be called with g.mu held.
func (g *Governor) publish(e Event) {
	for ch := range g.subs[e.Session] {
		select {
		case ch <- e:
		default: // a slow stream misses an event; the journal has it
		}
	}
}

// InFlight returns the tool calls in flight.
func (g *Governor) InFlight() []Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Call, len(g.inflight))
	for i, c := range g.inflight {
		out[i] = *c
	}
	return out
}

// Expect registers how the next activity in session was started.
func (g *Governor) Expect(session string, st Start) {
	g.mu.Lock()
	g.expect[session] = st
	g.mu.Unlock()
}

// Activities returns the running activities and the recently ended
// ones, newest first.
func (g *Governor) Activities() []Activity {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Activity
	for _, in := range g.inv {
		out = append(out, g.snapshot(in))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	for i := len(g.ended) - 1; i >= 0; i-- {
		out = append(out, copyActivity(g.ended[i]))
	}
	return out
}

// Activity returns one activity.
func (g *Governor) Activity(id string) (Activity, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if in := g.inv[id]; in != nil {
		return g.snapshot(in), true
	}
	for _, a := range g.ended {
		if a.ID == id {
			return copyActivity(a), true
		}
	}
	return Activity{}, false
}

// snapshot must be called with g.mu held.
func (g *Governor) snapshot(in *invocation) Activity {
	a := copyActivity(in.act)
	a.Calls = nil
	for _, c := range in.calls {
		a.Calls = append(a.Calls, *c)
	}
	a.State = StateRunning
	for _, c := range in.calls {
		if c.State == StateWaiting {
			a.State = StateWaiting
		}
	}
	if g.paused && a.State == StateRunning {
		a.State = StatePaused
	}
	a.LLMCalls = in.llmCalls
	return a
}

func copyActivity(a *Activity) Activity {
	c := *a
	c.Calls = append([]Call(nil), a.Calls...)
	return c
}

// Failed records that an activity's run ended in an error the runner
// returned (the plugin's callbacks do not see those, e.g. a gateway that
// cannot be reached): it is failed, with the error as its reason.
func (g *Governor) Failed(id string, err error) {
	if id == "" || err == nil {
		return
	}
	g.mu.Lock()
	var a *Activity
	for _, e := range g.ended {
		if e.ID == id {
			a = e
		}
	}
	if a == nil || a.State == StateCancelled || a.State == StateLimit {
		g.mu.Unlock()
		return
	}
	a.State, a.Reason = StateFailed, err.Error()
	f := journal.Fields{"SESSION": a.Session, "INVOCATION": id, "USER": a.User, "OUTCOME": "error", "REASON": a.Reason}
	g.mu.Unlock()
	g.log.Log(journal.InvocationEnd, "Invocation "+id+" failed: "+a.Reason, f)
}

// Cancel ends a running activity (CONTRACT.md §8). It reports whether
// the activity was running.
func (g *Governor) Cancel(id, by string) bool {
	g.mu.Lock()
	in := g.inv[id]
	if in == nil || in.cancel == nil {
		g.mu.Unlock()
		return false
	}
	in.cancelled = true
	in.act.Reason = "cancelled by " + by
	cancel := in.cancel
	f := journal.Fields{"SESSION": in.session, "INVOCATION": id, "USER": by, "OUTCOME": "cancelled"}
	g.mu.Unlock()
	g.log.Log(journal.ActivityCancel, "Activity "+id+" cancelled by "+by, f)
	cancel()
	return true
}

// Pause holds every activity before its next model or tool call, and
// refuses new ones, until Resume.
func (g *Governor) Pause(by string) {
	g.mu.Lock()
	if !g.paused {
		g.paused = true
		g.resume = make(chan struct{})
	}
	g.mu.Unlock()
	g.log.Log(journal.AgentPaused, "Agent paused by "+by, journal.Fields{"USER": by})
}

// Resume lets held activities go on.
func (g *Governor) Resume(by string) {
	g.mu.Lock()
	if g.paused {
		g.paused = false
		close(g.resume)
	}
	g.mu.Unlock()
	g.log.Log(journal.AgentResumed, "Agent resumed by "+by, journal.Fields{"USER": by})
}

// Paused reports whether the agent is paused.
func (g *Governor) Paused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// hold waits while the agent is paused.
func (g *Governor) hold(ctx context.Context) error {
	for {
		g.mu.Lock()
		paused, resume := g.paused, g.resume
		g.mu.Unlock()
		if !paused {
			return nil
		}
		select {
		case <-resume:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (g *Governor) beforeRun(ictx agent.InvocationContext) (*genai.Content, error) {
	s := ictx.Session()
	id := ictx.InvocationID()
	g.mu.Lock()
	st, ok := g.expect[s.ID()]
	delete(g.expect, s.ID())
	if !ok {
		st = Start{Origin: Origin{Kind: "api", By: s.UserID()}}
	}
	root := Ref{Agent: g.cfg.Name, Activity: id}
	if st.Root != nil {
		root = *st.Root
	}
	act := &Activity{ID: id, Agent: g.cfg.Name, Session: s.ID(), User: s.UserID(), Origin: st.Origin, Parent: st.Parent, Root: root, State: StateRunning, Started: g.now()}
	paused := g.paused
	if !paused {
		g.inv[id] = &invocation{session: s.ID(), user: s.UserID(), start: act.Started, act: act, cancel: st.Cancel}
	}
	g.mu.Unlock()
	f := journal.Fields{
		"SESSION": s.ID(), "INVOCATION": id, "USER": s.UserID(),
		"ORIGIN": st.Origin.Kind, "ORIGIN_BY": st.Origin.By, "SCHEDULE": st.Origin.Schedule,
		"ROOT_AGENT": root.Agent, "ROOT_ACTIVITY": root.Activity,
	}
	if st.Parent != nil {
		f["PARENT_AGENT"], f["PARENT_ACTIVITY"] = st.Parent.Agent, st.Parent.Activity
	}
	if paused {
		if st.Started != nil {
			st.Started("")
		}
		f["OUTCOME"], f["REASON"] = "denied", "agent paused"
		g.log.Log(journal.InvocationStart, "Invocation "+id+" refused: the agent is paused", f)
		return genai.NewContentFromText("This agent is paused by an administrator; nothing was done.", genai.RoleModel), nil
	}
	g.log.Log(journal.InvocationStart, fmt.Sprintf("Invocation %s started (%s by %s)", id, st.Origin.Kind, st.Origin.By), f)
	if st.Started != nil {
		st.Started(id)
	}
	return nil, nil
}

func (g *Governor) afterRun(ictx agent.InvocationContext) {
	g.mu.Lock()
	in := g.inv[ictx.InvocationID()]
	delete(g.inv, ictx.InvocationID())
	if in == nil {
		g.mu.Unlock()
		return
	}
	a := g.snapshot(in)
	now := g.now()
	a.Ended = &now
	switch {
	case in.cancelled:
		a.State = StateCancelled
	case in.stopped:
		a.State = StateLimit
	case ictx.Err() != nil:
		a.State = StateFailed
		if a.Reason == "" {
			a.Reason = ictx.Err().Error()
		}
	default:
		a.State = StateFinished
	}
	g.ended = append(g.ended, &a)
	if len(g.ended) > History {
		g.ended = g.ended[len(g.ended)-History:]
	}
	g.mu.Unlock()
	outcome := map[string]string{StateFinished: "ok", StateCancelled: "cancelled", StateLimit: "limit", StateFailed: "error"}[a.State]
	g.log.Log(journal.InvocationEnd, fmt.Sprintf("Invocation %s %s: %d model calls, %d tool calls", a.ID, a.State, in.llmCalls, in.toolCalls), journal.Fields{
		"SESSION": in.session, "INVOCATION": a.ID, "USER": in.user, "OUTCOME": outcome, "REASON": a.Reason,
		"DURATION_MS": ms(now.Sub(in.start)), "LLM_CALLS": strconv.Itoa(in.llmCalls), "TOOL_CALLS": strconv.Itoa(in.toolCalls),
		"ROOT_AGENT": a.Root.Agent, "ROOT_ACTIVITY": a.Root.Activity,
	})
}

// limit records a limit reached; g.mu must be held.
func (g *Governor) limit(in *invocation, invID, name string, value int) {
	in.stopped = true
	g.publish(Event{Kind: EvLimitExceeded, Session: in.session, Invocation: invID, Limit: name, Value: value})
	g.log.Log(journal.LimitExceeded, fmt.Sprintf("Limit %s (%d) reached", name, value), journal.Fields{
		"SESSION": in.session, "INVOCATION": invID, "USER": in.user, "OUTCOME": "limit", "REASON": name + "=" + strconv.Itoa(value),
	})
}

func (g *Governor) beforeModel(cctx agent.CallbackContext, _ *model.LLMRequest) (*model.LLMResponse, error) {
	if err := g.hold(cctx); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	in := g.inv[cctx.InvocationID()]
	if in == nil {
		return nil, nil
	}
	in.llmCalls++
	var name string
	var value int
	switch lim := g.cfg.Limits; {
	case in.llmCalls > lim.MaxLLMCalls:
		name, value = "max_llm_calls", lim.MaxLLMCalls
	case g.now().Sub(in.start) > lim.MaxInvocation():
		name, value = "max_invocation_seconds", lim.MaxInvocationSeconds
	default:
		return nil, nil
	}
	g.limit(in, cctx.InvocationID(), name, value)
	return &model.LLMResponse{
		Content:      genai.NewContentFromText(fmt.Sprintf("I stopped here: this agent's limit %s (%d) was reached.", name, value), genai.RoleModel),
		TurnComplete: true,
	}, nil
}

func (g *Governor) afterModel(cctx agent.CallbackContext, resp *model.LLMResponse, err error) (*model.LLMResponse, error) {
	if resp != nil && resp.Partial {
		return nil, nil
	}
	f := journal.Fields{"SESSION": cctx.SessionID(), "INVOCATION": cctx.InvocationID(), "USER": cctx.UserID(), "OUTCOME": "ok", "MODEL": g.cfg.Model.Name}
	if err != nil {
		f["OUTCOME"], f["REASON"] = "error", err.Error()
	}
	if resp != nil && resp.UsageMetadata != nil {
		f["TOKENS_IN"] = strconv.Itoa(int(resp.UsageMetadata.PromptTokenCount))
		f["TOKENS_OUT"] = strconv.Itoa(int(resp.UsageMetadata.CandidatesTokenCount))
	}
	if resp != nil && resp.Content != nil {
		var text strings.Builder
		for _, p := range resp.Content.Parts {
			if p.Text != "" && !p.Thought {
				text.WriteString(p.Text)
			}
		}
		if t := strings.TrimSpace(text.String()); t != "" {
			if len(t) > 500 {
				t = t[:500] + "…"
			}
			g.mu.Lock()
			if in := g.inv[cctx.InvocationID()]; in != nil {
				in.act.Summary = t
			}
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil && p.FunctionCall.ID != "" {
					g.reasons[p.FunctionCall.ID] = t
				}
			}
			g.mu.Unlock()
		}
	}
	g.log.Log(journal.ModelCall, "Model call to "+g.cfg.Model.Name, f)
	return nil, nil
}

func (g *Governor) beforeTool(tctx agent.ToolContext, t tool.Tool, args map[string]any) (map[string]any, error) {
	f := g.toolFields(tctx, t.Name(), args)
	if ct, ok := t.(ClientTool); ok && ct.ClientTool() && !g.cfg.Interaction.ClientTools.Allowed(t.Name()) {
		f["OUTCOME"], f["REASON"] = "denied", "client tool not allowed"
		g.log.Log(journal.ToolDeniedLocal, "Client tool "+t.Name()+" is not allowed", f)
		return map[string]any{"error": "this agent does not allow the client tool " + t.Name()}, nil
	}
	if err := g.hold(tctx); err != nil {
		return nil, err
	}
	g.mu.Lock()
	in := g.inv[tctx.InvocationID()]
	if in != nil {
		in.toolCalls++
		if in.toolCalls > g.cfg.Limits.MaxToolCalls {
			g.limit(in, tctx.InvocationID(), "max_tool_calls", g.cfg.Limits.MaxToolCalls)
			g.mu.Unlock()
			return map[string]any{"error": "tool call limit reached; finish without further tool calls"}, nil
		}
		if IsDelegation(t.Name()) && args != nil {
			// The trace of §9; the callee checks the caller by SO_PEERCRED.
			args[TraceArg] = map[string]any{
				"root_agent": in.act.Root.Agent, "root_activity": in.act.Root.Activity,
				"parent_agent": g.cfg.Name, "parent_activity": in.act.ID,
			}
		}
	}
	c := &Call{Session: tctx.SessionID(), Invocation: tctx.InvocationID(), CallID: tctx.FunctionCallID(), Tool: t.Name(), ArgsSHA256: f["ARGS_SHA256"], Since: g.now(), State: StateRunning, Rationale: g.reasons[tctx.FunctionCallID()]}
	delete(g.reasons, tctx.FunctionCallID())
	f["RATIONALE"] = c.Rationale
	g.inflight = append(g.inflight, c)
	if in != nil {
		in.calls = append(in.calls, c)
	}
	g.mu.Unlock()
	g.log.Log(journal.ToolCall, "Tool call "+t.Name(), f)
	return nil, nil
}

func (g *Governor) afterTool(tctx agent.ToolContext, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
	f := g.toolFields(tctx, t.Name(), args)
	g.mu.Lock()
	var call *Call
	for i, c := range g.inflight {
		if c.CallID == tctx.FunctionCallID() && c.Invocation == tctx.InvocationID() {
			call = c
			g.inflight = append(g.inflight[:i:i], g.inflight[i+1:]...)
			break
		}
	}
	if call == nil {
		g.mu.Unlock()
		return nil, nil // stopped by beforeTool: already recorded
	}
	now := g.now()
	f["DURATION_MS"] = ms(now.Sub(call.Since))
	errText := ""
	if err != nil {
		errText = err.Error()
	} else if e, ok := result["error"].(string); ok {
		errText = e
	}
	call.Ended, call.State, call.Reason = &now, "ok", errText
	if errText != "" {
		call.State = "error"
	}
	if call.ApprovalID != "" {
		outcome := "approved"
		if errText != "" {
			outcome = "declined"
		}
		call.Approval = outcome
		g.publish(Event{Kind: EvApprovalResolved, Session: call.Session, Invocation: call.Invocation, CallID: call.CallID, Tool: call.Tool, ApprovalID: call.ApprovalID, Outcome: outcome})
		af := copyFields(f)
		af["APPROVAL_ID"], af["OUTCOME"], af["REASON"] = call.ApprovalID, outcome, errText
		g.mu.Unlock()
		g.log.Log(journal.ApprovalResolved, fmt.Sprintf("Approval %s for %s: %s", call.ApprovalID, call.Tool, outcome), af)
		f["APPROVAL_ID"] = call.ApprovalID
	} else {
		g.mu.Unlock()
	}
	f["OUTCOME"] = "ok"
	if errText != "" {
		f["OUTCOME"], f["REASON"] = "error", errText
	}
	g.log.Log(journal.ToolResult, "Tool result "+t.Name()+": "+f["OUTCOME"], f)
	return nil, nil
}

// OnGatewayLog handles the gateway's logging messages: an approval
// notice is assigned to the call it names (its progress token,
// gateway.CallToken); from a gateway that names only the tool, to the
// oldest call of its tool in flight without one (CONTRACT.md §7).
func (g *Governor) OnGatewayLog(_ context.Context, p *mcp.LoggingMessageParams) {
	n, ok := gateway.ParseNotice(p)
	if !ok {
		return
	}
	g.mu.Lock()
	var call *Call
	for _, c := range g.inflight {
		if n.ProgressToken != "" {
			// Named: that call or, if it is not in flight, none.
			if gateway.CallToken(c.Invocation, c.CallID) == n.ProgressToken && c.Tool == n.ToolName() {
				call = c
				break
			}
		} else if c.Tool == n.ToolName() && c.ApprovalID == "" {
			call = c
			break
		}
	}
	f := journal.Fields{"TOOL": n.ToolName(), "SERVER": n.Server, "APPROVAL_ID": n.ApprovalID}
	if call != nil {
		call.ApprovalID, call.Approval, call.State = n.ApprovalID, "pending", StateWaiting
		f["SESSION"], f["INVOCATION"], f["CALL_ID"] = call.Session, call.Invocation, call.CallID
		if in := g.inv[call.Invocation]; in != nil {
			f["USER"] = in.user
		}
		g.publish(Event{Kind: EvApprovalPending, Session: call.Session, Invocation: call.Invocation, CallID: call.CallID, Tool: call.Tool, ApprovalID: n.ApprovalID, ApprovalURL: g.ApprovalURL(n.ApprovalID)})
	}
	g.mu.Unlock()
	g.log.Log(journal.ApprovalPending, fmt.Sprintf("Waiting for approval %s of %s", n.ApprovalID, n.ToolName()), f)
}

func (g *Governor) toolFields(tctx agent.ToolContext, name string, args map[string]any) journal.Fields {
	srv, _, _ := strings.Cut(name, "__")
	f := journal.Fields{
		"SESSION": tctx.SessionID(), "INVOCATION": tctx.InvocationID(), "USER": tctx.UserID(),
		"TOOL": name, "CALL_ID": tctx.FunctionCallID(), "ARGS_SHA256": ArgsDigest(args),
	}
	if strings.Contains(name, "__") {
		f["SERVER"] = srv
	}
	return f
}

// ArgsDigest is the SHA-256 of the arguments' canonical JSON
// (encoding/json sorts map keys).
func ArgsDigest(args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

func copyFields(f journal.Fields) journal.Fields {
	c := journal.Fields{}
	for k, v := range f {
		c[k] = v
	}
	return c
}

// TraceArg is the argument a delegation call carries its trace in.
const TraceArg = "_trace"

// IsDelegation reports whether a gateway tool delegates to another agent
// (the agents server shipped with Custos, CONTRACT.md §9).
func IsDelegation(tool string) bool { return tool == "agents__delegate" }
