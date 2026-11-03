// Package config reads and checks an agent's agent.yaml (CONTRACT.md §4).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ContractVersion is the contract version this code implements.
const ContractVersion = 1

// Default paths (CONTRACT.md §3, §6).
const (
	ConfigRoot    = "/etc/cockpit-agentic/agents"
	GatewaySocket = "/run/mcp-gateway/mcp.sock"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

var credRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidCredential reports whether s is a valid credential name.
func ValidCredential(s string) bool { return credRE.MatchString(s) }

// ValidName reports whether s is a valid agent name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

type Config struct {
	Version         int         `yaml:"version"`
	Name            string      `yaml:"name"`
	Description     string      `yaml:"description"`
	Model           Model       `yaml:"model"`
	Instruction     string      `yaml:"instruction"`
	InstructionFile string      `yaml:"instruction_file"`
	Gateway         Gateway     `yaml:"gateway"`
	Limits          Limits      `yaml:"limits"`
	Interaction     Interaction `yaml:"interaction"`
	Sessions        Sessions    `yaml:"sessions"`
	Schedules       []Schedule  `yaml:"schedules"`

	// Dir is the directory the file was read from (not in the file).
	Dir string `yaml:"-"`
}

type Model struct {
	Provider    string   `yaml:"provider"`
	Name        string   `yaml:"name"`
	Endpoint    string   `yaml:"endpoint"`
	Credential  string   `yaml:"credential"`
	Temperature *float32 `yaml:"temperature"`
}

type Gateway struct {
	Socket  string   `yaml:"socket"`
	Servers []string `yaml:"servers"`
	Tools   []string `yaml:"tools"`
}

type Limits struct {
	MaxLLMCalls          int `yaml:"max_llm_calls" json:"max_llm_calls"`
	MaxToolCalls         int `yaml:"max_tool_calls" json:"max_tool_calls"`
	MaxInvocationSeconds int `yaml:"max_invocation_seconds" json:"max_invocation_seconds"`
}

// MaxInvocation is the invocation deadline.
func (l Limits) MaxInvocation() time.Duration {
	return time.Duration(l.MaxInvocationSeconds) * time.Second
}

type Interaction struct {
	AGUI        bool        `yaml:"agui"`
	ClientTools ClientTools `yaml:"client_tools"`
}

// ClientTools is "deny" or {allow: [...]}.
type ClientTools struct {
	Allow []string
}

func (c *ClientTools) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if n.Value != "deny" {
			return fmt.Errorf("client_tools: %q: want deny or {allow: [...]}", n.Value)
		}
		c.Allow = nil
		return nil
	}
	var v struct {
		Allow []string `yaml:"allow"`
	}
	if err := n.Decode(&v); err != nil {
		return fmt.Errorf("client_tools: %w", err)
	}
	c.Allow = v.Allow
	return nil
}

// Allowed reports whether the frontend tool name may be offered.
func (c ClientTools) Allowed(name string) bool {
	for _, a := range c.Allow {
		if a == name {
			return true
		}
	}
	return false
}

// Schedule is a recurring activity, run by a systemd timer
// (agent-schedule@<name>:<schedule>.timer, CONTRACT.md §11).
type Schedule struct {
	Name       string `yaml:"name" json:"name"`
	OnCalendar string `yaml:"on_calendar" json:"on_calendar"`
	Prompt     string `yaml:"prompt" json:"prompt"`
}

var scheduleRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

type Sessions struct {
	RetentionDays int `yaml:"retention_days"`
}

// Load reads <dir>/agent.yaml and checks it.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, "agent.yaml"))
	if err != nil {
		return nil, err
	}
	c, err := Parse(b)
	if err != nil {
		return nil, err
	}
	c.Dir = dir
	if base := filepath.Base(dir); base != c.Name {
		return nil, fmt.Errorf("name %q does not match the directory %q", c.Name, base)
	}
	if c.InstructionFile != "" {
		p := c.InstructionFile
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		ib, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("instruction_file: %w", err)
		}
		c.Instruction = string(ib)
	}
	return c, nil
}

