package config

import (
	"strings"
	"testing"
)

// A key typed where a credential name goes is told apart from a name,
// and the message never repeats it.
func TestLooksLikeSecret(t *testing.T) {
	for _, s := range []string{"sk-litellm-local", "sk-proj-abc", "hf_abcdef", "gsk_x", "xai-1", "nvapi-x",
		"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"} {
		if !LooksLikeSecret(s) {
			t.Errorf("%q: not taken for a key", s)
		}
	}
	for _, s := range []string{"model-key", "litellm", "openai-key", "gemini-api-key-2026", "skeleton-key",
		"a-rather-long-credential-name-for-the-production-model"} {
		if LooksLikeSecret(s) {
			t.Errorf("%q: taken for a key", s)
		}
	}
	// Not a valid name (upper case), and a key: refused without its value.
	err := Model{Provider: "openai", Name: "m", Endpoint: "http://ai/v1", Credential: "sk-ABCdef123"}.Check()
	if err == nil || !strings.Contains(err.Error(), "looks like a key") || strings.Contains(err.Error(), "ABCdef") {
		t.Errorf("upper-case key: %v", err)
	}
}

// A schedule runs a prompt or a playbook (§14c).
func TestSchedulePlaybook(t *testing.T) {
	base := "version: 1\nname: dw\nmodel: {provider: openai, name: m, endpoint: \"http://ai/v1\"}\ngateway: {servers: [systemd]}\n"
	if _, err := Parse([]byte(base + "schedules: [{name: daily, on_calendar: daily, playbook: failed-units}]\n")); err != nil {
		t.Errorf("a playbook without a prompt: %v", err)
	}
	for _, bad := range []struct{ in, want string }{
		{"schedules: [{name: daily, on_calendar: daily}]\n", "prompt: missing (or name a playbook)"},
		{"schedules: [{name: daily, on_calendar: daily, playbook: ../x}]\n", "not a playbook name"},
	} {
		if _, err := Parse([]byte(base + bad.in)); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%q: %v", bad.in, err)
		}
	}
}
