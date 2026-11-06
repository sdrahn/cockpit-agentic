package gateway

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProposalKey is mcp-gateway's _meta key of a call's proposal (its R5,
// docs/requirements/mcp-gateway-for-custos-0.4.md): the agent's account
// of the call, which the gateway shows with the approval and the review
// item, marked as the client's claim. It is never forwarded to the
// server, and the gateway's policy never sees it.
const ProposalKey = "io.github.sdrahn.mcp-gateway/proposal"

// The gateway's limits (bytes, entries). A proposal over them, or with a
// control character, is dropped by the gateway (the call goes on), so
// Clean keeps within them.
const (
	maxSummary  = 300
	maxImpact   = 1000
	maxLine     = 300
	maxEntries  = 10
	maxKey      = 64
	maxProposal = 4096
)

// Proposal is the agent's account of a call (CONTRACT.md §7).
type Proposal struct {
	Summary  string            `json:"summary"`
	Impact   string            `json:"impact,omitempty"`
	Steps    []string          `json:"steps,omitempty"`
	Context  map[string]string `json:"context,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
}

// Clean makes p one the gateway takes: plain text only (no control or
// bidirectional formatting characters; line breaks only in the impact),
// each part cut at the gateway's limits, at most ten entries of each
// list; a summary always. Entries are dropped from the end until the
// whole fits in 4 KiB.
func (p Proposal) Clean() Proposal {
	out := Proposal{Summary: clip(plainLine(p.Summary), maxSummary), Impact: clip(plainText(p.Impact), maxImpact)}
	if out.Summary == "" {
		out.Summary = "The model gave no reason for this call."
	}
	for _, s := range p.Steps {
		if s = clip(plainLine(s), maxLine); s != "" && len(out.Steps) < maxEntries {
			out.Steps = append(out.Steps, s)
		}
	}
	for _, s := range p.Warnings {
		if s = clip(plainLine(s), maxLine); s != "" && len(out.Warnings) < maxEntries {
			out.Warnings = append(out.Warnings, s)
		}
	}
	for k, v := range p.Context {
		k, v = clip(plainLine(k), maxKey), clip(plainLine(v), maxLine)
		if k != "" && len(out.Context) < maxEntries {
			if out.Context == nil {
				out.Context = map[string]string{}
			}
			out.Context[k] = v
		}
	}
	for out.size() > maxProposal-256 { // room for the JSON around it
		switch {
		case len(out.Steps) > 0:
			out.Steps = out.Steps[:len(out.Steps)-1]
		case len(out.Warnings) > 1:
			out.Warnings = out.Warnings[:len(out.Warnings)-1]
		case out.Impact != "":
			out.Impact = ""
		default:
			return out
		}
	}
	return out
}

func (p Proposal) size() int {
	n := len(p.Summary) + len(p.Impact)
	for _, s := range append(append([]string{}, p.Steps...), p.Warnings...) {
		n += len(s) + 4
	}
	for k, v := range p.Context {
		n += len(k) + len(v) + 6
	}
	return n
}

// plainLine is s on one line: whitespace runs as one space, characters
// the gateway refuses left out.
func plainLine(s string) string {
	return strings.Join(strings.Fields(strip(s, false)), " ")
}

// plainText is s with its line breaks, without characters the gateway
// refuses.
func plainText(s string) string { return strings.TrimSpace(strip(s, true)) }

func strip(s string, lines bool) string {
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case lines && (r == '\n' || r == '\t'):
			return r
		case r == '\n' || r == '\t' || r == '\r':
			return ' '
		case unicode.IsControl(r), r == ' ', r == ' ',
			r >= '‪' && r <= '‮', r >= '⁦' && r <= '⁩':
			return -1
		}
		return r
	}, s)
}

// clip cuts s to at most n bytes, at a rune, with "…" when it cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
