package admin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modelYAML(cred string) string {
	return strings.Replace(yml, `endpoint: "http://ai:8000/v1"}`, `endpoint: "http://ai:8000/v1", credential: `+cred+`}`, 1)
}

// A key given as model.credential is refused, without repeating it, by
// check, create, edit and defaults: the agent would not start, and the key
// would sit in a file others read.
func TestKeyAsCredentialName(t *testing.T) {
	a, _ := newAdmin(t)
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(modelYAML("sk-litellm-local")), 0o644)
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "looks like a key") || strings.Contains(err.Error(), "litellm") {
			t.Errorf("%s: %v", what, err)
		}
	}
	refused("check", a.Check(f))
	refused("create", a.Create("patch-pilot", f))
	if _, err := os.Stat(a.agentDir("patch-pilot")); !os.IsNotExist(err) {
		t.Fatal("created all the same")
	}
	refused("defaults", a.SetDefaults([]byte("model: {provider: openai, name: m, endpoint: \"http://ai/v1\", credential: sk-litellm-local}\n")))
	if _, err := os.Stat(a.p(DefaultsFile)); !os.IsNotExist(err) {
		t.Error("defaults written all the same")
	}

	ok := filepath.Join(t.TempDir(), "ok.yaml")
	os.WriteFile(ok, []byte(modelYAML("model-key")), 0o644)
	if err := a.Create("patch-pilot", ok); err != nil {
		t.Fatal(err)
	}
	refused("edit", a.Edit("patch-pilot", f))

	// Stored under that name, it is a name after all (odd, but it works).
	os.MkdirAll(a.p(CredstoreDir), 0o700)
	os.WriteFile(a.p(filepath.Join(CredstoreDir, "agent-patch-pilot.sk-litellm-local")), []byte("x"), 0o600)
	if err := a.Edit("patch-pilot", f); err != nil {
		t.Errorf("edit with the credential stored: %v", err)
	}
	if err := a.Check(f); err != nil {
		t.Errorf("check with the credential stored: %v", err)
	}
}

// After create and edit, agent-admin says when the model's credential is
// not stored yet, with the command that stores it; not once it is.
func TestNoteUnstoredCredential(t *testing.T) {
	a, _ := newAdmin(t)
	var out bytes.Buffer
	a.Out = &out
	f := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(f, []byte(modelYAML("model-key")), 0o644)
	if err := a.Create("patch-pilot", f); err != nil {
		t.Fatal(err)
	}
	want := "agent-admin set-credential patch-pilot model-key"
	if !strings.Contains(out.String(), "model-key is not stored yet") || !strings.Contains(out.String(), want) {
		t.Errorf("create says %q", out.String())
	}
	out.Reset()
	os.MkdirAll(a.p(CredstoreDir), 0o700)
	os.WriteFile(a.p(filepath.Join(CredstoreDir, "agent-patch-pilot.model-key")), []byte("x"), 0o600)
	if err := a.Edit("patch-pilot", f); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "not stored") {
		t.Errorf("edit with the credential stored says %q", out.String())
	}
	out.Reset()
	os.WriteFile(f, []byte(yml), 0o644) // no credential: nothing to say
	if err := a.Edit("patch-pilot", f); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "credential") {
		t.Errorf("edit without a credential says %q", out.String())
	}
}
