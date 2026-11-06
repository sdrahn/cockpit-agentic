package governance

import (
	"fmt"
	"regexp"
	"strings"
)

// Signs of a prompt injection (CONTRACT.md §7, O14): text a tool returned
// that reads like instructions to the model. A call that follows such
// output is marked as a suspected injection for the person who decides on
// it. The check is advisory: it runs in the agent's process and can be
// fooled or skipped. What stops an injected call is the gateway: the
// approval, and the tools it does not offer.

// Preceding is the output of an earlier call of the activity, as the
// approval item shows it: an excerpt, around the sign found in it.
type Preceding struct {
	CallID  string `json:"call_id"`
	Tool    string `json:"tool"`
	Excerpt string `json:"excerpt"`
}

// Suspicion is one sign of a prompt injection in an earlier call's
// output.
type Suspicion struct {
	CallID string `json:"call_id"` // the earlier call whose output has it
	Tool   string `json:"tool"`
	Sign   string `json:"sign"`            // what was found (injectionSigns, or the two below)
	Quote  string `json:"quote,omitempty"` // the text, at most quoteLen characters
	at     int    // where it is in the output
}

const (
	// precedingCalls is how many earlier outputs a call is checked
	// against and an approval item shows.
	precedingCalls = 3
	excerptLen     = 1200
	quoteLen       = 120
)

var injectionSigns = []struct {
	sign string
	re   *regexp.Regexp
}{
	{"tells the model to ignore its instructions",
		regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b[^.\n]{0,30}\b(previous|prior|earlier|above|all|your|system)\b[^.\n]{0,20}\b(instructions?|prompts?|rules|guidelines)\b`)},
	{"has a role marker of a chat template",
		regexp.MustCompile(`(?im)(<\|(im_start|im_end|system|assistant|user)\|>|\[/?INST\]|<</?SYS>>|^\s*(system|assistant)\s*:)`)},
	{"claims an approval",
		regexp.MustCompile(`(?i)\b(pre-?approved|already approved|approved by the|needs? no (confirmation|approval)|no (confirmation|approval) (is )?(needed|required)|without (asking|approval|confirmation))\b`)},
	{"gives the model a new role or policy",
		regexp.MustCompile(`(?i)\b(you are now|new (instructions|policy|role)|policy update|maintenance mode|developer mode)\b`)},
	{"asks for the model's instructions",
		regexp.MustCompile(`(?i)\b(your|the) (complete |full )?(system (prompt|instructions)|instructions)\b`)},
}

// suspect checks the outputs before a call (oldest first) for signs of
// an injection: the signs above, an instruction to use the called tool,
// and the call's argument values (argText) in an output.
func suspect(tool string, argText []string, before []toolResult) []Suspicion {
	bare := tool
	if _, t, ok := strings.Cut(tool, "__"); ok {
		bare = t
	}
	// "call the change tool", "invoke custos-test__change": the tool as a
	// tool, so that "run zypper patch" in zypper's own output is no sign
	// for zypp__patch.
	toolRe := regexp.MustCompile(`(?i)\b(call|run|use|execute|invoke)\b[^.\n]{0,40}(\b` + regexp.QuoteMeta(bare) +
		`\s+(tool|function)\b|` + regexp.QuoteMeta(tool) + `\b)`)
	var out []Suspicion
	for _, r := range before {
		add := func(sign string, loc []int) {
			out = append(out, Suspicion{CallID: r.callID, Tool: r.tool, Sign: sign, Quote: clip(r.text[loc[0]:loc[1]], quoteLen), at: loc[0]})
		}
		for _, s := range injectionSigns {
			if loc := s.re.FindStringIndex(r.text); loc != nil {
				add(s.sign, loc)
			}
		}
		if loc := toolRe.FindStringIndex(r.text); loc != nil {
			add("tells the model to use "+tool, loc)
		}
		for _, a := range argText {
			if i := directedValue(r.text, a); i >= 0 {
				// No quote: it would show an argument (§4); the excerpt
				// is around it.
				out = append(out, Suspicion{CallID: r.callID, Tool: r.tool, Sign: "has one of the call's argument values", at: i})
				break
			}
		}
	}
	return out
}

// directive is text that tells the model to use a value: "please set",
// "call ... with value", "pass the ids".
var directive = regexp.MustCompile(`(?i)\b(please|set|put|pass|use|call|invoke|execute|enter|type|fill|send|provide|give|value|ids?|argument|parameter)\b`)

// directedValue is where an output tells the model to use the value v:
// v on a line that has a directive before it; -1 if nowhere. A value that
// is merely there, as a unit name in a list or a log line, is the
// ordinary flow of a diagnosis (one tool's output, the next call's
// argument), not a sign.
func directedValue(text, v string) int {
	for from := 0; ; {
		j := strings.Index(text[from:], v)
		if j < 0 {
			return -1
		}
		i := from + j
		line := text[strings.LastIndex(text[:i], "\n")+1 : i]
		if directive.MatchString(line) {
			return i
		}
		from = i + len(v)
	}
}

// lastResults are the activity's newest outputs before the call, oldest
// first; g.mu must be held.
func (in *invocation) lastResults(callID string) []toolResult {
	var rs []toolResult
	for _, r := range in.results {
		if r.callID != callID {
			rs = append(rs, r)
		}
	}
	if len(rs) > precedingCalls {
		rs = rs[len(rs)-precedingCalls:]
	}
	return rs
}

// preceding is the approval item's view of the outputs: an excerpt of
// each, around the first suspicious text in it when there is one.
func preceding(rs []toolResult, sus []Suspicion) []Preceding {
	out := make([]Preceding, 0, len(rs))
	for _, r := range rs {
		at := 0
		for _, s := range sus {
			if s.CallID == r.callID {
				at = s.at
				break
			}
		}
		out = append(out, Preceding{CallID: r.callID, Tool: r.tool, Excerpt: excerpt(r.text, at)})
	}
	return out
}

// excerpt is up to excerptLen characters of text around at.
func excerpt(text string, at int) string {
	r := []rune(text)
	if len(r) <= excerptLen {
		return text
	}
	pos := len([]rune(text[:at]))
	start := max(0, min(pos-excerptLen/3, len(r)-excerptLen))
	s := string(r[start : start+excerptLen])
	if start > 0 {
		s = "…" + s
	}
	if start+excerptLen < len(r) {
		s += "…"
	}
	return s
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// argStrings are the string values of the arguments long enough to be
// telling (6 characters and more).
func argStrings(args map[string]any) []string {
	var all, out []string
	collectStrings(args, &all)
	for _, s := range all {
		if s = normalize(s); len(s) >= 6 {
			out = append(out, s)
		}
	}
	return out
}

// suspicionText is the journal's AGENT_SUSPECTED_INJECTION: the signs and
// the calls they were found in, without the quotes (outputs stay out of
// the journal).
func suspicionText(sus []Suspicion) string {
	var parts []string
	seen := map[string]bool{}
	for _, s := range sus {
		p := fmt.Sprintf("%s (%s) %s", s.Tool, s.CallID, s.Sign)
		if !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "; ")
}
