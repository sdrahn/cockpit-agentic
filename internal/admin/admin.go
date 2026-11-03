// Package admin is agent-admin: the administrative actions on agents
// that Cockpit runs with administrative access (CONTRACT.md §11). Every
// path hangs off Root, so tests can run it in a temporary directory.
package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sdrahn/cockpit-agentic/internal/config"
	"github.com/sdrahn/cockpit-agentic/internal/delegate"
)

type Admin struct {
	Root string // "" on a host; a temporary directory in tests
	// Run runs a command (systemctl, systemd-sysusers, systemd-creds).
	Run func(stdin io.Reader, name string, args ...string) error
	Out io.Writer
	// Now is the clock (tests).
	Now func() time.Time
	// Control posts to an agent's control API (pause, resume).
	Control func(sock, path string) error
}

func New() *Admin {
	return &Admin{
		Run: func(stdin io.Reader, name string, args ...string) error {
			c := exec.Command(name, args...)
			c.Stdin, c.Stdout, c.Stderr = stdin, os.Stdout, os.Stderr
			return c.Run()
		},
		Out: os.Stdout, Now: time.Now,
		Control: func(sock, path string) error { return delegate.Post(context.Background(), sock, path) },
	}
}

func (a *Admin) p(path string) string { return filepath.Join(a.Root, path) }

func (a *Admin) agentDir(name string) string {
	return a.p(filepath.Join(config.ConfigRoot, name))
}

// Paths of the contract (§3, §6).
const (
	SysusersDir   = "/etc/sysusers.d"
	CredstoreDir  = "/etc/credstore.encrypted"
	UnitDir       = "/etc/systemd/system"
	GatewayAgents = "/etc/mcp-gateway/policy/agents"
	GatewayStop   = "/etc/mcp-gateway/policy/agents_control/data.yaml"
	SocketDir     = "/run/agents"
)

func checkName(name string) error {
	if !config.ValidName(name) {
		return fmt.Errorf("%q is not a valid agent name", name)
	}
	return nil
}

