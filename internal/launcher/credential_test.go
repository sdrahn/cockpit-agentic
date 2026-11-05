package launcher

import (
	"context"
	"strings"
	"testing"

	"github.com/sdrahn/custos/internal/config"
)

// A model credential that is not stored says how to store it; one that
// looks like the key itself says so, without repeating it.
func TestModelCredentialMissing(t *testing.T) {
	cfg := &config.Config{Name: "dw", Model: config.Model{Provider: "openai", Name: "m", Endpoint: "http://ai/v1", Credential: "model-key"}}
	_, err := newModel(context.Background(), cfg, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "model.credential model-key is not stored for agent dw: agent-admin set-credential dw model-key") {
		t.Errorf("missing: %v", err)
	}
	// No credentials directory: systemd imported nothing.
	if _, err := newModel(context.Background(), cfg, ""); err == nil || !strings.Contains(err.Error(), "agent-admin set-credential dw model-key") {
		t.Errorf("no credentials directory: %v", err)
	}
	cfg.Model.Credential = "sk-litellm-local"
	_, err = newModel(context.Background(), cfg, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "looks like a key") || strings.Contains(err.Error(), "litellm") {
		t.Errorf("key as name: %v", err)
	}
}
