package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// HistoryFile is where an agent keeps its ended activities, in its state
// directory (/var/lib/agents/<name>): the launcher exits when idle and
// starts again on the next request, and the activities that ended
// before, with their reports, are still listed (CONTRACT.md §5).
const HistoryFile = "activities.json"

// HistoryAge is how long an ended activity is kept.
const HistoryAge = 30 * 24 * time.Hour

// Keep loads the ended activities kept at path and keeps them there from
// now on. A file that does not exist is none; one that does not parse is
// set aside (path.bad) and the history starts empty, so that the agent
// still starts.
func (g *Governor) Keep(path string) error {
	b, err := os.ReadFile(path)
	var kept []*Activity
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("activities: %w", err)
	default:
		if json.Unmarshal(b, &kept) != nil {
			os.Rename(path, path+".bad")
			kept = nil
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.historyPath = path
	g.ended = append(trimHistory(kept, g.now()), g.ended...)
	if len(g.ended) > History {
		g.ended = g.ended[len(g.ended)-History:]
	}
	return nil
}

// trimHistory drops what ended more than HistoryAge ago, and entries
// without an end.
func trimHistory(as []*Activity, now time.Time) []*Activity {
	var out []*Activity
	for _, a := range as {
		if a != nil && a.Ended != nil && now.Sub(*a.Ended) <= HistoryAge {
			out = append(out, a)
		}
	}
	return out
}

// saveHistory writes the ended activities, replacing the file at once
// (a new file renamed over the old); g.mu must be held. A failure is
// logged, not fatal: the activities are still in memory.
func (g *Governor) saveHistory() {
	if g.historyPath == "" {
		return
	}
	g.ended = trimHistory(g.ended, g.now())
	b, err := json.Marshal(g.ended)
	if err == nil {
		tmp := filepath.Join(filepath.Dir(g.historyPath), "."+filepath.Base(g.historyPath)+".new")
		if err = os.WriteFile(tmp, b, 0o600); err == nil {
			err = os.Rename(tmp, g.historyPath)
		}
	}
	if err != nil {
		g.historyErr = err.Error()
	}
}
