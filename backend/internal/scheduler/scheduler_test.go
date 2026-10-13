package scheduler_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/testdb"
)

var now = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

func newScheduler(pool *pgxpool.Pool) *scheduler.Scheduler {
	q := jobs.New(pool, jobs.Limits{})
	q.Clock = func() time.Time { return now }
	s := scheduler.New(pool, q, []string{"facebook_marketplace"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Clock = func() time.Time { return now }
	return s
}

func tick(t *testing.T, s *scheduler.Scheduler) int {
	t.Helper()
	n, err := s.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTickEnqueuesOneJobPerRegionAndReschedules(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, seed.SearchID, "londrina-pr")
	s := newScheduler(pool)

	if n := tick(t, s); n != 2 {
		t.Fatalf("enqueued %d, want 2", n)
	}
	var payload map[string]any
	if err := pool.QueryRow(context.Background(), `SELECT payload FROM jobs
		WHERE region_id = 'curitiba-pr'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	region := payload["region"].(map[string]any)
	search := payload["search"].(map[string]any)
	if region["fb_location"] != "curitiba" || search["keyword"] != "Honda Civic EG" {
		t.Fatalf("payload = %v", payload)
	}
	if limits := payload["limits"].(map[string]any); limits["max_pages"] != float64(3) {
		t.Fatalf("limits = %v", limits)
	}

	var next time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT next_run_at FROM search_definitions`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next.Before(now.Add(27*time.Minute)) || next.After(now.Add(33*time.Minute)) {
		t.Fatalf("next_run_at = %v, want now+30m±10%%", next)
	}
	if n := tick(t, s); n != 0 {
		t.Fatalf("second tick enqueued %d, want 0 (not due)", n)
	}
}

func TestTickDoesNotDuplicateInFlightJobs(t *testing.T) {
	pool := testdb.New(t)
	testdb.Seed(t, pool)
	s := newScheduler(pool)
	tick(t, s)
	if _, err := pool.Exec(context.Background(),
		`UPDATE search_definitions SET next_run_at = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if n := tick(t, s); n != 0 {
		t.Fatalf("enqueued %d while previous job still queued", n)
	}
}

func TestTickSkipsDisabledSearchesAndRegionsWithoutLocation(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "nowhere", "")
	testdb.LinkRegion(t, pool, seed.SearchID, "nowhere")
	s := newScheduler(pool)
	if n := tick(t, s); n != 1 {
		t.Fatalf("enqueued %d, want 1 (region without fb_location skipped)", n)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE search_definitions SET enabled = false, next_run_at = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM jobs`); err != nil {
		t.Fatal(err)
	}
	if n := tick(t, s); n != 0 {
		t.Fatalf("disabled search enqueued %d", n)
	}
}
