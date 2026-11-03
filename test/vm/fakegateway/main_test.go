package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenRequired(t *testing.T) {
	_, h, seen := New("real")
	for tok, want := range map[string]int{"openshell:resolve:env:AGENT_GATEWAY_TOKEN": 401, "": 401, "real": 200} {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("token %q: %d, want %d", tok, w.Code, want)
		}
	}
	if seen.Right != 1 || seen.Wrong != 2 {
		t.Errorf("seen %+v", seen)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/other", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("other path %d", w.Code)
	}
}
