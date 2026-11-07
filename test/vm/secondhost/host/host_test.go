package host_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sdrahn/custos/test/contract"
	"github.com/sdrahn/custos/test/vm/secondhost/host"
)

// The second host passes the contract's checks (#112), as the reference
// launcher does (internal/launcher's TestContract).
func TestContract(t *testing.T) {
	var mu sync.Mutex
	recs := map[string][]map[string]string{}
	h := host.New("mock", "custos-second-host@mock.service", func(event, _ string, f map[string]string) {
		mu.Lock()
		defer mu.Unlock()
		recs[event] = append(recs[event], f)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, name, _ := strings.Cut(r.Header.Get("X-Test-Caller"), ":")
		n, _ := strconv.Atoi(uid)
		h.Handler().ServeHTTP(w, r.WithContext(host.WithCaller(r.Context(), host.Caller{UID: n, Name: name})))
	}))
	defer srv.Close()
	contract.Check(t, contract.Host{
		Name: "mock",
		Do: func(method, path, caller string, body any) (int, []byte) {
			var rd io.Reader
			if body != nil {
				b, _ := json.Marshal(body)
				rd = bytes.NewReader(b)
			}
			req, _ := http.NewRequest(method, srv.URL+path, rd)
			req.Header.Set("X-Test-Caller", caller)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			b, _ := io.ReadAll(res.Body)
			return res.StatusCode, b
		},
		Records: func(event string) []map[string]string {
			mu.Lock()
			defer mu.Unlock()
			return recs[event]
		},
	})
}

// The scripted activities: WAIT runs until cancelled by its user, FAIL
// fails; a delegated one names its parent only from its own caller.
func TestScripted(t *testing.T) {
	h := host.New("mock", "x.service", func(string, string, map[string]string) {})
	do := func(method, path, caller string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		uid, name, _ := strings.Cut(caller, ":")
		n, _ := strconv.Atoi(uid)
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, r.WithContext(host.WithCaller(r.Context(), host.Caller{UID: n, Name: name})))
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	_, m := do("POST", "/v1/activities", "1000:alice", map[string]any{"prompt": "WAIT for it"})
	id := m["id"].(string)
	if _, a := do("GET", "/v1/activities/"+id, "1000:alice", nil); a["state"] != "running" {
		t.Errorf("WAIT: %v", a)
	}
	if code, _ := do("POST", "/v1/activities/"+id+"/cancel", "1001:bob", nil); code != 403 {
		t.Errorf("another person cancelled: %d", code)
	}
	if code, _ := do("POST", "/v1/activities/"+id+"/cancel", "1000:alice", nil); code != 204 {
		t.Errorf("its user's cancel: %d", code)
	}
	if _, a := do("GET", "/v1/activities/"+id, "1000:alice", nil); a["state"] != "cancelled" {
		t.Errorf("cancelled: %v", a)
	}
	if _, a := do("POST", "/v1/activities", "1000:alice", map[string]any{"prompt": "FAIL now", "wait": true}); a["state"] != "failed" {
		t.Errorf("FAIL: %v", a)
	}
	trace := map[string]string{"parent_agent": "triage", "parent_activity": "e-1", "root_agent": "triage", "root_activity": "e-0"}
	_, a := do("POST", "/v1/activities", "990:agent-triage", map[string]any{"prompt": "x", "wait": true, "trace": trace})
	if a["origin"].(map[string]any)["kind"] != "delegation" || a["parent"].(map[string]any)["activity"] != "e-1" || a["root"].(map[string]any)["activity"] != "e-0" {
		t.Errorf("delegated: %v", a)
	}
	_, b := do("POST", "/v1/activities", "991:agent-other", map[string]any{"prompt": "x", "wait": true, "trace": trace})
	if b["parent"] != nil {
		t.Errorf("a trace naming another agent was believed: %v", b)
	}
	// An agent reads what it delegated, and nothing else.
	if code, _ := do("GET", "/v1/activities/"+a["id"].(string), "990:agent-triage", nil); code != 200 {
		t.Errorf("its own: %d", code)
	}
	if code, _ := do("GET", "/v1/activities/"+id, "990:agent-triage", nil); code != 404 {
		t.Errorf("another's: %d", code)
	}
}
