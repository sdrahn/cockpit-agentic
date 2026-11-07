// Package host is a second agent host for the control plane (CONTRACT.md
// Part I; #112, O8): a mock that serves the control API (§5) and writes
// the audit records (§10), with no model, gateway or ADK behind it. It
// imports nothing of Custos's own code, only the standard library, so
// that what the control plane does with it rests on the contract alone.
//
// Its activities are scripted by the prompt:
//
//	WAIT …   runs until it is cancelled
//	FAIL …   ends failed
//	anything else ends finished at once, its summary "echo: <prompt>"
//
// It serves no AG-UI (§11: interaction.agui false) and no playbooks
// (§14c is a draft: GET /v1/playbooks answers 404).
package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Implementation is what /v1/status names.
const Implementation = "custos-test/second-host"

// Caller is who called, as the socket's peer credentials say (§5).
type Caller struct {
	UID  int
	Name string
}

func (c Caller) root() bool  { return c.UID == 0 }
func (c Caller) agent() bool { return strings.HasPrefix(c.Name, "agent-") }

type callerKey struct{}

// WithCaller puts the caller into a request's context.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerOf(r *http.Request) (Caller, bool) {
	c, ok := r.Context().Value(callerKey{}).(Caller)
	return c, ok
}

// Origin, Ref and Activity are §4's.
type Origin struct {
	Kind     string `json:"kind"`
	By       string `json:"by"`
	Schedule string `json:"schedule,omitempty"`
	Event    string `json:"event,omitempty"`
}

type Ref struct {
	Agent    string `json:"agent"`
	Activity string `json:"activity"`
}

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
	LLMCalls int        `json:"llm_calls"`
	Calls    []any      `json:"calls"`
	Response string     `json:"response,omitempty"`
}

// Logger writes an audit record (§10): event, message, the fields
// without the AGENT_ prefix.
type Logger func(event, message string, fields map[string]string)

// Host is one agent.
type Host struct {
	Name    string
	Service string // its systemd unit, for lifecycle (§3a)
	Log     Logger
	Now     func() time.Time

	mu      sync.Mutex
	acts    []*Activity // oldest first
	paused  bool
	started time.Time
}

// New makes a host for the agent name, run by the systemd unit service.
func New(name, service string, log Logger) *Host {
	h := &Host{Name: name, Service: service, Log: log, Now: time.Now}
	h.started = h.Now()
	return h
}

// Handler is the control API (§5).
func (h *Host) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", h.status)
	mux.HandleFunc("GET /v1/activities", h.list)
	mux.HandleFunc("GET /v1/activities/{id}", h.one)
	mux.HandleFunc("POST /v1/activities", h.start)
	mux.HandleFunc("POST /v1/activities/{id}/cancel", h.cancel)
	mux.HandleFunc("POST /v1/pause", h.pause(true))
	mux.HandleFunc("POST /v1/resume", h.pause(false))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := callerOf(r)
		if !ok {
			http.Error(w, "caller unknown", http.StatusForbidden)
			return
		}
		// Agents (delegation, §9) may only start and read activities.
		if c.agent() && !(r.Method == http.MethodPost && r.URL.Path == "/v1/activities") &&
			!(r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/activities/")) {
			http.Error(w, "agents may only delegate", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (h *Host) status(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	running := 0
	for _, a := range h.acts {
		if a.Ended == nil {
			running++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"contract": 1, "implementation": Implementation, "name": h.Name, "version": "0",
		"lifecycle": map[string]string{"kind": "systemd", "service": h.Service},
		"model":     "none", "paused": h.paused,
		"gateway": map[string]any{"socket": "", "reachable": false},
		"running": running, "in_flight": []any{}, "started": h.started,
		"limits":      map[string]int{},
		"interaction": map[string]any{"agui": false, "client_tools": []string{}},
		"schedules":   []any{}, "events": []any{},
	})
}

// sorted: running first, then ended, newest first (§5).
func (h *Host) sorted() []Activity {
	out := make([]Activity, 0, len(h.acts))
	for _, a := range h.acts {
		c := *a
		c.Response = ""
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].Ended == nil, out[j].Ended == nil
		if ri != rj {
			return ri
		}
		return out[i].Started.After(out[j].Started)
	})
	return out
}

func (h *Host) list(w http.ResponseWriter, r *http.Request) {
	if c, _ := callerOf(r); c.agent() {
		http.Error(w, "agents may only read the activities they started", http.StatusForbidden)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"activities": h.sorted()})
}

