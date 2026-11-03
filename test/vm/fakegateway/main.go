// fakegateway stands in for mcp-gateway's Streamable HTTP listener in the
// OpenShell tests (test/openshell): the agent's tools (agentic-test__probe,
// agentic-test__change), an approval for change with the gateway's
// notice, and a check that requests carry the real token, which only
// OpenShell's supervisor can have put there (the agent holds a
// placeholder).
//
//	fakegateway --listen 0.0.0.0:18443 --admin 127.0.0.1:18444 --token-file F
//
// The admin listener: GET /tokens (what was seen), POST /approve/<id>,
// POST /decline/<id>, GET /executed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/sdrahn/cockpit-agentic/internal/testutil"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:18443", "MCP endpoint (path /mcp)")
	admin := flag.String("admin", "127.0.0.1:18444", "test control endpoint")
	tokenFile := flag.String("token-file", "", "the token requests must carry")
	flag.Parse()
	b, err := os.ReadFile(*tokenFile)
	if err != nil {
		log.Fatal(err)
	}
	g, mux, seen := New(strings.TrimSpace(string(b)))
	go func() { log.Fatal(http.ListenAndServe(*admin, adminMux(g, seen))) }()
	log.Fatal(http.ListenAndServe(*listen, mux))
}

// Seen counts the tokens requests carried: right and wrong.
type Seen struct {
	Right, Wrong int
	Wrongs       []string
}

// New returns the gateway and its MCP handler, which refuses requests
// without the token (401, as mcp-gateway does).
func New(token string) (*testutil.Gateway, http.Handler, *Seen) {
	g := testutil.NewGateway([]string{"agentic-test__probe", "agentic-test__change"})
	g.Ask["agentic-test__change"] = "a-osh-1"
	seen := &Seen{}
	h := g.Handler()
	return g, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/mcp") {
			http.NotFound(w, r)
			return
		}
		if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got != token {
			seen.Wrong++
			// The request and what it carried: which of the agent's requests
			// came without the token (or with the placeholder).
			seen.Wrongs = append(seen.Wrongs, fmt.Sprintf("%s %s session=%t %q", r.Method, r.URL.Path, r.Header.Get("Mcp-Session-Id") != "", got))
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		seen.Right++
		h.ServeHTTP(w, r)
	}), seen
}

func adminMux(g *testutil.Gateway, seen *Seen) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /tokens", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(seen) })
	// The tests' own probes (without a token) are not the agent's.
	m.HandleFunc("POST /tokens/reset", func(w http.ResponseWriter, r *http.Request) { *seen = Seen{} })
	m.HandleFunc("GET /executed", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(g.Ran()) })
	m.HandleFunc("POST /approve/{id}", func(w http.ResponseWriter, r *http.Request) { g.Decide(r.PathValue("id"), true) })
	m.HandleFunc("POST /decline/{id}", func(w http.ResponseWriter, r *http.Request) { g.Decide(r.PathValue("id"), false) })
	return m
}
