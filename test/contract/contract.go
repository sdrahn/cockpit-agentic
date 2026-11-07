// Package contract checks an agent host against the control-plane
// contract (CONTRACT.md Part I: §3 discovery, §4 activities, §5 the
// control API, §8 pause, §10 the records), the same checks for every host:
// the reference launcher (internal/launcher's tests) and the second host
// (test/vm/secondhost, #112). It knows the contract, not either host.
package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

// Host is how the checks reach a host.
type Host struct {
	// Name is the agent's name.
	Name string
	// Do sends a request as caller ("uid:name", as SO_PEERCRED would
	// say) and returns the status and the body.
	Do func(method, path, caller string, body any) (int, []byte)
	// Records are the host's audit records of an event (§10), their
	// fields without the AGENT_ prefix; nil: not checked.
	Records func(event string) []map[string]string
}

// The callers: a supervisor (a member of agent-clients), root, an agent.
const (
	Supervisor = "1000:alice"
	Root       = "0:root"
	Agent      = "991:agent-other"
)

type activity struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Session string `json:"session"`
	User    string `json:"user"`
	Origin  struct {
		Kind, By, Schedule string
	} `json:"origin"`
	Root struct {
		Agent, Activity string
	} `json:"root"`
	State    string          `json:"state"`
	Started  string          `json:"started"`
	Ended    *string         `json:"ended"`
	Calls    json.RawMessage `json:"calls"`
	Response *string         `json:"response"`
}