func (h *Host) find(id string) *Activity {
	for _, a := range h.acts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (h *Host) one(w http.ResponseWriter, r *http.Request) {
	c, _ := callerOf(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.find(r.PathValue("id"))
	// An agent sees only what it delegated (§5), as if nothing else were there.
	if a == nil || (c.agent() && (a.Parent == nil || "agent-"+a.Parent.Agent != c.Name)) {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

type startRequest struct {
	Prompt  string  `json:"prompt"`
	Session string  `json:"session"`
	User    string  `json:"user"`
	Origin  *Origin `json:"origin"`
	Trace   *struct {
		RootAgent      string `json:"root_agent"`
		RootActivity   string `json:"root_activity"`
		ParentAgent    string `json:"parent_agent"`
		ParentActivity string `json:"parent_activity"`
	} `json:"trace"`
	Wait bool `json:"wait"`
}

func newID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (h *Host) start(w http.ResponseWriter, r *http.Request) {
	c, _ := callerOf(r)
	var req startRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, `want {"prompt": …}`, http.StatusBadRequest)
		return
	}
	a := &Activity{ID: newID("e-"), Agent: h.Name, Session: req.Session, User: c.Name, State: "running", Started: h.Now(), Calls: []any{}}
	if a.Session == "" {
		a.Session = newID("s-")
	}
	switch {
	case c.agent():
		a.Origin = Origin{Kind: "delegation", By: c.Name}
		// The trace is believed only where it names the caller (§9).
		if t := req.Trace; t != nil && "agent-"+t.ParentAgent == c.Name {
			a.Parent = &Ref{Agent: t.ParentAgent, Activity: t.ParentActivity}
			a.Root = *a.Parent
			if t.RootAgent != "" {
				a.Root = Ref{Agent: t.RootAgent, Activity: t.RootActivity}
			}
		}
	case c.root() && req.Origin != nil:
		switch req.Origin.Kind {
		case "schedule", "event", "api", "chat":
		default:
			http.Error(w, "origin.kind: want schedule, event, api or chat", http.StatusBadRequest)
			return
		}
		a.Origin = *req.Origin
		if req.User != "" {
			a.User = req.User
		} else if a.Origin.Kind == "schedule" || a.Origin.Kind == "event" {
			a.User = "system:" + a.Origin.Kind
		}
	default:
		a.Origin = Origin{Kind: "api", By: c.Name}
	}
	if a.Parent == nil {
		a.Root = Ref{Agent: h.Name, Activity: a.ID}
	}

	h.mu.Lock()
	if h.paused {
		h.mu.Unlock()
		h.Log("invocation.start", "Invocation refused: the agent is paused", map[string]string{
			"INVOCATION": a.ID, "SESSION": a.Session, "USER": a.User, "ORIGIN": a.Origin.Kind, "ORIGIN_BY": a.Origin.By,
			"OUTCOME": "denied", "REASON": "agent paused"})
		http.Error(w, "the agent is paused", http.StatusConflict)
		return
	}
	h.acts = append(h.acts, a)
	if len(h.acts) > 200+16 { // keeps at least the last 200 ended (§4)
		h.acts = h.acts[len(h.acts)-216:]
	}
	h.mu.Unlock()
	f := map[string]string{"INVOCATION": a.ID, "SESSION": a.Session, "USER": a.User, "ORIGIN": a.Origin.Kind,
		"ORIGIN_BY": a.Origin.By, "SCHEDULE": a.Origin.Schedule, "ORIGIN_EVENT": a.Origin.Event,
		"ROOT_AGENT": a.Root.Agent, "ROOT_ACTIVITY": a.Root.Activity}
	if a.Parent != nil {
		f["PARENT_AGENT"], f["PARENT_ACTIVITY"] = a.Parent.Agent, a.Parent.Activity
	}
	h.Log("invocation.start", "Invocation "+a.ID+" started ("+a.Origin.Kind+" by "+a.Origin.By+")", f)

	switch {
	case strings.HasPrefix(req.Prompt, "WAIT"):
		// Runs until cancelled.
	case strings.HasPrefix(req.Prompt, "FAIL"):
		h.end(a, "failed", "", "scripted failure")
	default:
		h.end(a, "finished", "echo: "+req.Prompt, "")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.Wait && a.Ended != nil {
		writeJSON(w, http.StatusOK, a)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": a.ID, "session": a.Session})
}

// end ends an activity and records it (invocation.end).
func (h *Host) end(a *Activity, state, text, reason string) {
	h.mu.Lock()
	now := h.Now()
	a.State, a.Ended, a.Reason = state, &now, reason
	if text != "" {
		a.Summary, a.Response = text, text
	}
	h.mu.Unlock()
	outcome := map[string]string{"finished": "ok", "failed": "error", "cancelled": "cancelled"}[state]
	h.Log("invocation.end", "Invocation "+a.ID+" "+state, map[string]string{
		"INVOCATION": a.ID, "SESSION": a.Session, "USER": a.User, "OUTCOME": outcome, "REASON": reason,
		"ROOT_AGENT": a.Root.Agent, "ROOT_ACTIVITY": a.Root.Activity,
		"DURATION_MS": itoa(int(now.Sub(a.Started).Milliseconds())), "LLM_CALLS": "0", "TOOL_CALLS": "0"})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (h *Host) cancel(w http.ResponseWriter, r *http.Request) {
	c, _ := callerOf(r)
	h.mu.Lock()
	a := h.find(r.PathValue("id"))
	switch {
	case a == nil:
		h.mu.Unlock()
		http.NotFound(w, r)
		return
	case !c.root() && a.User != c.Name && a.Origin.By != c.Name:
		h.mu.Unlock()
		http.Error(w, "only root, the activity's user or originator may cancel it", http.StatusForbidden)
		return
	case a.Ended != nil:
		h.mu.Unlock()
		http.Error(w, "the activity is not running", http.StatusConflict)
		return
	}
	h.mu.Unlock()
	h.Log("activity.cancel", "Activity "+a.ID+" cancelled by "+c.Name, map[string]string{"INVOCATION": a.ID, "BY": c.Name})
	h.end(a, "cancelled", "", "cancelled by "+c.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Host) pause(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerOf(r)
		if !c.root() {
			http.Error(w, "only root pauses and resumes", http.StatusForbidden)
			return
		}
		h.mu.Lock()
		h.paused = on
		h.mu.Unlock()
		ev, msg := "agent.resumed", "Agent resumed by "+c.Name
		if on {
			ev, msg = "agent.paused", "Agent paused by "+c.Name
		}
		h.Log(ev, msg, map[string]string{"BY": c.Name})
		w.WriteHeader(http.StatusNoContent)
	}
}
