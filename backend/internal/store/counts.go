package store

import (
	"context"
	"time"
)

// Daily totals for the operator's monitoring (deploy/ansible reads them
// from the database): how many uploads and downloads per UTC day, nothing
// per file, visitor or time of day, so they reveal nothing about a single
// transfer. Rows older than countsKeep are removed by DeleteExpired.

type counter string

const (
	countUploads        counter = "uploads"         // share links (stored here or externally)
	countInboxUploads   counter = "inbox_uploads"   // to upload links
	countDownloads      counter = "downloads"       // through share links
	countInboxDownloads counter = "inbox_downloads" // fetched by an upload link's owner
)

const countsKeep = 400 * 24 * time.Hour

func countDay(t time.Time) string { return t.UTC().Format("2006-01-02") }

// count adds one to today's total of c. Best effort: a failed count never
// fails the upload or download it counts. c is one of the constants above,
// never input.
func (s *Store) count(ctx context.Context, now time.Time, c counter) {
	col := string(c)
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx),
		`INSERT INTO daily_counts (day, `+col+`) VALUES (?, 1)
		 ON CONFLICT (day) DO UPDATE SET `+col+` = `+col+` + 1`, countDay(now))
}

// DailyCount is one day's totals.
type DailyCount struct {
	Day                                              string
	Uploads, InboxUploads, Downloads, InboxDownloads int64
}

// DailyCounts returns the totals from the day of since on, oldest first.
func (s *Store) DailyCounts(ctx context.Context, since time.Time) ([]DailyCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT day, uploads, inbox_uploads, downloads, inbox_downloads FROM daily_counts WHERE day >= ? ORDER BY day`,
		countDay(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyCount
	for rows.Next() {
		var d DailyCount
		if err := rows.Scan(&d.Day, &d.Uploads, &d.InboxUploads, &d.Downloads, &d.InboxDownloads); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
