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
		RunEnv: func(env []string, name string, args ...string) error {
			r.cmds = append(r.cmds, strings.Join(env, " ")+" "+name+" "+strings.Join(args, " "))
			return nil
		},
		Image:  "localhost/agent:test",
		Secret: func() string { return "0123456789abcdef0123456789abcdef" },
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
	if s := read("/etc/mcp-gateway/policy/agents/patch-pilot.yaml"); !strings.Contains(s, "patch-pilot:\n    mode: supervised") || !strings.Contains(s, "permissions: []") {
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
	s := read("/etc/mcp-gateway/policy/agents/patch-pilot.yaml")
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
	if s := read(GatewayStop); !strings.Contains(s, "_control:\n  stopped: true") {
		t.Errorf("stop file %q", s)
	}
	if err := a.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if s := read(GatewayStop); !strings.Contains(s, "stopped: false") {
		t.Errorf("after release-all: %q", s)
	}
	if err := a.Delete("patch-pilot", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.agentDir("patch-pilot")); !os.IsNotExist(err) {
		t.Error("config stays")
	}
	if _, err := os.Stat(a.p("/etc/mcp-gateway/policy/agents/patch-pilot.yaml")); !os.IsNotExist(err) {
		t.Error("gateway data stays")
	}
}

// agent-admin returns when the gateway's OPA has the change (here an OPA
// that lags behind), and says so when OPA never gets it.
func TestWaitsForThePolicy(t *testing.T) {
	a, _ := newAdmin(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { now = now.Add(100 * time.Millisecond); return now }
	a.PolicyWait = 5 * time.Second
	reads := 0
	a.OPAData = func(path string) (any, error) {
		reads++
		if path == "mcp/agents/_control/stopped" {
			return reads > 3, nil // loaded on the fourth read
		}
		return nil, nil
	}
	if err := a.StopAll("alice"); err != nil || reads != 4 {
		t.Errorf("stop-all: %v after %d reads", err, reads)
	}
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(yml), 0o644)
	a.OPAData = func(string) (any, error) { return nil, nil } // never loads
	if err := a.Create("patch-pilot", f); err == nil || !strings.Contains(err.Error(), "did not load the change") {
		t.Errorf("create with a stale gateway: %v", err)
	}
	a.OPAData = func(string) (any, error) { return nil, errNoOPA }
	if err := a.SetMode("patch-pilot", "advisory"); err != nil {
		t.Errorf("without the gateway's OPA: %v", err)
	}
	a.OPAData = func(string) (any, error) {
		return map[string]any{"mode": "bounded", "paused": false, "permissions": []any{}}, nil
	}
	if err := a.SetMode("patch-pilot", "bounded"); err != nil {
		t.Errorf("loaded at once: %v", err)
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

const oshYml = `version: 1
name: sb
model: {provider: openai, name: m, endpoint: "http://127.0.0.1:8008/v1"}
gateway: {url: "https://gw.example.com:8443/mcp", servers: [zypp]}
sandbox: {kind: openshell}
`

func TestOpenShellAgent(t *testing.T) {
	a, r := newAdmin(t)
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(oshYml), 0o644)
	if err := a.Create("sb", f); err != nil {
		t.Fatal(err)
	}
	dir := a.agentDir("sb")
	for name, want := range map[string]string{
		"openshell-policy.yaml":  "run_as_user: sandbox",
		"openshell-profile.yaml": "id: custos-gateway-sb",
		"openshell.env":          "OPENSHELL_GATEWAY=openshell",
		"relay.secret":           "0123456789abcdef",
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: %v, lacks %q:\n%s", name, err, want, b)
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, "relay.secret")); st.Mode().Perm() != 0o600 {
		t.Errorf("relay.secret mode %v", st.Mode())
	}
	all := strings.Join(r.cmds, "\n")
	if !strings.Contains(all, "openshell -g openshell profile import -f "+filepath.Join(dir, "openshell-profile.yaml")+" --global") {
		t.Errorf("profile not imported:\n%s", all)
	}
	if strings.Contains(all, "agent@sb.socket") || strings.Contains(all, "sandbox create") {
		t.Errorf("no systemd socket and no sandbox before the token:\n%s", all)
	}

	r.cmds = nil
	if err := a.SetCredential("sb", "gateway-token", strings.NewReader("tok-123\n")); err != nil {
		t.Fatal(err)
	}
	all = strings.Join(r.cmds, "\n")
	for _, want := range []string{
		"AGENT_GATEWAY_TOKEN=tok-123 openshell -g openshell provider create --name agent-sb-gateway --type custos-gateway-sb --credential AGENT_GATEWAY_TOKEN",
		"openshell -g openshell sandbox create --name agent-sb --from localhost/agent:test --policy " + filepath.Join(dir, "openshell-policy.yaml"),
		"--env CUSTOS_RELAY_SECRET=0123456789abcdef0123456789abcdef",
		"systemctl enable --now agent-openshell@sb.socket",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if strings.Contains(all, "tok-123 ") && strings.Count(all, "tok-123") != 1 {
		t.Error("token on a command line")
	}

	r.cmds = nil
	a.StopAll("alice")
	all = strings.Join(r.cmds, "\n")
	if !strings.Contains(all, "agent-openshell@*.service") || !strings.Contains(all, "openshell -g openshell sandbox stop agent-sb") {
		t.Errorf("stop-all misses the sandbox:\n%s", all)
	}
	r.cmds = nil
	a.ReleaseAll()
	if all = strings.Join(r.cmds, "\n"); !strings.Contains(all, "agent-openshell@sb.socket") {
		t.Errorf("release-all:\n%s", all)
	}
	r.cmds = nil
	if err := a.Delete("sb", false); err != nil {
		t.Fatal(err)
	}
	all = strings.Join(r.cmds, "\n")
	for _, want := range []string{"disable --now agent-openshell@sb.socket", "sandbox delete agent-sb", "provider delete agent-sb-gateway", "profile delete custos-gateway-sb"} {
		if !strings.Contains(all, want) {
			t.Errorf("delete misses %q:\n%s", want, all)
		}
	}
}

func TestGatewayTokenOnlyForOpenShell(t *testing.T) {
	a, _ := newAdmin(t)
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(yml), 0o644)
	a.Create("patch-pilot", f)
	if err := a.SetCredential("patch-pilot", "gateway-token", strings.NewReader("x")); err == nil {
		t.Error("gateway-token accepted for a systemd agent")
	}
}

