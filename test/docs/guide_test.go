// Package docs checks the user guide (docs/user-guide) and its mcp-docs
// collection file (packaging/mcp-docs/custos.yaml) as mcp-docs reads
// them: front matter, one title per document, sections small enough to
// read whole, explicit anchors that the links name, and every chapter in
// the index.
package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	guideDir = "../../docs/user-guide"
	collFile = "../../packaging/mcp-docs/custos.yaml"
	// mcp-docs's advice for a section an agent reads at once.
	maxSection = 8 << 10
)

var (
	headingRE = regexp.MustCompile(`^(#{1,6}) (.*?)(?: \{#([a-z0-9-]+)\})?$`)
	linkRE    = regexp.MustCompile(`\]\(([^)#\s]*)(?:#([^)\s]+))?\)`)
)

type heading struct {
	level int
	text  string
	id    string
	start int // byte offset of the section
}

type doc struct {
	name     string
	front    map[string]any
	body     string
	headings []heading
}

// parse splits off the front matter and finds the headings outside
// code fences.
func parse(t *testing.T, name string, b []byte) doc {
	t.Helper()
	d := doc{name: name}
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		t.Errorf("%s: no front matter", name)
	} else {
		end := strings.Index(s[4:], "\n---\n")
		if end < 0 {
			t.Fatalf("%s: front matter not closed", name)
		}
		if err := yaml.Unmarshal([]byte(s[4:4+end]), &d.front); err != nil {
			t.Errorf("%s: front matter: %v", name, err)
		}
		s = s[4+end+5:]
	}
	d.body = s
	fence := false
	off := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		l := strings.TrimRight(line, "\n")
		if strings.HasPrefix(l, "```") {
			fence = !fence
		} else if !fence {
			if m := headingRE.FindStringSubmatch(l); m != nil {
				d.headings = append(d.headings, heading{len(m[1]), m[2], m[3], off})
			}
		}
		off += len(line)
	}
	if fence {
		t.Errorf("%s: a code fence is not closed", name)
	}
	return d
}

func load(t *testing.T) map[string]doc {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(guideDir, "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no guide: %v", err)
	}
	docs := map[string]doc{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		docs[filepath.Base(f)] = parse(t, filepath.Base(f), b)
	}
	return docs
}

func TestGuideStructure(t *testing.T) {
	for name, d := range load(t) {
		for _, k := range []string{"title", "description"} {
			if v, _ := d.front[k].(string); v == "" {
				t.Errorf("%s: front matter has no %s", name, k)
			}
		}
		h1 := 0
		ids := map[string]bool{}
		for i, h := range d.headings {
			if h.level == 1 {
				h1++
				if title, _ := d.front["title"].(string); h.text != title && name != "README.md" {
					t.Errorf("%s: title %q, heading %q", name, title, h.text)
				}
				continue
			}
			if h.id == "" {
				t.Errorf("%s: %q has no {#id}", name, h.text)
			} else if ids[h.id] {
				t.Errorf("%s: id %s twice", name, h.id)
			}
			ids[h.id] = true
			end := len(d.body)
			for _, n := range d.headings[i+1:] {
				if n.level <= h.level {
					end = n.start
					break
				}
			}
			if size := end - h.start; size > maxSection {
				t.Errorf("%s#%s: %d bytes, more than %d", name, h.id, size, maxSection)
			}
		}
		if h1 != 1 {
			t.Errorf("%s: %d level-1 headings, want 1", name, h1)
		}
	}
}

func TestGuideLinks(t *testing.T) {
	docs := load(t)
	ids := map[string]map[string]bool{}
	for name, d := range docs {
		ids[name] = map[string]bool{}
		for _, h := range d.headings {
			if h.id != "" {
				ids[name][h.id] = true
			}
		}
	}
	for name, d := range docs {
		for _, m := range linkRE.FindAllStringSubmatch(d.body, -1) {
			target, id := m[1], m[2]
			if strings.Contains(target, "://") {
				continue
			}
			if target == "" {
				target = name
			}
			if strings.Contains(target, "/") {
				// Outside the guide: not installed with it (custos-docs
				// has the guide and CONTRACT.md only).
				t.Errorf("%s: link to %s, outside the installed guide", name, m[0])
				continue
			}
			if _, ok := docs[target]; !ok {
				t.Errorf("%s: link to a missing %s", name, target)
			} else if id != "" && !ids[target][id] {
				t.Errorf("%s: link to %s#%s, which has no such section", name, target, id)
			}
		}
	}
}

func TestIndexNamesEveryChapter(t *testing.T) {
	docs := load(t)
	index, ok := docs["README.md"]
	if !ok {
		t.Fatal("no README.md")
	}
	for name, d := range docs {
		if name == "README.md" {
			continue
		}
		title, _ := d.front["title"].(string)
		if !strings.Contains(index.body, "["+title+"]("+name+")") {
			t.Errorf("the index has no [%s](%s)", title, name)
		}
	}
}

func TestCollectionFile(t *testing.T) {
	b, err := os.ReadFile(collFile)
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"name": true, "title": true, "root": true, "description": true,
		"index": true, "include": true, "exclude": true, "also": true, "list_items": true, "instructions": true}
	for k := range c {
		if !known[k] {
			t.Errorf("%s: unknown key %s", collFile, k)
		}
	}
	want := map[string]string{"name": "custos", "root": "/usr/share/custos/docs", "index": "user-guide/README.md"}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s: %s is %v, want %s", collFile, k, c[k], v)
		}
	}
	// The Makefile installs the guide there (install-doc, DOCDIR).
	mk, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"DOCDIR     ?= $(DATADIR)/custos/docs", "$(DESTDIR)$(DOCDIR)/user-guide/", "$(DESTDIR)$(DOCDIR)/CONTRACT.md"} {
		if !bytes.Contains(mk, []byte(s)) {
			t.Errorf("Makefile: no %q", s)
		}
	}
}
