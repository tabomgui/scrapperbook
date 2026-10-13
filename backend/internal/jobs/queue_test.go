package jobs_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/testdb"
)

var (
	sources = []string{"facebook_marketplace"}
	types   = []string{"search"}
)

func saoPaulo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// clock is a controllable time source shared with the queue.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func openLimits() jobs.Limits {
	return jobs.Limits{MaxPerHour: 100, MinGap: 0, Lease: 10 * time.Minute}
}

func newQueue(t *testing.T, pool *pgxpool.Pool, limits jobs.Limits) (*jobs.Queue, *clock) {
	t.Helper()
	c := &clock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, saoPaulo(t))}
	q := jobs.New(pool, limits)
	q.Clock = c.Now
	return q, c
}

func enqueue(t *testing.T, q *jobs.Queue, pool *pgxpool.Pool, searchID, regionID string, priority int) {
	t.Helper()
	ok, err := q.Enqueue(context.Background(), pool, jobs.NewJob{
		Type: "search", Source: "facebook_marketplace", SearchID: searchID, RegionID: regionID,
		Priority: priority, Payload: json.RawMessage(`{"search":{"keyword":"civic"}}`),
	})
	if err != nil || !ok {
		t.Fatalf("enqueue = %v, %v", ok, err)
	}
}

func claim(t *testing.T, q *jobs.Queue) *jobs.Job {
	t.Helper()
	j, err := q.Claim(context.Background(), "w1", sources, types)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestEnqueueSkipsDuplicateInFlight(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ok, err := q.Enqueue(context.Background(), pool, jobs.NewJob{
		Type: "search", Source: "facebook_marketplace", SearchID: s.SearchID, RegionID: s.RegionID,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil || ok {
		t.Fatalf("duplicate enqueue = %v, %v; want false, nil", ok, err)
	}
}

func TestClaimReturnsJobAndMarksRunning(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)

	j := claim(t, q)
	if j == nil {
		t.Fatal("no job claimed")
	}
	if j.Attempt != 1 || j.Source != "facebook_marketplace" || *j.SearchID != s.SearchID || *j.RegionID != s.RegionID {
		t.Fatalf("job = %+v", j)
	}
	if !j.StartedAt.Equal(c.now) || j.StopBefore != nil {
		t.Fatalf("started_at = %v, stop_before = %v", j.StartedAt, j.StopBefore)
	}
	var status, worker string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, worker_id FROM jobs WHERE id = $1`, j.ID).Scan(&status, &worker); err != nil {
		t.Fatal(err)
	}
	if status != "running" || worker != "w1" {
		t.Fatalf("status/worker = %s/%s", status, worker)
	}
	if again := claim(t, q); again != nil {
		t.Fatal("claimed a running job twice")
	}
}

func TestClaimPrefersHigherPriority(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, s.SearchID, "londrina-pr")
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	enqueue(t, q, pool, s.SearchID, "londrina-pr", 5)

	if j := claim(t, q); j == nil || *j.RegionID != "londrina-pr" {
		t.Fatalf("claimed %+v, want londrina-pr", j)
	}
}

func TestClaimGates(t *testing.T) {
	cases := []struct {
		name   string
		limits func(jobs.Limits) jobs.Limits
		prep   func(t *testing.T, pool *pgxpool.Pool, c *clock)
	}{
		{"quiet hours", func(l jobs.Limits) jobs.Limits {
			l.Quiet = jobs.QuietHours{Enabled: true, StartHour: 11, EndHour: 13, Location: saoPaulo(t)}
			return l
		}, nil},
		{"hourly budget", func(l jobs.Limits) jobs.Limits { l.MaxPerHour = 1; return l },
			func(t *testing.T, pool *pgxpool.Pool, c *clock) { recentStart(t, pool, c.now.Add(-30*time.Minute)) }},
		{"min gap", func(l jobs.Limits) jobs.Limits { l.MinGap = 4 * time.Minute; return l },
			func(t *testing.T, pool *pgxpool.Pool, c *clock) { recentStart(t, pool, c.now.Add(-time.Minute)) }},
		{"session needs attention", nil, func(t *testing.T, pool *pgxpool.Pool, c *clock) {
			exec(t, pool, `UPDATE source_sessions SET status = 'needs_attention'`)
		}},
		{"source cooldown", nil, func(t *testing.T, pool *pgxpool.Pool, c *clock) {
			exec(t, pool, `UPDATE source_sessions SET cooldown_until = $1`, c.now.Add(time.Minute))
		}},
		{"run_after in future", nil, func(t *testing.T, pool *pgxpool.Pool, c *clock) {
			exec(t, pool, `UPDATE jobs SET run_after = $1 WHERE status = 'queued'`, c.now.Add(time.Minute))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testdb.New(t)
			s := testdb.Seed(t, pool)
			limits := openLimits()
			if tc.limits != nil {
				limits = tc.limits(limits)
			}
			q, c := newQueue(t, pool, limits)
			enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
			if tc.prep != nil {
				tc.prep(t, pool, c)
			}
			if j := claim(t, q); j != nil {
				t.Fatalf("claimed %s despite gate", j.ID)
			}
		})
	}
}

func TestConcurrentClaimsNeverShareJob(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, s.SearchID, "londrina-pr")
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	enqueue(t, q, pool, s.SearchID, "londrina-pr", 0)

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := q.Claim(context.Background(), "w", sources, types)
			if err != nil {
				t.Error(err)
				return
			}
			if j != nil {
				mu.Lock()
				seen[j.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 2 {
		t.Fatalf("claimed %d distinct jobs, want 2", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %s claimed %d times", id, n)
		}
	}
}

// recentStart inserts a finished job that started at the given time.
func recentStart(t *testing.T, pool *pgxpool.Pool, at time.Time) {
	t.Helper()
	exec(t, pool, `INSERT INTO jobs (type, source, payload, status, run_after, started_at, finished_at)
		VALUES ('search', 'facebook_marketplace', '{}', 'succeeded', $1, $1, $1)`, at)
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
