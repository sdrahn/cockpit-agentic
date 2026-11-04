package launcher

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sdrahn/custos/internal/governance"
	"github.com/sdrahn/custos/internal/testutil"
)

// Cancelling an activity whose tool call is in flight must not break the
// agent's gateway connection for the activities after it (all share one
// MCP session).
func TestCancelThenNextActivity(t *testing.T) {
	e := newEnv(t,
		testutil.Call("c1", "zypp__install_patches", map[string]any{}),
		testutil.Call("c2", "zypp__list_updates", map[string]any{}),
		testutil.Text("listed"),
	)
	e.gw.Block["zypp__install_patches"] = true
	_, b := e.do("POST", "/v1/activities", "0:root", map[string]any{"prompt": "install"})
	var st struct{ ID string }
	json.Unmarshal(b, &st)
	waitFor(t, func() bool { return len(e.l.Governor.InFlight()) == 1 })
	if res, _ := e.do("POST", "/v1/activities/"+st.ID+"/cancel", "0:root", nil); res.StatusCode != 204 {
		t.Fatalf("cancel: %d", res.StatusCode)
	}
	// At once, as the functional tests do.
	done := make(chan []byte, 1)
	go func() {
		_, b := e.do("POST", "/v1/activities", "0:root", map[string]any{"prompt": "list", "wait": true})
		done <- b
	}()
	select {
	case b = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the next activity did not end")
	}
	var a governance.Activity
	json.Unmarshal(b, &a)
	if a.State != governance.StateFinished || a.Summary != "listed" || len(a.Calls) != 1 || a.Calls[0].State != "ok" {
		t.Errorf("next activity after a cancel: %s", b)
	}
	if !strings.Contains(strings.Join(e.gw.Ran(), ","), "zypp__list_updates") {
		t.Errorf("the gateway ran %v", e.gw.Ran())
	}
}

// As the functional tests run it: a cancel, then an AG-UI run.
func TestCancelThenAGUI(t *testing.T) {
	e := newEnv(t,
		testutil.Call("c1", "zypp__install_patches", map[string]any{}),
		testutil.Call("c2", "zypp__list_updates", map[string]any{}),
		testutil.Text("listed"),
	)
	e.gw.Block["zypp__install_patches"] = true
	_, b := e.do("POST", "/v1/activities", "0:root", map[string]any{"prompt": "install"})
	var st struct{ ID string }
	json.Unmarshal(b, &st)
	waitFor(t, func() bool { return len(e.l.Governor.InFlight()) == 1 })
	e.do("POST", "/v1/activities/"+st.ID+"/cancel", "0:root", nil)
	ch := e.agui(map[string]any{"threadId": "custos-test-1", "messages": []any{map[string]any{"role": "user", "content": "PROBE"}},
		"tools": []any{map[string]any{"name": "evil", "parameters": map[string]any{"type": "object"}}}})
	var types []string
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				s := strings.Join(types, ",")
				for _, want := range []string{"TOOL_CALL_START", "TOOL_CALL_RESULT", "TEXT_MESSAGE_CONTENT", "RUN_FINISHED"} {
					if !strings.Contains(s, want) {
						t.Errorf("no %s in %s", want, s)
					}
				}
				return
			}
			types = append(types, ev["type"].(string))
			if ev["type"] == "RUN_ERROR" {
				t.Logf("RUN_ERROR %v", ev)
			}
		case <-timeout:
			t.Fatalf("stream did not end: %v", types)
		}
	}
}

// As on the VM: the cancelled call's approval is denied while the next
// run (AG-UI) is going, so the gateway answers the cancelled call late,
// on the MCP session the next run shares.
func TestLateAnswerToCancelledCall(t *testing.T) {
	for i := 0; i < 20; i++ {
		e := newEnv(t,
			testutil.Call("c1", "zypp__install_patches", map[string]any{}),
			testutil.Call("c2", "zypp__list_updates", map[string]any{}),
			testutil.Text("listed"),
		)
		e.gw.Ask["zypp__install_patches"] = "a-1"
		_, b := e.do("POST", "/v1/activities", "0:root", map[string]any{"prompt": "install"})
		var st struct{ ID string }
		json.Unmarshal(b, &st)
		waitFor(t, func() bool {
			a, _ := e.l.Governor.Activity(st.ID)
			return a.State == governance.StateWaiting
		})
		e.do("POST", "/v1/activities/"+st.ID+"/cancel", "0:root", nil)
		ch := e.agui(map[string]any{"threadId": "custos-test-1", "messages": []any{map[string]any{"role": "user", "content": "PROBE"}}})
		go func() { time.Sleep(time.Duration(i) * time.Millisecond); e.gw.Decide("a-1", false) }()
		var types []string
		timeout := time.After(20 * time.Second)
	read:
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					break read
				}
				types = append(types, ev["type"].(string))
				if ev["type"] == "RUN_ERROR" {
					t.Logf("RUN_ERROR %v", ev)
				}
			case <-timeout:
				t.Fatalf("stream did not end: %v", types)
			}
		}
		s := strings.Join(types, ",")
		if !strings.Contains(s, "TOOL_CALL_RESULT") || !strings.Contains(s, "RUN_FINISHED") {
			t.Fatalf("run %d: %s", i, s)
		}
	}
}
