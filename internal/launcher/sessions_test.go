package launcher

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/adk/session"
)

// Runs in parallel (a schedule's, an AG-UI client's) create sessions and
// append events at the same time: none may fail with SQLITE_BUSY.
func TestSessionsConcurrentWrites(t *testing.T) {
	svc, _, err := openSessions(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				r, err := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: fmt.Sprintf("s-%d-%d", g, i)})
				if err != nil {
					errs <- err
					continue
				}
				ev := session.NewEvent("inv")
				ev.Author = "user"
				if err := svc.AppendEvent(ctx, r.Session, ev); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	n := 0
	for err := range errs {
		if n == 0 {
			t.Errorf("concurrent session write: %v", err)
		}
		n++
	}
	if n > 0 {
		t.Errorf("%d of 400 writes failed", n)
	}
}
