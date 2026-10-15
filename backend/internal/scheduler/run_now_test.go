package scheduler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/testdb"
)

func TestRunNowEnqueuesOneJobPerRegionWithoutRescheduling(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, seed.SearchID, "londrina-pr")
	s := newScheduler(pool)

	var before time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT next_run_at FROM search_definitions WHERE id = $1`, seed.SearchID).Scan(&before); err != nil {
		t.Fatal(err)
	}

	n, err := s.RunNow(context.Background(), seed.SearchID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("enqueued %d, want 2", n)
	}

	var after time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT next_run_at FROM search_definitions WHERE id = $1`, seed.SearchID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("next_run_at changed: before=%v after=%v", before, after)
	}

	var jobCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM jobs WHERE search_id = $1`, seed.SearchID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 2 {
		t.Fatalf("jobs in db = %d, want 2", jobCount)
	}
}

func TestRunNowSkipsRegionsWithAlreadyQueuedJobs(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	s := newScheduler(pool)

	if n, err := s.RunNow(context.Background(), seed.SearchID); err != nil || n != 1 {
		t.Fatalf("first call: n=%d err=%v, want 1, nil", n, err)
	}
	n, err := s.RunNow(context.Background(), seed.SearchID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second call enqueued %d, want 0 (already queued)", n)
	}
}

func TestRunNowReturnsZeroForSearchWithNoLinkedRegions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO search_definitions (name, keyword, frequency, next_run_at, filters, alert)
		VALUES ('Sem regiao', 'Teste', '30 minutes', '2000-01-01T00:00:00Z', '{}', '{}')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	s := newScheduler(pool)

	n, err := s.RunNow(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("enqueued %d for a search with no regions, want 0", n)
	}
}

func TestRunNowReturnsErrSearchNotFoundForUnknownID(t *testing.T) {
	pool := testdb.New(t)
	s := newScheduler(pool)

	_, err := s.RunNow(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, scheduler.ErrSearchNotFound) {
		t.Fatalf("err = %v, want ErrSearchNotFound", err)
	}
}

func TestRunNowWorksEvenWhenSearchIsDisabled(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	if _, err := pool.Exec(context.Background(),
		`UPDATE search_definitions SET enabled = false WHERE id = $1`, seed.SearchID); err != nil {
		t.Fatal(err)
	}
	s := newScheduler(pool)

	n, err := s.RunNow(context.Background(), seed.SearchID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("enqueued %d for a disabled search, want 1 (manual run bypasses enabled)", n)
	}
}
