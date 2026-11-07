package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The second host rests on the contract alone (#112): it imports nothing
// of Custos's but itself, only the standard library.
func TestImportsNoCustosCode(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if !strings.HasPrefix(p, "github.com/sdrahn/custos/test/vm/secondhost") {
			t.Errorf("the second host imports %s", p)
		}
	}
}
