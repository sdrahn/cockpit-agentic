// Package units checks the shipped systemd units against what the rest of
// Custos relies on.
package units

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// settings reads a unit file's assignments, in order (key → values).
func settings(t *testing.T, path string) map[string][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string][]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[k] = append(out[k], v)
		}
	}
	return out
}

func has(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// An agent's credentials, sealed to the TPM on a host that has one, are
// decrypted under the unit's device policy: PrivateDevices= needs the TPM
// allowed, or the unit fails at step CREDENTIALS ("Protocol error").
func TestAgentCredentialsReachTheTPM(t *testing.T) {
	u := settings(t, "../../systemd/agent@.service")
	if !has(u["ImportCredential"], "agent-%i.*") {
		t.Fatalf("ImportCredential: %v", u["ImportCredential"])
	}
	if has(u["PrivateDevices"], "yes") && !has(u["DeviceAllow"], "/dev/tpmrm0 rw") {
		t.Error("PrivateDevices=yes without DeviceAllow=/dev/tpmrm0 rw: TPM-sealed credentials cannot be decrypted")
	}
}

// agent-admin creates /etc/custos/agents/<name> as 0750; the unit says the
// same, or systemd warns at every start.
func TestAgentConfigurationDirectoryMode(t *testing.T) {
	u := settings(t, "../../systemd/agent@.service")
	if !has(u["ConfigurationDirectoryMode"], "0750") {
		t.Errorf("ConfigurationDirectoryMode: %v, want 0750 (agent-admin create)", u["ConfigurationDirectoryMode"])
	}
}