// Parse decodes and checks a configuration, filling in defaults.
// Unknown keys are errors, so a typo does not silently drop a limit.
func Parse(b []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("agent.yaml: %w", err)
	}
	c.defaults()
	if err := c.Check(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) defaults() {
	if c.Gateway.Socket == "" {
		c.Gateway.Socket = GatewaySocket
	}
	if c.Limits.MaxLLMCalls == 0 {
		c.Limits.MaxLLMCalls = 25
	}
	if c.Limits.MaxToolCalls == 0 {
		c.Limits.MaxToolCalls = 40
	}
	if c.Limits.MaxInvocationSeconds == 0 {
		c.Limits.MaxInvocationSeconds = 900
	}
	if c.Sessions.RetentionDays == 0 {
		c.Sessions.RetentionDays = 30
	}
}

// Check returns all problems at once, so an editor can show them together.
func (c *Config) Check() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	if c.Version != ContractVersion {
		bad("version: %d: this launcher implements version %d", c.Version, ContractVersion)
	}
	if !ValidName(c.Name) {
		bad("name: %q: want lower-case letters, digits and '-', 2-32 characters, starting with a letter", c.Name)
	}
	switch c.Model.Provider {
	case "gemini", "vertex":
		if c.Model.Endpoint != "" {
			bad("model.endpoint: only for provider openai")
		}
	case "openai":
		if !strings.HasPrefix(c.Model.Endpoint, "https://") && !strings.HasPrefix(c.Model.Endpoint, "http://") {
			bad("model.endpoint: %q: want an http(s) URL", c.Model.Endpoint)
		}
	case "":
		bad("model.provider: missing")
	default:
		bad("model.provider: %q: want gemini, vertex or openai", c.Model.Provider)
	}
	if c.Model.Name == "" {
		bad("model.name: missing")
	}
	if c.Model.Provider == "gemini" && c.Model.Credential == "" {
		bad("model.credential: provider gemini needs an API key credential")
	}
	if c.Model.Credential != "" && !ValidCredential(c.Model.Credential) {
		bad("model.credential: %q: want a credential name such as model-key", c.Model.Credential)
	}
	if c.Instruction != "" && c.InstructionFile != "" {
		bad("instruction and instruction_file: set one of them")
	}
	if !filepath.IsAbs(c.Gateway.Socket) {
		bad("gateway.socket: %q: want an absolute path", c.Gateway.Socket)
	}
	if len(c.Gateway.Servers) == 0 {
		bad("gateway.servers: name at least one gateway server")
	}
	for _, t := range c.Gateway.Tools {
		srv, _, ok := strings.Cut(t, "__")
		if !ok {
			bad("gateway.tools: %q: want <server>__<tool>", t)
		} else if !contains(c.Gateway.Servers, srv) {
			bad("gateway.tools: %q: server %q is not in gateway.servers", t, srv)
		}
	}
	for k, v := range map[string]int{"max_llm_calls": c.Limits.MaxLLMCalls, "max_tool_calls": c.Limits.MaxToolCalls, "max_invocation_seconds": c.Limits.MaxInvocationSeconds} {
		if v < 0 {
			bad("limits.%s: %d: must be positive", k, v)
		}
	}
	seen := map[string]bool{}
	for i, s := range c.Schedules {
		if !scheduleRE.MatchString(s.Name) || seen[s.Name] {
			bad("schedules[%d].name: %q: want a unique lower-case name", i, s.Name)
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.OnCalendar) == "" || strings.ContainsAny(s.OnCalendar, "\n\r") {
			bad("schedules[%d].on_calendar: want a systemd calendar expression (systemd.time(7))", i)
		}
		if strings.TrimSpace(s.Prompt) == "" {
			bad("schedules[%d].prompt: missing", i)
		}
	}
	if c.Sessions.RetentionDays < 0 {
		bad("sessions.retention_days: must be positive")
	}
	return errors.Join(errs...)
}

// ShowsTool reports whether a gateway tool (<server>__<tool>) is offered
// to the model. This is a convenience, not a security boundary (§4).
func (c *Config) ShowsTool(name string) bool {
	srv, _, ok := strings.Cut(name, "__")
	if !ok || !contains(c.Gateway.Servers, srv) {
		return false
	}
	return len(c.Gateway.Tools) == 0 || contains(c.Gateway.Tools, name)
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