// Check validates a configuration file (or an agent's) without changing anything.
func (a *Admin) Check(target string) error {
	if config.ValidName(target) {
		_, err := config.Load(a.agentDir(target))
		return err
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	_, err = config.Parse(b)
	return err
}

// Create installs a new agent from a configuration file.
func (a *Admin) Create(name, from string) error {
	if err := checkName(name); err != nil {
		return err
	}
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(b)
	if err != nil {
		return err
	}
	if cfg.Name != name {
		return fmt.Errorf("the file is for agent %q, not %q", cfg.Name, name)
	}
	dir := a.agentDir(name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("agent %s exists", name)
	}
	user := "agent-" + name
	sysusers := fmt.Sprintf("# agent-admin: the user of agent %[1]s (CONTRACT.md §2)\nu %[2]s - \"cockpit-agentic agent %[1]s\" /var/lib/agents/%[1]s /usr/sbin/nologin\nm %[2]s mcp-users\nm %[2]s agent-clients\n", name, user)
	sf := a.p(filepath.Join(SysusersDir, user+".conf"))
	if err := writeFile(sf, []byte(sysusers), 0o644); err != nil {
		return err
	}
	if err := a.Run(nil, "systemd-sysusers", sf); err != nil {
		return fmt.Errorf("creating user %s: %w", user, err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "agent.yaml"), b, 0o640); err != nil {
		return err
	}
	if err := a.Run(nil, "chgrp", "-R", user, dir); err != nil {
		return err
	}
	// Supervised by default, and nothing allowed until listed (§6).
	if err := a.writeGateway(name, &GatewayData{Mode: "supervised", Permissions: []map[string]any{}}); err != nil {
		return err
	}
	if err := a.syncSchedules(cfg); err != nil {
		return err
	}
	if err := a.Run(nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return a.Run(nil, "systemctl", "enable", "--now", "agent@"+name+".socket")
}

// Edit replaces an agent's configuration and restarts it if it runs.
func (a *Admin) Edit(name, from string) error {
	if err := checkName(name); err != nil {
		return err
	}
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(b)
	if err != nil {
		return err
	}
	if cfg.Name != name {
		return fmt.Errorf("the file is for agent %q, not %q", cfg.Name, name)
	}
	if err := writeFile(filepath.Join(a.agentDir(name), "agent.yaml"), b, 0o640); err != nil {
		return err
	}
	a.Run(nil, "chgrp", "agent-"+name, filepath.Join(a.agentDir(name), "agent.yaml"))
	if err := a.syncSchedules(cfg); err != nil {
		return err
	}
	a.Run(nil, "systemctl", "daemon-reload")
	return a.Run(nil, "systemctl", "try-restart", "agent@"+name+".service")
}

// Delete removes an agent. Its user stays: uids are not reused while
// files may still be theirs.
func (a *Admin) Delete(name string, keepState bool) error {
	if err := checkName(name); err != nil {
		return err
	}
	cfg, _ := config.Load(a.agentDir(name))
	units := []string{"agent@" + name + ".socket", "agent@" + name + ".service"}
	if cfg != nil {
		for _, s := range cfg.Schedules {
			units = append(units, scheduleUnit(name, s.Name)+".timer")
		}
	}
	a.Run(nil, "systemctl", append([]string{"disable", "--now"}, units...)...)
	rm := []string{a.agentDir(name), a.p(filepath.Join(GatewayAgents, name)), a.p(filepath.Join(UnitDir, "agent@"+name+".service.d"))}
	matches, _ := filepath.Glob(a.p(filepath.Join(CredstoreDir, "agent-"+name+".*")))
	rm = append(rm, matches...)
	timers, _ := filepath.Glob(a.p(filepath.Join(UnitDir, "agent-schedule@"+name+":*")))
	rm = append(rm, timers...)
	if !keepState {
		rm = append(rm, a.p("/var/lib/agents/"+name))
	}
	for _, p := range rm {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return a.Run(nil, "systemctl", "daemon-reload")
}

// SetCredential encrypts a secret from stdin for the agent (§5).
func (a *Admin) SetCredential(name, cred string, secret io.Reader) error {
	if err := checkName(name); err != nil {
		return err
	}
	if !config.ValidCredential(cred) {
		return fmt.Errorf("%q is not a valid credential name", cred)
	}
	id := "agent-" + name + "." + cred
	if err := os.MkdirAll(a.p(CredstoreDir), 0o700); err != nil {
		return err
	}
	return a.Run(secret, "systemd-creds", "encrypt", "--name="+id, "-", a.p(filepath.Join(CredstoreDir, id)))
}

// GatewayData is data.mcp.agents.<name> (§6).
type GatewayData struct {
	Mode        string           `yaml:"mode" json:"mode"`
	Paused      bool             `yaml:"paused,omitempty" json:"paused"`
	PausedBy    string           `yaml:"paused_by,omitempty" json:"paused_by,omitempty"`
	Permissions []map[string]any `yaml:"permissions" json:"permissions"`
}

func (a *Admin) gatewayFile(name string) string {
	return a.p(filepath.Join(GatewayAgents, name, "data.yaml"))
}

func (a *Admin) readGateway(name string) (*GatewayData, error) {
	b, err := os.ReadFile(a.gatewayFile(name))
	if err != nil {
		return nil, err
	}
	var d GatewayData
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", a.gatewayFile(name), err)
	}
	return &d, nil
}

func (a *Admin) writeGateway(name string, d *GatewayData) error {
	if d.Permissions == nil {
		d.Permissions = []map[string]any{}
	}
	b, err := yaml.Marshal(d)
	if err != nil {
		return err
	}
	head := "# Written by agent-admin: the autonomy mode and permissions of agent " + name + "\n# (cockpit-agentic CONTRACT.md §6). mcp-gateway's OPA reloads it by itself.\n"
	return writeFile(a.gatewayFile(name), append([]byte(head), b...), 0o644)
}

// SetMode sets the autonomy mode, enforced by the gateway.
func (a *Admin) SetMode(name, mode string) error {
	if err := checkName(name); err != nil {
		return err
	}
	switch mode {
	case "advisory", "supervised", "bounded":
	default:
		return fmt.Errorf("mode %q: want advisory, supervised or bounded", mode)
	}
	d, err := a.readGateway(name)
	if err != nil {
		return err
	}
	d.Mode = mode
	return a.writeGateway(name, d)
}

// SetPermissions replaces the agent's gateway permissions from a YAML list.
func (a *Admin) SetPermissions(name string, from io.Reader) error {
	if err := checkName(name); err != nil {
		return err
	}
	var perms []map[string]any
	b, err := io.ReadAll(from)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&perms); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("permissions: %w", err)
	}
	for i, p := range perms {
		if _, ok := p["server"].(string); !ok {
			return fmt.Errorf("permissions[%d]: server missing", i)
		}
		for k := range p {
			switch k {
			case "server", "tool", "resource", "prompt", "args", "read_only", "autonomous", "approval_channel", "approval_scopes", "effect", "obligations":
			default:
				return fmt.Errorf("permissions[%d]: unknown key %q", i, k)
			}
		}
	}
	d, err := a.readGateway(name)
	if err != nil {
		return err
	}
	d.Permissions = perms
	return a.writeGateway(name, d)
}

