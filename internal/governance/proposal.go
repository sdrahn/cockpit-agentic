package governance

import (
	"fmt"

	"github.com/sdrahn/custos/internal/gateway"
)

// Proposal is the agent's account of a call in flight, which the gateway
// shows with its approval and review item (mcp-gateway's proposal, R5;
// CONTRACT.md §7): the model's stated reason, the activity and who started
// it, and the signs of a suspected prompt injection. Self-asserted, like
// what it is made of: the gateway marks it as the client's claim and its
// policy never sees it. nil if the call is not known.
func (g *Governor) Proposal(invocation, callID string) *gateway.Proposal {
	g.mu.Lock()
	defer g.mu.Unlock()
	in := g.inv[invocation]
	if in == nil {
		return nil
	}
	var c *Call
	for _, x := range in.calls {
		if x.CallID == callID {
			c = x
		}
	}
	if c == nil {
		return nil
	}
	p := &gateway.Proposal{Summary: c.Rationale, Context: map[string]string{
		"agent":      g.cfg.Name,
		"activity":   in.act.ID,
		"started by": originText(in.act.Origin),
	}}
	// The whole rationale when one line of it is not all of it.
	if len(c.Rationale) > 280 {
		p.Impact = c.Rationale
	}
	if in.act.Parent != nil {
		p.Context["delegated by"] = in.act.Parent.Agent + " (" + in.act.Parent.Activity + ")"
	}
	if in.act.Root.Activity != in.act.ID {
		p.Context["started in"] = in.act.Root.Agent + " (" + in.act.Root.Activity + ")"
	}
	if in.act.Playbook != "" {
		p.Context["playbook"] = in.act.Playbook
	}
	for _, s := range c.SuspectedInjection {
		w := fmt.Sprintf("Suspected prompt injection: the output of %s (%s) %s", s.Tool, s.CallID, s.Sign)
		if s.Quote != "" {
			w += ": “" + s.Quote + "”"
		}
		p.Warnings = append(p.Warnings, w)
	}
	return p
}

// originText is who or what started an activity, for the proposal.
func originText(o Origin) string {
	switch o.Kind {
	case "schedule":
		return "schedule " + o.Schedule
	case "chat":
		return "chat with " + o.By
	case "delegation":
		return "delegation from " + o.By
	case "":
		return "unknown"
	}
	return o.Kind + " (" + o.By + ")"
}