const oshOAuthYml = `version: 1
name: sb
model: {provider: openai, name: m, endpoint: "http://127.0.0.1:8008/v1"}
gateway:
  url: "https://gw.example.com:8443/mcp"
  servers: [zypp]
  oauth: {token_url: "https://idp.example.com/token", client_id: agent-sb, scopes: [mcp]}
sandbox: {kind: openshell}
`

// With an OAuth client, OpenShell's gateway mints and renews the token:
// agent-admin hands it the client and mints the first token.
func TestOpenShellAgentOAuth(t *testing.T) {
	a, r := newAdmin(t)
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(oshOAuthYml), 0o644)
	if err := a.Create("sb", f); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(a.agentDir("sb"), "openshell-profile.yaml"))
	if !strings.Contains(string(b), "strategy: oauth2_client_credentials") || !strings.Contains(string(b), "token_url: https://idp.example.com/token") {
		t.Errorf("profile without the refresh:\n%s", b)
	}
	if err := a.SetCredential("sb", "gateway-token", strings.NewReader("tok")); err == nil {
		t.Error("a static token accepted for an agent with an OAuth client")
	}

	r.cmds = nil
	if err := a.SetCredential("sb", "gateway-client-secret", strings.NewReader("s3cret-value\n")); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(r.cmds, "\n")
	want := []string{
		"openshell -g openshell provider create --name agent-sb-gateway --type custos-gateway-sb --runtime-credentials",
		"CUSTOS_GATEWAY_CLIENT_SECRET=s3cret-value openshell -g openshell provider refresh configure agent-sb-gateway --credential-key AGENT_GATEWAY_TOKEN --strategy oauth2-client-credentials --material client_id=agent-sb --secret-material-env client_secret=CUSTOS_GATEWAY_CLIENT_SECRET",
		"openshell -g openshell provider refresh rotate agent-sb-gateway --credential-key AGENT_GATEWAY_TOKEN",
		"openshell -g openshell sandbox create --name agent-sb",
		"systemctl enable --now agent-openshell@sb.socket",
	}
	last := -1
	for _, w := range want {
		i := strings.Index(all, w)
		if i < 0 {
			t.Errorf("missing %q in\n%s", w, all)
			continue
		}
		if i < last {
			t.Errorf("%q out of order in\n%s", w, all)
		}
		last = i
	}
	// The secret only in the environment of the one command that needs it.
	if n := strings.Count(all, "s3cret-value"); n != 1 || strings.Contains(all, "client_secret=s3cret") {
		t.Errorf("the client secret is on a command line or repeated (%d):\n%s", n, all)
	}
}

func TestClientSecretNeedsOAuth(t *testing.T) {
	a, _ := newAdmin(t)
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(oshYml), 0o644)
	a.Create("sb", f)
	if err := a.SetCredential("sb", "gateway-client-secret", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "gateway.oauth") {
		t.Errorf("client secret without an OAuth client: %v", err)
	}
}

// The user whose rootless Podman runs the sandboxes goes to the relay
// (openshell.env), for its log source.
func TestOpenShellUser(t *testing.T) {
	a, _ := newAdmin(t)
	a.OpenShellUser = "osh"
	old := LookupUser
	LookupUser = func(string) (string, string, error) { return "1001", "/home/osh", nil }
	t.Cleanup(func() { LookupUser = old })
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(oshYml), 0o644)
	if err := a.Create("sb", f); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(a.agentDir("sb"), "openshell.env"))
	if !strings.Contains(string(b), "\nOPENSHELL_USER=osh\n") {
		t.Errorf("openshell.env:\n%s", b)
	}
	d, err := os.ReadFile(a.p(filepath.Join(UnitDir, "agent-openshell@sb.service.d", "openshell-user.conf")))
	if err != nil || !strings.Contains(string(d), "ReadWritePaths=-/home/osh/.local/share/containers -/run/user/1001\n") {
		t.Errorf("relay drop-in: %v\n%s", err, d)
	}
	a2, _ := newAdmin(t)
	a2.OpenShellUser = "osh\nEVIL=1"
	if err := a2.Create("sb", f); err == nil {
		t.Error("a user name with a newline was written to openshell.env")
	}
}
