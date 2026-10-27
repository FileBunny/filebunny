// Package cleanup periodically removes expired files.
package cleanup

import (
	"context"
	"log/slog"
	"time"
)

// Store is the part of store.Store that cleanup needs.
type Store interface {
	DeleteExpired(ctx context.Context, now time.Time) (int, error)
}

// Run removes expired files once immediately and then every interval, until
// ctx is cancelled.
func Run(ctx context.Context, st Store, interval time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		Once(ctx, st, time.Now(), log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Once removes the files that expired at or before now.
func Once(ctx context.Context, st Store, now time.Time, log *slog.Logger) {
	n, err := st.DeleteExpired(ctx, now)
	if err != nil && ctx.Err() == nil {
		log.Error("cleanup failed", "err", err, "removed", n)
		return
	}
	if n > 0 {
		log.Info("removed expired files", "count", n)
	}
}
