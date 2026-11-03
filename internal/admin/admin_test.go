package admin

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type rec struct{ cmds []string }

func newAdmin(t *testing.T) (*Admin, *rec) {
	r := &rec{}
	a := &Admin{
		Root: t.TempDir(),
		Run: func(_ io.Reader, name string, args ...string) error {
			r.cmds = append(r.cmds, name+" "+strings.Join(args, " "))
			return nil
		},
		Out: io.Discard, Now: func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) },
		Control: func(sock, path string) error {
			r.cmds = append(r.cmds, "POST "+sock+path)
			return nil
		},
		Start: func(sock string, body map[string]any) error {
			o := body["origin"].(map[string]any)
			r.cmds = append(r.cmds, "START "+sock+" "+body["prompt"].(string)+" "+o["kind"].(string)+" "+o["schedule"].(string)+" "+o["by"].(string))
			return nil
		},
	}
	return a, r
}

const yml = `version: 1
name: patch-pilot
model: {provider: openai, name: m, endpoint: "http://ai:8000/v1"}
gateway: {servers: [zypp]}
schedules:
  - {name: compliance, on_calendar: "Mon..Fri 06:00", prompt: "Run the compliance check."}
`

func TestCreateModePauseDelete(t *testing.T) {
	a, r := newAdmin(t)
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(yml), 0o644)
	if err := a.Create("patch-pilot", f); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string { b, _ := os.ReadFile(a.p(p)); return string(b) }
	if s := read("/etc/sysusers.d/agent-patch-pilot.conf"); !strings.Contains(s, "u agent-patch-pilot -") || !strings.Contains(s, "m agent-patch-pilot mcp-users") {
		t.Errorf("sysusers %q", s)
	}
	if s := read("/etc/mcp-gateway/policy/agents/patch-pilot/data.yaml"); !strings.Contains(s, "mode: supervised") || !strings.Contains(s, "permissions: []") {
		t.Errorf("gateway data %q", s)
	}
	if s := read("/etc/systemd/system/agent-schedule@patch-pilot_compliance.timer.d/schedule.conf"); !strings.Contains(s, "OnCalendar=Mon..Fri 06:00") {
		t.Errorf("timer %q", s)
	}
	all := strings.Join(r.cmds, "\n")
	for _, want := range []string{"systemd-sysusers", "systemctl enable --now agent@patch-pilot.socket", "systemctl enable --now agent-schedule@patch-pilot_compliance.timer"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if err := a.Create("patch-pilot", f); err == nil {
		t.Error("created twice")
	}
	if err := a.SetMode("patch-pilot", "bounded"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetMode("patch-pilot", "yolo"); err == nil {
		t.Error("bad mode accepted")
	}
	if err := a.SetPermissions("patch-pilot", strings.NewReader("- {server: zypp, tool: 'list_*', read_only: true}\n- {server: zypp, tool: install_patches, autonomous: true}\n")); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPermissions("patch-pilot", strings.NewReader("- {server: zypp, tool: x, allow_everything: true}\n")); err == nil {
		t.Error("unknown permission key accepted")
	}
	if err := a.Pause("patch-pilot", "alice", true); err != nil {
		t.Fatal(err)
	}
	s := read("/etc/mcp-gateway/policy/agents/patch-pilot/data.yaml")
	if !testHas(s, "mode: bounded", "paused: true", "paused_by: alice 2026-11-03T12:00:00Z", "install_patches", "autonomous: true") {
		t.Errorf("gateway data %q", s)
	}
	if !strings.Contains(strings.Join(r.cmds, "\n"), "POST /run/agents/patch-pilot/api.sock/v1/pause") {
		t.Error("agent not told to pause")
	}
	if names, _ := a.List(); len(names) != 1 || names[0] != "patch-pilot" {
		t.Errorf("list %v", names)
	}
	if err := a.StopAll("alice"); err != nil {
		t.Fatal(err)
	}
	if s := read(GatewayStop); !strings.Contains(s, "stopped: true") {
		t.Errorf("stop file %q", s)
	}
	if err := a.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.p(GatewayStop)); !os.IsNotExist(err) {
		t.Error("stop file stays")
	}
	if err := a.Delete("patch-pilot", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.agentDir("patch-pilot")); !os.IsNotExist(err) {
		t.Error("config stays")
	}
	if _, err := os.Stat(a.p("/etc/mcp-gateway/policy/agents/patch-pilot")); !os.IsNotExist(err) {
		t.Error("gateway data stays")
	}
}

func TestNames(t *testing.T) {
	a, _ := newAdmin(t)
	for _, bad := range []string{"../etc", "Root", "a", "x/y"} {
		if err := a.SetMode(bad, "advisory"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := a.SetCredential("patch-pilot", "../key", strings.NewReader("s")); err == nil {
		t.Error("bad credential name accepted")
	}
}

func testHas(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}

func TestTrigger(t *testing.T) {
	a, r := newAdmin(t)
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(yml), 0o644)
	if err := a.Create("patch-pilot", f); err != nil {
		t.Fatal(err)
	}
	r.cmds = nil
	if err := a.Trigger("patch-pilot_compliance"); err != nil {
		t.Fatal(err)
	}
	if want := "START /run/agents/patch-pilot/api.sock Run the compliance check. schedule compliance agent-schedule@patch-pilot_compliance.timer"; len(r.cmds) != 1 || r.cmds[0] != want {
		t.Errorf("got %v\nwant %s", r.cmds, want)
	}
	for _, bad := range []string{"patch-pilot_nope", "patch-pilot", "../x_y"} {
		if err := a.Trigger(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
