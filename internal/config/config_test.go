package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
version: 1
name: patch-pilot
model: {provider: openai, name: qwen3-32b, endpoint: "https://ai.example/v1"}
instruction: "Keep the host patched."
gateway: {servers: [zypp, systemd], tools: [zypp__list_updates]}
interaction: {agui: true, client_tools: {allow: [confirm_window]}}
`

func TestParse(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Gateway.Socket != GatewaySocket || c.Limits.MaxToolCalls != 40 || c.Sessions.RetentionDays != 30 {
		t.Errorf("defaults not applied: %+v", c)
	}
	if !c.Interaction.ClientTools.Allowed("confirm_window") || c.Interaction.ClientTools.Allowed("x") {
		t.Error("client_tools allow list")
	}
	if !c.ShowsTool("zypp__list_updates") || c.ShowsTool("zypp__install") || c.ShowsTool("fs__read") {
		t.Error("ShowsTool")
	}
}

func TestParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"version":      {"version: 2\nname: ab\nmodel: {provider: vertex, name: m}\ngateway: {servers: [a]}", "version: 2"},
		"name":         {"version: 1\nname: Bad\nmodel: {provider: vertex, name: m}\ngateway: {servers: [a]}", "name:"},
		"unknown key":  {"version: 1\nname: ab\nmodel: {provider: vertex, name: m}\ngateway: {servers: [a]}\nlimitz: {}", "limitz"},
		"no servers":   {"version: 1\nname: ab\nmodel: {provider: vertex, name: m}", "gateway.servers"},
		"tool server":  {"version: 1\nname: ab\nmodel: {provider: vertex, name: m}\ngateway: {servers: [a], tools: [b__x]}", "not in gateway.servers"},
		"gemini key":   {"version: 1\nname: ab\nmodel: {provider: gemini, name: m}\ngateway: {servers: [a]}", "API key"},
		"client tools": {"version: 1\nname: ab\nmodel: {provider: vertex, name: m}\ngateway: {servers: [a]}\ninteraction: {client_tools: allow}", "client_tools"},
	} {
		_, err := Parse([]byte(tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "patch-pilot")
	os.Mkdir(dir, 0o755)
	y := strings.Replace(sample, `instruction: "Keep the host patched."`, "instruction_file: instruction.md", 1)
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(y), 0o644)
	os.WriteFile(filepath.Join(dir, "instruction.md"), []byte("Be careful."), 0o644)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Instruction != "Be careful." {
		t.Errorf("instruction = %q", c.Instruction)
	}
	other := filepath.Join(filepath.Dir(dir), "other")
	os.Rename(dir, other)
	if _, err := Load(other); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("name/dir mismatch: %v", err)
	}
}

func TestSandbox(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if l := c.Lifecycle(); l["kind"] != "systemd" || l["service"] != "agent@patch-pilot.service" || l["socket"] != "agent@patch-pilot.socket" {
		t.Errorf("default lifecycle %v", l)
	}
	osh := strings.Replace(sample, "gateway: {servers: [zypp, systemd], tools: [zypp__list_updates]}", "gateway: {url: \"https://host.openshell.internal:8443/mcp\", token_env: AGENT_GATEWAY_TOKEN, servers: [zypp]}", 1)
	c, err = Parse([]byte(osh + "sandbox: {kind: openshell}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if l := c.Lifecycle(); l["kind"] != "openshell" || l["sandbox"] != "agent-patch-pilot" || l["gateway"] != "openshell" || l["service"] != "agent-openshell@patch-pilot.service" {
		t.Errorf("openshell lifecycle %v", l)
	}
	for _, bad := range []string{"sandbox: {kind: docker}\n", "sandbox: {kind: systemd, openshell_gateway: x}\n"} {
		if _, err := Parse([]byte(sample + bad)); err == nil || !strings.Contains(err.Error(), "sandbox.") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestGatewayURL(t *testing.T) {
	base := "version: 1\nname: ab\nmodel: {provider: vertex, name: m}\n"
	for gw, want := range map[string]string{
		"gateway: {servers: [a], url: \"https://gw:8443/mcp\"}":                            "",
		"gateway: {servers: [a], url: \"http://127.0.0.1:8443/mcp\", token_env: GW_TOKEN}": "",
		"gateway: {servers: [a], url: \"http://gw:8443/mcp\"}":                             "want an https URL",
		"gateway: {servers: [a], url: \"https://gw/mcp\", socket: /run/x.sock}":            "not both",
		"gateway: {servers: [a], url: \"https://gw/mcp\", token_env: \"bad name\"}":        "environment variable",
		"gateway: {servers: [a], token_env: GW_TOKEN}":                                     "only with gateway.url",
		"gateway: {servers: [a]}\nsandbox: {kind: openshell}":                              "reaches the gateway over HTTPS",
	} {
		c, err := Parse([]byte(base + gw + "\n"))
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: %v", gw, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: %v, want %q", gw, err, want)
		case want == "" && c.Gateway.Socket != "":
			t.Errorf("%s: socket defaulted along with url", gw)
		}
	}
}
