// Package openshell turns an agent's configuration into what NVIDIA
// OpenShell needs to run it (CONTRACT.md §3a, §16a): the sandbox policy,
// the provider profile for mcp-gateway's token, and the arguments of
// `openshell sandbox create`.
//
// The agent may reach two places: mcp-gateway's HTTPS listener (with the
// token the supervisor adds) and its model endpoint. Everything else is
// denied by OpenShell's default. Services on the host are reached as
// host.openshell.internal.
package openshell

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/sdrahn/cockpit-agentic/internal/config"
)

// Paths inside the sandbox image (packaging/openshell/Containerfile).
const (
	Launcher  = "/usr/libexec/cockpit-agentic/agent-launcher"
	Workdir   = "/sandbox"
	StateDir  = "/sandbox/state"
	TokenEnv  = "AGENT_GATEWAY_TOKEN"
	SecretEnv = "AGENTIC_RELAY_SECRET"
	ConfigEnv = "AGENTIC_CONFIG"
)

// Port is the agent's control API port: inside the sandbox (loopback)
// and on the host, where `openshell forward` makes it reachable to the
// relay. Stable per name, in 47000–47999.
func Port(agent string) int {
	h := uint32(2166136261)
	for i := 0; i < len(agent); i++ {
		h = (h ^ uint32(agent[i])) * 16777619
	}
	return 47000 + int(h%1000)
}

// HostAlias is the name a sandbox reaches the host by.
const HostAlias = "host.openshell.internal"

// Names of an agent's OpenShell objects.
func SandboxName(agent string) string  { return "agent-" + agent }
func ProviderName(agent string) string { return "agent-" + agent + "-gateway" }
func ProfileID(agent string) string    { return "cockpit-agentic-gateway-" + agent }

type endpoint struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Path        string `yaml:"path,omitempty"`
	Protocol    string `yaml:"protocol"`
	Enforcement string `yaml:"enforcement"`
	Access      string `yaml:"access,omitempty"`
	Credential  *struct {
		Provider string `yaml:"provider"`
	} `yaml:"credential_binding,omitempty"`
}

type rule struct {
	Name      string     `yaml:"name"`
	Endpoints []endpoint `yaml:"endpoints"`
	Binaries  []binary   `yaml:"binaries"`
}

type binary struct {
	Path string `yaml:"path"`
}

// Policy is the agent's sandbox policy (OpenShell policy schema v1).
// The agent runs as OpenShell's unprivileged sandbox user, which owns the
// sandbox's working directory (where its state lives).
func Policy(cfg *config.Config) ([]byte, error) {
	gw, err := hostPort(cfg.Gateway.URL)
	if err != nil {
		return nil, fmt.Errorf("gateway.url: %w", err)
	}
	// Host and port only: whether a path glob such as /mcp/** matches /mcp
	// itself is not settled in OpenShell's documentation.
	gwEP := endpoint{Host: gw.host, Port: gw.port, Protocol: "mcp", Enforcement: "enforce", Access: "full"}
	gwEP.Credential = &struct {
		Provider string `yaml:"provider"`
	}{ProviderName(cfg.Name)}
	rules := map[string]rule{
		"mcp_gateway": {Name: "mcp-gateway", Endpoints: []endpoint{gwEP}, Binaries: []binary{{Launcher}}},
	}
	if cfg.Model.Provider == "openai" {
		m, err := hostPort(cfg.Model.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("model.endpoint: %w", err)
		}
		rules["model"] = rule{Name: "model", Endpoints: []endpoint{{Host: m.host, Port: m.port, Protocol: "rest", Enforcement: "enforce", Access: "full"}}, Binaries: []binary{{Launcher}}}
	} else {
		rules["model"] = rule{Name: "model", Endpoints: []endpoint{
			{Host: "generativelanguage.googleapis.com", Port: 443, Protocol: "rest", Enforcement: "enforce", Access: "full"},
			{Host: "*-aiplatform.googleapis.com", Port: 443, Protocol: "rest", Enforcement: "enforce", Access: "full"},
		}, Binaries: []binary{{Launcher}}}
	}
	p := map[string]any{
		"version": 1,
		"filesystem_policy": map[string]any{
			"include_workdir": true,
			"read_only":       []string{"/usr", "/lib", "/lib64", "/etc"},
		},
		"landlock":         map[string]any{"compatibility": "hard_requirement"},
		"process":          map[string]any{"run_as_user": "sandbox", "run_as_group": "sandbox"},
		"network_policies": rules,
	}
	return yaml.Marshal(p)
}

