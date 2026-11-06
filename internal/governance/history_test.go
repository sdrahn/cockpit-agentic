package governance

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sdrahn/custos/internal/config"
	"github.com/sdrahn/custos/internal/journal"
)

func governor(now time.Time) *Governor {
	g := New(&config.Config{Name: "health-check"}, &journal.Memory{})
	g.now = func() time.Time { return now }
	return g
}

// Ended activities and their reports outlive the process: an agent that
// exited when idle lists them again when it starts.
func TestHistoryKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), HistoryFile)
	now := time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	g := governor(now)
	if err := g.Keep(path); err != nil {
		t.Fatal(err)
	}
	old, recent := now.Add(-31*24*time.Hour), now.Add(-time.Hour)
	g.mu.Lock()
	g.ended = append(g.ended,
		&Activity{ID: "e-old", State: StateFinished, Started: old, Ended: &old},
		&Activity{ID: "e-1", State: StateFinished, Started: recent, Ended: &recent, Playbook: "boot-health",
			Report: &Report{Summary: "The last boot took 1 min; plymouth-quit-wait waited 62 s."}})
	g.saveHistory()
	g.mu.Unlock()
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("history file: %v %v", fi, err)
	}

	again := governor(now)
	if err := again.Keep(path); err != nil {
		t.Fatal(err)
	}
	as := again.Activities()
	if len(as) != 1 || as[0].ID != "e-1" || as[0].Playbook != "boot-health" || as[0].Report == nil || as[0].Report.Summary == "" {
		t.Fatalf("after a restart: %+v", as)
	}
	if a, ok := again.Activity("e-1"); !ok || a.Ended == nil {
		t.Errorf("activity e-1: %+v %v", a, ok)
	}
}

// A file that does not parse is set aside; the agent still starts.
func TestHistoryUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), HistoryFile)
	os.WriteFile(path, []byte("{not json"), 0o600)
	g := governor(time.Now())
	if err := g.Keep(path); err != nil || len(g.Activities()) != 0 {
		t.Fatalf("%v %v", err, g.Activities())
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Errorf("not set aside: %v", err)
	}
}
