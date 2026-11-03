// Package clienttools offers tools defined by an AG-UI frontend to the
// model, per session (CONTRACT.md §11). The frontend runs them; the
// agent only passes the call out and ends the run.
package clienttools

import (
	"fmt"
	"sync"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// Def is a tool as the frontend described it.
type Def struct {
	Name        string
	Description string
	Parameters  any // JSON Schema
}

// Registry holds the client tools of each session's current run.
type Registry struct {
	mu   sync.Mutex
	defs map[string][]Def
}

func NewRegistry() *Registry { return &Registry{defs: map[string][]Def{}} }

// Set offers defs in session until the returned function is called.
func (r *Registry) Set(session string, defs []Def) (clear func()) {
	r.mu.Lock()
	r.defs[session] = defs
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.defs, session)
		r.mu.Unlock()
	}
}

// Toolset is the ADK toolset of the registry.
func (r *Registry) Toolset() tool.Toolset { return toolset{r} }

type toolset struct{ r *Registry }

func (toolset) Name() string { return "agui-client-tools" }

func (t toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	t.r.mu.Lock()
	defer t.r.mu.Unlock()
	var out []tool.Tool
	for _, d := range t.r.defs[ctx.SessionID()] {
		out = append(out, &clientTool{d})
	}
	return out, nil
}

type clientTool struct{ d Def }

func (c *clientTool) Name() string        { return c.d.Name }
func (c *clientTool) Description() string { return c.d.Description }
func (c *clientTool) IsLongRunning() bool { return true }
func (c *clientTool) ClientTool() bool    { return true }

func (c *clientTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: c.d.Name, Description: c.d.Description, ParametersJsonSchema: c.d.Parameters}
}

// Run hands the call to the frontend: the run ends here, and the
// frontend answers with a tool message in its next request.
func (c *clientTool) Run(ctx agent.ToolContext, _ any) (map[string]any, error) {
	ctx.Actions().SkipSummarization = true
	return map[string]any{"status": "pending", "detail": "the frontend runs this tool"}, nil
}

// ProcessRequest declares the tool to the model (as ADK's PackTool does).
func (c *clientTool) ProcessRequest(_ agent.ToolContext, req *model.LLMRequest) error {
	if req.Tools == nil {
		req.Tools = map[string]any{}
	}
	if _, dup := req.Tools[c.Name()]; dup {
		return fmt.Errorf("duplicate tool: %q", c.Name())
	}
	req.Tools[c.Name()] = c
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	for _, t := range req.Config.Tools {
		if t != nil && t.FunctionDeclarations != nil {
			t.FunctionDeclarations = append(t.FunctionDeclarations, c.Declaration())
			return nil
		}
	}
	req.Config.Tools = append(req.Config.Tools, &genai.Tool{FunctionDeclarations: []*genai.FunctionDeclaration{c.Declaration()}})
	return nil
}
