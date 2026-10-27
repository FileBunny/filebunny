package cleanup

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/store"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestOnceRemovesExpired(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

	old, _, _ := st.Put(ctx, strings.NewReader("old"), now, now.Add(time.Minute), 0)
	fresh, _, _ := st.Put(ctx, strings.NewReader("fresh"), now, now.Add(time.Hour), 0)

	Once(ctx, st, now.Add(2*time.Minute), discard)

	// Query far in the past so only deletion, not expiry, hides a file.
	if _, _, err := st.Get(ctx, old.ID, now); err != store.ErrNotFound {
		t.Errorf("expired file still present: %v", err)
	}
	_, blob, err := st.Get(ctx, fresh.ID, now)
	if err != nil {
		t.Fatalf("fresh file removed: %v", err)
	}
	blob.Close()
}

type countingStore struct{ calls atomic.Int32 }

func (c *countingStore) DeleteExpired(context.Context, time.Time) (int, error) {
	c.calls.Add(1)
	return 0, nil
}

func TestRunTicksUntilCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := &countingStore{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			Run(ctx, st, time.Minute, discard)
			close(done)
		}()

		time.Sleep(3*time.Minute + time.Second)
		synctest.Wait()
		if got := st.calls.Load(); got != 4 { // immediately + 3 ticks
			t.Errorf("calls = %d, want 4", got)
		}
		cancel()
		<-done
	})
}
