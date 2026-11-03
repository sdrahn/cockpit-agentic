// Package vm holds the functional tests (run-tests.sh) and their helper
// programs; this test checks the script itself where no test system is.
package vm

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestScriptParses(t *testing.T) {
	if out, err := exec.Command("bash", "-n", "run-tests.sh").CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
}

func TestRefusesWithoutDisposable(t *testing.T) {
	for _, args := range [][]string{{}, {"--results", "/tmp/x"}, {"--bogus"}} {
		out, err := exec.Command("bash", append([]string{"run-tests.sh"}, args...)...).CombinedOutput()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 2 {
			t.Errorf("%v: %v (want exit status 2)\n%s", args, err, out)
		}
		if len(args) == 0 && !strings.Contains(string(out), "--disposable") {
			t.Errorf("no hint at --disposable: %s", out)
		}
	}
}