// Pause holds an agent: the gateway denies its calls (enforced) and the
// agent holds its activities (graceful).
func (a *Admin) Pause(name, by string, on bool) error {
	if err := checkName(name); err != nil {
		return err
	}
	d, err := a.readGateway(name)
	if err != nil {
		return err
	}
	d.Paused, d.PausedBy = on, ""
	if on {
		d.PausedBy = by + " " + a.Now().UTC().Format(time.RFC3339)
	}
	if err := a.writeGateway(name, d); err != nil {
		return err
	}
	path := "/v1/resume"
	if on {
		path = "/v1/pause"
	}
	if err := a.Control(filepath.Join(SocketDir, name, "api.sock"), path); err != nil {
		fmt.Fprintf(a.Out, "agent %s: the gateway denies its calls; the agent itself was not reached (%v)\n", name, err)
	}
	return nil
}

// StopAll is the emergency stop: the gateway denies every agent
// everything, and every agent unit and schedule stops.
func (a *Admin) StopAll(by string) error {
	b := fmt.Sprintf("# Emergency stop (agent-admin stop-all). Remove with agent-admin release-all.\nstopped: true\nby: %q\nat: %q\n", by, a.Now().UTC().Format(time.RFC3339))
	if err := writeFile(a.p(GatewayStop), []byte(b), 0o644); err != nil {
		return err
	}
	return a.Run(nil, "systemctl", "stop", "agent-schedule@*.timer", "agent@*.socket", "agent@*.service")
}

// ReleaseAll undoes StopAll: sockets and timers that are enabled start again.
func (a *Admin) ReleaseAll() error {
	if err := os.Remove(a.p(GatewayStop)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	names, err := a.List()
	if err != nil {
		return err
	}
	var units []string
	for _, n := range names {
		units = append(units, "agent@"+n+".socket")
		if cfg, err := config.Load(a.agentDir(n)); err == nil {
			for _, s := range cfg.Schedules {
				units = append(units, scheduleUnit(n, s.Name)+".timer")
			}
		}
	}
	if len(units) == 0 {
		return nil
	}
	return a.Run(nil, "systemctl", append([]string{"start"}, units...)...)
}

// List returns the agents' names (§11: an agent exists when its agent.yaml does).
func (a *Admin) List() ([]string, error) {
	m, err := filepath.Glob(a.p(filepath.Join(config.ConfigRoot, "*", "agent.yaml")))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range m {
		if n := filepath.Base(filepath.Dir(f)); config.ValidName(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func scheduleUnit(agent, sched string) string { return "agent-schedule@" + agent + ":" + sched }

// syncSchedules writes a timer drop-in per schedule and removes the
// ones of schedules that are gone.
func (a *Admin) syncSchedules(cfg *config.Config) error {
	want := map[string]bool{}
	for _, s := range cfg.Schedules {
		unit := scheduleUnit(cfg.Name, s.Name)
		want[unit] = true
		conf := fmt.Sprintf("# agent-admin: schedule %s of agent %s\n[Timer]\nOnCalendar=\nOnCalendar=%s\n", s.Name, cfg.Name, s.OnCalendar)
		if err := writeFile(a.p(filepath.Join(UnitDir, unit+".timer.d", "schedule.conf")), []byte(conf), 0o644); err != nil {
			return err
		}
	}
	old, _ := filepath.Glob(a.p(filepath.Join(UnitDir, "agent-schedule@"+cfg.Name+":*.timer.d")))
	for _, d := range old {
		unit := strings.TrimSuffix(filepath.Base(d), ".timer.d")
		if !want[unit] {
			a.Run(nil, "systemctl", "disable", "--now", unit+".timer")
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
	}
	a.Run(nil, "systemctl", "daemon-reload")
	for unit := range want {
		if err := a.Run(nil, "systemctl", "enable", "--now", unit+".timer"); err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes atomically.
func writeFile(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