// Check runs the checks.
func Check(t *testing.T, h Host) {
	t.Helper()
	get := func(path, caller string, v any) int {
		t.Helper()
		code, b := h.Do("GET", path, caller, nil)
		if v != nil && code == 200 {
			if err := json.Unmarshal(b, v); err != nil {
				t.Errorf("GET %s: %v: %s", path, err, b)
			}
		}
		return code
	}
	start := func(caller string, body map[string]any) (int, activity, []byte) {
		t.Helper()
		code, b := h.Do("POST", "/v1/activities", caller, body)
		var a activity
		json.Unmarshal(b, &a)
		return code, a, b
	}

	// §3, §5: the status.
	var st struct {
		Contract       int               `json:"contract"`
		Implementation string            `json:"implementation"`
		Name           string            `json:"name"`
		Lifecycle      map[string]string `json:"lifecycle"`
		Paused         *bool             `json:"paused"`
		Running        *int              `json:"running"`
		InFlight       []any             `json:"in_flight"`
		Interaction    map[string]any    `json:"interaction"`
		Schedules      []any             `json:"schedules"`
	}
	if code := get("/v1/status", Supervisor, &st); code != 200 {
		t.Fatalf("GET /v1/status: %d", code)
	}
	if st.Contract != 1 || st.Implementation == "" || st.Name != h.Name || st.Paused == nil || st.Running == nil ||
		st.InFlight == nil || st.Interaction == nil || st.Schedules == nil {
		t.Errorf("status %+v", st)
	}
	switch k := st.Lifecycle["kind"]; k {
	case "systemd":
		if st.Lifecycle["service"] == "" {
			t.Errorf("lifecycle systemd without its service: %v", st.Lifecycle)
		}
	case "openshell":
	default:
		t.Errorf("lifecycle kind %q (§3a: systemd or openshell)", k)
	}

	// §4, §5: a supervisor's activity, origin api, waited for.
	code, a, b := start(Supervisor, map[string]any{"prompt": "hello", "wait": true})
	if code != 200 || a.ID == "" || a.Agent != h.Name || a.State != "finished" || a.Origin.Kind != "api" || a.Origin.By != "alice" ||
		a.User != "alice" || a.Root.Agent != h.Name || a.Root.Activity != a.ID || a.Ended == nil || a.Started == "" || string(a.Calls) == "null" || a.Session == "" {
		t.Errorf("a supervisor's activity: %d %s", code, b)
	}
	// Not waited for: 202 and its id.
	code, b = h.Do("POST", "/v1/activities", Supervisor, map[string]any{"prompt": "again"})
	var acc struct{ ID, Session string }
	json.Unmarshal(b, &acc)
	if code != 202 || acc.ID == "" || acc.Session == "" {
		t.Errorf("not waited for: %d %s", code, b)
	}
	var one activity
	if code := get("/v1/activities/"+a.ID, Supervisor, &one); code != 200 || one.ID != a.ID {
		t.Errorf("GET /v1/activities/{id}: %d %+v", code, one)
	}
	if code := get("/v1/activities/e-no-such", Supervisor, nil); code != 404 {
		t.Errorf("an unknown activity: %d", code)
	}
	var list struct {
		Activities []activity `json:"activities"`
	}
	if code := get("/v1/activities", Supervisor, &list); code != 200 {
		t.Fatalf("GET /v1/activities: %d", code)
	}
	found, endedSeen := false, false
	for _, x := range list.Activities {
		if x.ID == a.ID {
			found = true
		}
		if x.Response != nil {
			t.Errorf("the list has a response: %s", x.ID)
		}
		// Running first, then ended.
		if x.Ended != nil {
			endedSeen = true
		} else if endedSeen {
			t.Errorf("a running activity after an ended one: %s", x.ID)
		}
	}
	if !found {
		t.Errorf("the activity is not listed")
	}

	// §4: only root sets the origin and the user; a schedule's runs as
	// system:schedule.
	_, a, b = start(Supervisor, map[string]any{"prompt": "x", "wait": true, "origin": map[string]string{"kind": "schedule", "by": "forged"}})
	if a.Origin.Kind != "api" || a.Origin.By != "alice" {
		t.Errorf("a supervisor set its origin: %s", b)
	}
	_, a, b = start(Root, map[string]any{"prompt": "x", "wait": true,
		"origin": map[string]string{"kind": "schedule", "by": "agent-schedule@" + h.Name + "_check.timer", "schedule": "check"}})
	if a.Origin.Kind != "schedule" || a.Origin.Schedule != "check" || a.User != "system:schedule" {
		t.Errorf("root's scheduled activity: %s", b)
	}
	if code, _, b := start(Root, map[string]any{"prompt": "x", "origin": map[string]string{"kind": "made-up", "by": "x"}}); code != 400 {
		t.Errorf("an unknown origin kind: %d %s", code, b)
	}
	if code, _, b := start(Supervisor, map[string]any{"nothing": true}); code != 400 {
		t.Errorf("no prompt: %d %s", code, b)
	}
	if code, _ := h.Do("POST", "/v1/activities/"+a.ID+"/cancel", Root, nil); code != 409 {
		t.Errorf("cancelling an ended activity: %d", code)
	}

	// §5: agents only start and read activities.
	if code := get("/v1/activities", Agent, nil); code != 403 {
		t.Errorf("an agent listed activities: %d", code)
	}
	if code, _ := h.Do("POST", "/v1/pause", Agent, nil); code != 403 {
		t.Errorf("an agent paused: %d", code)
	}

	// §8: pause and resume are root's; paused, a start is refused (409).
	if code, _ := h.Do("POST", "/v1/pause", Supervisor, nil); code != 403 {
		t.Errorf("a supervisor paused: %d", code)
	}
	if code, _ := h.Do("POST", "/v1/pause", Root, nil); code/100 != 2 {
		t.Fatalf("root's pause: %d", code)
	}
	if get("/v1/status", Supervisor, &st); st.Paused == nil || !*st.Paused {
		t.Errorf("paused, the status says %+v", st.Paused)
	}
	if code, _, b := start(Supervisor, map[string]any{"prompt": "x"}); code != 409 {
		t.Errorf("a start while paused: %d %s", code, b)
	}
	if code, _ := h.Do("POST", "/v1/resume", Root, nil); code/100 != 2 {
		t.Fatalf("root's resume: %d", code)
	}
	if code, _, b := start(Supervisor, map[string]any{"prompt": "x", "wait": true}); code != 200 {
		t.Errorf("a start after resume: %d %s", code, b)
	}

	// §10: the records.
	if h.Records == nil {
		return
	}
	ok := false
	for _, r := range h.Records("invocation.start") {
		if r["ORIGIN"] == "schedule" && r["SCHEDULE"] == "check" && r["USER"] == "system:schedule" && r["INVOCATION"] != "" &&
			strings.HasPrefix(r["ORIGIN_BY"], "agent-schedule@") {
			ok = true
		}
	}
	if !ok {
		t.Errorf("no invocation.start record of the scheduled activity: %v", h.Records("invocation.start"))
	}
	if len(h.Records("invocation.end")) == 0 {
		t.Error("no invocation.end records")
	}
}
