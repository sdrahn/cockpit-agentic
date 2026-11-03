// fakemodel is a scripted OpenAI-compatible chat completion server for
// the functional tests (package cockpit-agentic-tests): agents talk to
// it like to a local model server, so the tests need no model and give
// the same answers every time.
//
// The last message decides the answer:
//
//	a tool result                  → text "done: <the result>"
//	user text with PROBE           → call <prefix>__probe {}
//	user text with CHANGE          → text with the reason, and call <prefix>__change {"value": "test"}
//	user text with DELEGATE a t…   → call agents__delegate {"agent": a, "task": "t…"}
//	anything else                  → text "ok"
//
// A tool the request does not offer is not called; the answer then says
// so ("not offered: <tool>"), which is how the tests see hidden tools.
//
// Usage: fakemodel [--listen 127.0.0.1:8008] [--prefix agentic-test]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
)

type message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type request struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Tools    []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

// Model answers requests.
type Model struct {
	Prefix string
	n      atomic.Int64
}

// Respond returns the answer to a request.
func (m *Model) Respond(req *request) message {
	offered := map[string]bool{}
	for _, t := range req.Tools {
		offered[t.Function.Name] = true
	}
	text := func(s string) message { return message{Role: "assistant", Content: &s} }
	if len(req.Messages) == 0 {
		return text("ok")
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role == "tool" {
		c := ""
		if last.Content != nil {
			c = *last.Content
		}
		if len(c) > 300 {
			c = c[:300]
		}
		return text("done: " + c)
	}
	in := ""
	if last.Content != nil {
		in = *last.Content
	}
	call := func(reason, name string, args map[string]any) message {
		if !offered[name] {
			return text("not offered: " + name)
		}
		b, _ := json.Marshal(args)
		var tc toolCall
		tc.ID = fmt.Sprintf("call_%d", m.n.Add(1))
		tc.Type = "function"
		tc.Function.Name, tc.Function.Arguments = name, string(b)
		msg := message{Role: "assistant", ToolCalls: []toolCall{tc}}
		if reason != "" {
			msg.Content = &reason
		}
		return msg
	}
	switch {
	case strings.Contains(in, "DELEGATE"):
		f := strings.Fields(in[strings.Index(in, "DELEGATE")+len("DELEGATE"):])
		if len(f) < 2 {
			return text("usage: DELEGATE <agent> <task>")
		}
		return call("Handing this to "+f[0]+".", "agents__delegate", map[string]any{"agent": f[0], "task": strings.Join(f[1:], " ")})
	case strings.Contains(in, "CHANGE"):
		return call("Writing the test marker; no other impact.", m.Prefix+"__change", map[string]any{"value": "test"})
	case strings.Contains(in, "PROBE"):
		return call("", m.Prefix+"__probe", map[string]any{})
	}
	return text("ok")
}

func (m *Model) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.NotFound(w, r)
		return
	}
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	msg := m.Respond(&req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": msg, "finish_reason": "stop"}},
		"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
	})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8008", "address")
	prefix := flag.String("prefix", "agentic-test", "the gateway server whose tools PROBE and CHANGE call")
	flag.Parse()
	log.Fatal(http.ListenAndServe(*listen, &Model{Prefix: *prefix}))
}