// Profile is the provider profile that binds the gateway token to the
// gateway's endpoint only.
func Profile(cfg *config.Config) ([]byte, error) {
	gw, err := hostPort(cfg.Gateway.URL)
	if err != nil {
		return nil, fmt.Errorf("gateway.url: %w", err)
	}
	return yaml.Marshal(map[string]any{
		"id":           ProfileID(cfg.Name),
		"display_name": "mcp-gateway for agent " + cfg.Name,
		"description":  "Bearer token of agent-" + cfg.Name + " for mcp-gateway (cockpit-agentic)",
		"category":     "data",
		"credentials": []map[string]any{{
			"name": "gateway_token", "description": "OAuth access token whose subject is agent-" + cfg.Name,
			"env_vars": []string{TokenEnv}, "required": true, "auth_style": "bearer", "header_name": "authorization",
		}},
		"endpoints": []map[string]any{{"host": gw.host, "port": gw.port, "protocol": "mcp", "enforcement": "enforce"}},
		"binaries":  []string{Launcher},
	})
}

// SandboxConfig is the agent's configuration as the sandbox gets it: the
// instruction inline, the gateway by URL with the token variable.
func SandboxConfig(cfg *config.Config) ([]byte, error) {
	c := *cfg
	c.InstructionFile = ""
	c.Gateway.Socket = ""
	c.Gateway.TokenEnv = TokenEnv
	// The host's loopback is the sandbox's host.openshell.internal.
	c.Gateway.URL = hostURL(c.Gateway.URL)
	if c.Model.Endpoint != "" {
		c.Model.Endpoint = hostURL(c.Model.Endpoint)
	}
	return yaml.Marshal(&c)
}

// CreateArgs are the arguments of `openshell` that create the sandbox.
func CreateArgs(cfg *config.Config, image, policyFile, secret string, port int) ([]string, error) {
	sc, err := SandboxConfig(cfg)
	if err != nil {
		return nil, err
	}
	return []string{
		"sandbox", "create", "--name", SandboxName(cfg.Name), "--from", image,
		"--policy", policyFile, "--provider", ProviderName(cfg.Name),
		"--env", SecretEnv + "=" + secret,
		"--env", ConfigEnv + "=" + base64.StdEncoding.EncodeToString(sc),
		"--env", "STATE_DIRECTORY=" + StateDir,
		"--detach", "--",
		Launcher, "serve", "--config-env", ConfigEnv, "--listen-tcp", fmt.Sprintf("127.0.0.1:%d", port), "--idle-exit", "0",
	}, nil
}

// hostURL replaces a loopback host by the sandbox's name for the host.
func hostURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	h := u.Hostname()
	if h == "localhost" || (net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback()) {
		if p := u.Port(); p != "" {
			u.Host = net.JoinHostPort(HostAlias, p)
		} else {
			u.Host = HostAlias
		}
	}
	return u.String()
}

type hp struct {
	host, path string
	port       int
}

// hostPort reads a URL as OpenShell endpoint fields; a loopback host is
// the sandbox host's (host.openshell.internal).
func hostPort(raw string) (hp, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return hp{}, fmt.Errorf("%q is not a URL", raw)
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return hp{}, err
		}
	}
	h := u.Hostname()
	if h == "localhost" || (net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback()) {
		h = HostAlias
	}
	path := u.EscapedPath()
	if path == "/" {
		path = ""
	}
	return hp{host: h, port: port, path: path}, nil
}
