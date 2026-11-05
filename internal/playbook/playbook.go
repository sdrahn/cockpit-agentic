// Package playbook reads the diagnostic playbooks (CONTRACT.md §14c):
// published, defined diagnostic procedures, each a list of read-only
// tools to call and what to look for. A playbook grants nothing: each
// step is a gateway call, which the gateway decides like any other.
package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The playbooks' directories: the package's, and the administrator's,
// where a file of the same name replaces the package's and an empty one
// hides it.
const (
	Vendor = "/usr/share/custos/playbooks"
	Site   = "/etc/custos/playbooks"
)

// Playbook is one playbook.
type Playbook struct {
	Name        string   `yaml:"-" json:"-"`
	Title       string   `yaml:"title" json:"title"`
	Description string   `yaml:"description" json:"description"`
	Needs       []string `yaml:"needs,omitempty" json:"needs,omitempty"`
	Steps       []Step   `yaml:"steps" json:"steps"`
	LookFor     string   `yaml:"look_for" json:"look_for"`
	// File is where it was read from (agent-admin playbooks).
	File string `yaml:"-" json:"file"`
	// Error says why a playbook file cannot be used (agent-admin
	// playbooks lists it with the error instead of failing).
	Error string `yaml:"-" json:"error,omitempty"`
}

// Step is a tool to call, server__tool as the gateway names it.
type Step struct {
	Tool     string `yaml:"tool" json:"tool"`
	Why      string `yaml:"why" json:"why"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
}

var (
	nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	toolRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*__[A-Za-z0-9_.-]+$`)
)

// ValidName reports whether s is a valid playbook name (an agent name's
// form).
func ValidName(s string) bool { return nameRE.MatchString(s) }

// Parse reads one playbook file: one or more playbooks by name. Unknown
// keys are errors.
func Parse(b []byte) (map[string]*Playbook, error) {
	var doc map[string]*Playbook
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	for name, p := range doc {
		if !ValidName(name) {
			return nil, fmt.Errorf("%q is not a valid playbook name", name)
		}
		if p == nil {
			return nil, fmt.Errorf("%s: empty", name)
		}
		p.Name = name
		if err := p.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return doc, nil
}

func (p *Playbook) check() error {
	if strings.TrimSpace(p.Title) == "" {
		return errors.New("title: missing")
	}
	if strings.TrimSpace(p.LookFor) == "" {
		return errors.New("look_for: missing")
	}
	if len(p.Steps) == 0 {
		return errors.New("steps: at least one")
	}
	required := 0
	for i, s := range p.Steps {
		if !toolRE.MatchString(s.Tool) {
			return fmt.Errorf("steps[%d].tool: %q: want server__tool", i, s.Tool)
		}
		if strings.TrimSpace(s.Why) == "" {
			return fmt.Errorf("steps[%d].why: missing", i)
		}
		if !s.Optional {
			required++
		}
	}
	if required == 0 {
		return errors.New("steps: at least one that is not optional")
	}
	return nil
}

// Load reads the playbooks under root ("" on a host): the package's,
// replaced by the administrator's of the same file name, an empty file
// hiding it. A file that does not parse is kept as one playbook named
// after the file, with its Error.
func Load(root string) (map[string]*Playbook, error) {
	files := map[string]string{} // base name → path; /etc replaces /usr
	for _, dir := range []string{Vendor, Site} {
		m, err := filepath.Glob(filepath.Join(root, dir, "*.yaml"))
		if err != nil {
			return nil, err
		}
		for _, f := range m {
			files[filepath.Base(f)] = f
		}
	}
	out := map[string]*Playbook{}
	names := make([]string, 0, len(files))
	for b := range files {
		names = append(names, b)
	}
	sort.Strings(names)
	for _, base := range names {
		f := files[base]
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(b)) == 0 {
			continue // hidden by the administrator
		}
		doc, err := Parse(b)
		if err != nil {
			n := strings.TrimSuffix(base, ".yaml")
			out[n] = &Playbook{Name: n, File: strings.TrimPrefix(f, root), Error: err.Error()}
			continue
		}
		for n, p := range doc {
			p.File = strings.TrimPrefix(f, root)
			out[n] = p
		}
	}
	return out, nil
}

// Missing are the playbook's steps that are not optional and whose tool
// is not among those offered.
func (p *Playbook) Missing(offered map[string]bool) []string {
	var out []string
	for _, s := range p.Steps {
		if !s.Optional && !offered[s.Tool] {
			out = append(out, s.Tool)
		}
	}
	return out
}

// Prompt is what the model is given to run the playbook: its steps (the
// optional ones only when offered), what to look for, and the person's
// own words, if any.
func (p *Playbook) Prompt(offered map[string]bool, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run the diagnostic playbook %q (%s).\n", p.Name, p.Title)
	if p.Description != "" {
		b.WriteString(p.Description + "\n")
	}
	b.WriteString("\nCall these tools in this order; they only read:\n")
	n := 0
	for _, s := range p.Steps {
		if s.Optional && !offered[s.Tool] {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. %s: %s\n", n, s.Tool, s.Why)
	}
	b.WriteString("\nLook for: " + strings.TrimSpace(p.LookFor) + "\n")
	b.WriteString("\nThen report: what you found, your root-cause hypotheses with how confident you are, " +
		"the tool output each one rests on (quote it), and a written remediation plan for the administrator. " +
		"Change nothing yourself.\n")
	if strings.TrimSpace(extra) != "" {
		b.WriteString("\nThe administrator adds: " + strings.TrimSpace(extra) + "\n")
	}
	return b.String()
}
