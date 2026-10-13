// Package jobs implements the Postgres-backed job queue.
// All time comparisons use Queue.Clock, never the database clock.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/db"
)

const defaultMaxAttempts = 4

type NewJob struct {
	Type        string
	Source      string
	SearchID    string
	RegionID    string
	Priority    int
	Payload     json.RawMessage
	MaxAttempts int // 0 means defaultMaxAttempts
}

type Job struct {
	ID        string
	Type      string
	Source    string
	SearchID  *string
	RegionID  *string
	Attempt   int
	Payload   json.RawMessage
	StartedAt time.Time
	// StopBefore is the start time of the last successful run for this same
	// search+region, or nil if none has ever succeeded. A crawler can use it
	// to stop paging once it reaches listings already seen as of that time,
	// instead of re-scraping the whole result set on every run.
	StopBefore *time.Time
}

type QuietHours struct {
	Enabled   bool
	StartHour int // inclusive, 0-23
	EndHour   int // exclusive, 0-23
	Location  *time.Location
}

// Contains reports whether t falls in the quiet window. Windows may wrap midnight.
func (q QuietHours) Contains(t time.Time) bool {
	if !q.Enabled {
		return false
	}
	h := t.In(q.Location).Hour()
	if q.StartHour <= q.EndHour {
		return h >= q.StartHour && h < q.EndHour
	}
	return h >= q.StartHour || h < q.EndHour
}

type Limits struct {
	MaxPerHour int
	MinGap     time.Duration
	Lease      time.Duration
	Quiet      QuietHours
}

type Queue struct {
	pool   *pgxpool.Pool
	limits Limits
	Clock  func() time.Time
}

func New(pool *pgxpool.Pool, limits Limits) *Queue {
	return &Queue{pool: pool, limits: limits, Clock: time.Now}
}

// Enqueue inserts a job unless the same source/search/region is already in flight.
func (q *Queue) Enqueue(ctx context.Context, d db.DBTX, j NewJob) (bool, error) {
	maxAttempts := j.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	tag, err := d.Exec(ctx, `INSERT INTO jobs
		(type, source, search_id, region_id, priority, payload, max_attempts, run_after)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT DO NOTHING`,
		j.Type, j.Source, j.SearchID, j.RegionID, j.Priority, j.Payload, maxAttempts, q.Clock())
	if err != nil {
		return false, fmt.Errorf("enqueue: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// pickSQL selects the next eligible job. Parameters: $1 sources, $2 types,
// $3 now, $4 now-1h (hourly budget window), $5 MaxPerHour, $6 now-MinGap
// (min-gap cutoff).
const pickSQL = `
WITH open_sources AS (
    SELECT s.source
    FROM source_sessions s
    WHERE s.source = ANY($1)
      -- session must be healthy and not cooling down after a rate-limit/checkpoint
      AND s.status = 'ok'
      AND (s.cooldown_until IS NULL OR s.cooldown_until <= $3)
      -- at most MaxPerHour jobs started for this source in the last hour
      AND (SELECT count(*) FROM jobs j WHERE j.source = s.source AND j.started_at > $4) < $5
      -- at least MinGap since the most recent job started for this source
      AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.source = s.source AND j.started_at > $6)
)
SELECT id FROM jobs
WHERE status = 'queued'
  AND run_after <= $3
  AND type = ANY($2)
  AND source IN (SELECT source FROM open_sources)
ORDER BY priority DESC, run_after
LIMIT 1
FOR UPDATE SKIP LOCKED`

// Claim leases the next eligible job, or returns nil when none may run now.
func (q *Queue) Claim(ctx context.Context, workerID string, sources, types []string) (*Job, error) {
	now := q.Clock()
	if q.limits.Quiet.Contains(now) {
		return nil, nil
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Held for the whole transaction, including the StopBefore lookup and the
	// worker-busy update below: with a single MVP worker this costs nothing,
	// and it keeps every write in Claim atomic. If multiple concurrent workers
	// ever make this a throughput bottleneck, the lock only needs to cover the
	// pickSQL select and the jobs UPDATE that follows it — the StopBefore read
	// and the workers update don't participate in the invariant it protects.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('jobs.claim'))`); err != nil {
		return nil, fmt.Errorf("claim lock: %w", err)
	}
	var id string
	err = tx.QueryRow(ctx, pickSQL, sources, types, now,
		now.Add(-time.Hour), q.limits.MaxPerHour, now.Add(-q.limits.MinGap)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pick job: %w", err)
	}

	var j Job
	err = tx.QueryRow(ctx, `UPDATE jobs
		SET status = 'running', attempts = attempts + 1, started_at = $2, lease_until = $3, worker_id = $4
		WHERE id = $1
		RETURNING id, type, source, search_id, region_id, attempts, payload, started_at`,
		id, now, now.Add(q.limits.Lease), workerID).Scan(
		&j.ID, &j.Type, &j.Source, &j.SearchID, &j.RegionID, &j.Attempt, &j.Payload, &j.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("lease job: %w", err)
	}
	if j.SearchID != nil && j.RegionID != nil {
		// pgx.ErrNoRows here means this search+region pair has never run before
		// (no search_definition_regions row); that's treated the same as a row
		// that exists with last_success_at still NULL — both leave StopBefore nil.
		err = tx.QueryRow(ctx, `SELECT last_success_at FROM search_definition_regions
			WHERE search_id = $1 AND region_id = $2`, *j.SearchID, *j.RegionID).Scan(&j.StopBefore)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("load stop_before: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workers
		SET status = 'busy', current_job_id = $2, last_heartbeat_at = $3 WHERE id = $1`,
		workerID, j.ID, now); err != nil {
		return nil, fmt.Errorf("mark worker busy: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &j, nil
}
