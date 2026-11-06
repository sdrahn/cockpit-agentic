package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sdrahn/custos/internal/governance"
	"github.com/sdrahn/custos/internal/journal"
	"github.com/sdrahn/custos/internal/playbook"
	"github.com/sdrahn/custos/internal/testutil"
)

func playbooks(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put := func(name, s string) {
		f := filepath.Join(root, playbook.Vendor, name+".yaml")
		os.MkdirAll(filepath.Dir(f), 0o755)
		os.WriteFile(f, []byte(s), 0o644)
	}
	// fs__read_file is a gateway tool the agent is not shown (its
	// configuration names only the server zypp).
	put("updates", "updates:\n  title: Updates\n  steps:\n    - {tool: zypp__list_updates, why: Pending updates.}\n"+
		"    - {tool: fs__read_file, why: A file., optional: true}\n  look_for: Security patches.\n")
	put("files", "files:\n  title: Files\n  steps:\n    - {tool: fs__read_file, why: A file.}\n  look_for: Anything.\n")
	put("broken", "broken: {title: x}\n")
	return root
}

// An activity runs a playbook: the model gets its steps (those offered),
// what to look for and the person's words; the activity and its record
// name the playbook.
func TestPlaybookActivity(t *testing.T) {
	e := newEnvPlaybooks(t, playbooks(t), testutil.Text("Report: nothing pending."))
	res, b := e.do("POST", "/v1/activities", "", map[string]any{"playbook": "updates", "prompt": "nginx in particular", "wait": true})
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var act governance.Activity
	json.Unmarshal(b, &act)
	if act.Playbook != "updates" || act.State != governance.StateFinished {
		t.Errorf("activity %s", b)
	}
	sent := e.m.Calls[0].Contents[len(e.m.Calls[0].Contents)-1].Parts[0].Text
	for _, want := range []string{`Run the diagnostic playbook "updates"`, "1. zypp__list_updates: Pending updates.", "Look for: Security patches.",
		"The administrator adds: nginx in particular"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the model was not given %q:\n%s", want, sent)
		}
	}
	if strings.Contains(sent, "fs__read_file") {
		t.Errorf("an optional step not offered reached the model:\n%s", sent)
	}
	st := e.log.Events(journal.InvocationStart)
	if f := st[len(st)-1].Fields; f["PLAYBOOK"] != "updates" {
		t.Errorf("invocation.start %v", f)
	}
}

// A playbook is refused, before anything runs, when it is unknown,
// broken, or needs a tool the gateway does not offer the agent.
func TestPlaybookRefused(t *testing.T) {
	e := newEnvPlaybooks(t, playbooks(t))
	for _, c := range []struct {
		body map[string]any
		code int
		want string
	}{
		{map[string]any{"playbook": "files"}, 422, "needs tools the gateway does not offer agent patch-pilot: fs__read_file"},
		{map[string]any{"playbook": "no-such"}, 404, `no playbook "no-such"`},
		{map[string]any{"playbook": "broken"}, 400, "playbook broken"},
		{map[string]any{"playbook": "../etc"}, 400, "not a playbook name"},
		{map[string]any{}, 400, "playbook"},
	} {
		res, b := e.do("POST", "/v1/activities", "", c.body)
		if res.StatusCode != c.code || !strings.Contains(string(b), c.want) {
			t.Errorf("%v: %d %s", c.body, res.StatusCode, b)
		}
	}
	if n := len(e.log.Events(journal.InvocationStart)); n != 0 {
		t.Errorf("%d activities started", n)
	}
}

// GET /v1/playbooks says which playbooks this agent can run: those whose
// steps that are not optional all have tools the gateway offers it; the
// others with the tools they lack, a broken one with its error.
func TestPlaybooksOffered(t *testing.T) {
	e := newEnvPlaybooks(t, playbooks(t))
	res, b := e.do("GET", "/v1/playbooks", "", nil)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var got struct {
		Playbooks map[string]PlaybookOffer `json:"playbooks"`
	}
	json.Unmarshal(b, &got)
	pb := got.Playbooks
	if u := pb["updates"]; !u.Runnable || u.Title != "Updates" || len(u.Missing) != 0 {
		t.Errorf("updates (its optional step not offered): %+v", u)
	}
	if f := pb["files"]; f.Runnable || len(f.Missing) != 1 || f.Missing[0] != "fs__read_file" {
		t.Errorf("files: %+v", f)
	}
	if br := pb["broken"]; br.Runnable || br.Error == "" {
		t.Errorf("broken: %+v", br)
	}
}
