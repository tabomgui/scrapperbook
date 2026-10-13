package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/testdb"
)

func complete(t *testing.T, pool *pgxpool.Pool, q *jobs.Queue, j *jobs.Job, attempt int) (jobs.Completed, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	done, err := q.Complete(ctx, tx, j.ID, attempt, "w1")
	if err != nil {
		return done, err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return done, nil
}

func jobStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM jobs WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCompleteMarksSucceededAndRecordsLastSuccess(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	if _, err := complete(t, pool, q, j, 2); !errors.Is(err, jobs.ErrStale) {
		t.Fatalf("wrong attempt: err = %v, want ErrStale", err)
	}
	done, err := complete(t, pool, q, j, 1)
	if err != nil {
		t.Fatal(err)
	}
	if done.Source != "facebook_marketplace" || *done.SearchID != s.SearchID || !done.StartedAt.Equal(c.now) {
		t.Fatalf("completed = %+v", done)
	}
	if st := jobStatus(t, pool, j.ID); st != "succeeded" {
		t.Fatalf("status = %s", st)
	}
	if _, err := complete(t, pool, q, j, 1); !errors.Is(err, jobs.ErrStale) {
		t.Fatalf("second complete: err = %v, want ErrStale", err)
	}

	// The next run of the same pair gets stop_before = previous start.
	c.Advance(time.Hour)
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	next := claim(t, q)
	if next.StopBefore == nil || !next.StopBefore.Equal(j.StartedAt) {
		t.Fatalf("stop_before = %v, want %v", next.StopBefore, j.StartedAt)
	}
}

func TestFailNetworkBacksOffThenDies(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ctx := context.Background()

	for i, wait := range []time.Duration{time.Minute, 5 * time.Minute, 25 * time.Minute} {
		j := claim(t, q)
		if j == nil || j.Attempt != i+1 {
			t.Fatalf("round %d: job = %+v", i, j)
		}
		out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindNetwork, "boom")
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != "queued" || !out.RunAfter.Equal(c.now.Add(wait)) {
			t.Fatalf("round %d: outcome = %+v", i, out)
		}
		c.Advance(wait)
	}
	j := claim(t, q)
	out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindTimeout, "boom")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "dead" || jobStatus(t, pool, j.ID) != "dead" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestFailCheckpointPausesSourceUntilResumed(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ctx := context.Background()

	j := claim(t, q)
	out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindCheckpoint, "checkpoint page")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "queued" {
		t.Fatalf("outcome = %+v", out)
	}
	var session string
	if err := pool.QueryRow(ctx, `SELECT status FROM source_sessions
		WHERE source = 'facebook_marketplace'`).Scan(&session); err != nil {
		t.Fatal(err)
	}
	if session != "needs_attention" {
		t.Fatalf("session = %s", session)
	}
	var alerts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE kind = 'system'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("system alerts = %d", alerts)
	}

	c.Advance(24 * time.Hour)
	if claim(t, q) != nil {
		t.Fatal("claimed while session needs attention")
	}
	if err := q.ResumeSource(ctx, "facebook_marketplace"); err != nil {
		t.Fatal(err)
	}
	again := claim(t, q)
	if again == nil || again.Attempt != 1 {
		t.Fatalf("after resume: job = %+v, want attempt 1", again)
	}
}

func TestFailRateLimitedCoolsDownSource(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)

	j := claim(t, q)
	if _, err := q.Fail(context.Background(), j.ID, j.Attempt, "w1", jobs.KindRateLimited, "slow down"); err != nil {
		t.Fatal(err)
	}
	c.Advance(59 * time.Minute)
	if claim(t, q) != nil {
		t.Fatal("claimed during cooldown")
	}
	c.Advance(2 * time.Minute)
	if claim(t, q) == nil {
		t.Fatal("no job after cooldown")
	}
}

func TestFailLayoutChangedRetriesOnceThenDies(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ctx := context.Background()

	j := claim(t, q)
	out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindLayoutChanged, "no results captured")
	if err != nil || out.Status != "queued" {
		t.Fatalf("first: %+v, %v", out, err)
	}
	c.Advance(5 * time.Minute)
	j = claim(t, q)
	out, err = q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindLayoutChanged, "no results captured")
	if err != nil || out.Status != "dead" {
		t.Fatalf("second: %+v, %v", out, err)
	}
	var alerts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE kind = 'system'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("system alerts = %d, want 1 per day", alerts)
	}
}

func TestFailRejectsStaleAttempt(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)
	if _, err := q.Fail(context.Background(), j.ID, 9, "w1", jobs.KindNetwork, "x"); !errors.Is(err, jobs.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
}

func TestReapExpiredLeases(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	c.Advance(5 * time.Minute)
	if n, err := q.ReapExpiredLeases(context.Background()); err != nil || n != 0 {
		t.Fatalf("early reap = %d, %v", n, err)
	}
	c.Advance(6 * time.Minute)
	if n, err := q.ReapExpiredLeases(context.Background()); err != nil || n != 1 {
		t.Fatalf("reap = %d, %v", n, err)
	}
	if st := jobStatus(t, pool, j.ID); st != "queued" {
		t.Fatalf("status = %s", st)
	}
	if next := claim(t, q); next == nil || next.Attempt != 2 {
		t.Fatalf("reclaimed = %+v, want attempt 2", next)
	}
}
