package sweep_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/sweep"
	"scrapperbook/backend/internal/testdb"
)

func insertRawCapture(t *testing.T, pool *pgxpool.Pool, jobID, externalID string, capturedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO raw_captures
		(job_id, source, external_id, crawler_version, parser_version, payload, captured_at)
		VALUES ($1, 'facebook_marketplace', $2, 'test', 'test', '{}', $3)`,
		jobID, externalID, capturedAt); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeOldRawCapturesDeletesOnlyStale(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	jobID := testdb.InsertRunningJob(t, pool, seed)
	insertRawCapture(t, pool, jobID, "old-1", now.Add(-20*24*time.Hour))
	insertRawCapture(t, pool, jobID, "new-1", now.Add(-1*time.Hour))

	n, err := sweep.PurgeOldRawCaptures(context.Background(), pool, now, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}
	var remaining int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM raw_captures`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
}
