package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"scrapperbook/backend/internal/metrics"
)

// ErrStale means the job is not running with the given attempt
// (already finished, reaped, or a duplicate submission).
var ErrStale = errors.New("job is not running with this attempt")

// ErrUnknownSource means the given source has no row in source_sessions.
var ErrUnknownSource = errors.New("unknown source")

type ErrorKind string

const (
	KindNetwork       ErrorKind = "network"
	KindTimeout       ErrorKind = "timeout"
	KindRateLimited   ErrorKind = "rate_limited"
	KindCheckpoint    ErrorKind = "checkpoint"
	KindLoginRequired ErrorKind = "login_required"
	KindLayoutChanged ErrorKind = "layout_changed"
)

func ParseErrorKind(s string) (ErrorKind, bool) {
	switch k := ErrorKind(s); k {
	case KindNetwork, KindTimeout, KindRateLimited, KindCheckpoint, KindLoginRequired, KindLayoutChanged:
		return k, true
	}
	return "", false
}

const (
	rateLimitCooldown        = time.Hour
	layoutChangedRetryDelay  = 5 * time.Minute
	layoutChangedMaxAttempts = 2
)

var backoff = []time.Duration{time.Minute, 5 * time.Minute, 25 * time.Minute}

func backoffFor(attempt int) time.Duration {
	i := min(max(attempt-1, 0), len(backoff)-1)
	return backoff[i]
}

type Completed struct {
	Source     string
	SearchID   *string
	RegionID   *string
	StartedAt  time.Time
	FinishedAt time.Time
}

type Outcome struct {
	Status   string     `json:"status"`
	RunAfter *time.Time `json:"run_after,omitempty"`
}

// Complete marks a running job as succeeded inside the caller's transaction,
// so ingest and completion commit together.
func (q *Queue) Complete(ctx context.Context, tx pgx.Tx, jobID string, attempt int, workerID string) (Completed, error) {
	now := q.Clock()
	c := Completed{FinishedAt: now}
	err := tx.QueryRow(ctx, `UPDATE jobs
		SET status = 'succeeded', finished_at = $3, lease_until = NULL, error_kind = NULL, error_message = NULL
		WHERE id = $1 AND attempts = $2 AND status = 'running'
		RETURNING source, search_id, region_id, started_at`,
		jobID, attempt, now).Scan(&c.Source, &c.SearchID, &c.RegionID, &c.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Completed{}, ErrStale
	}
	if err != nil {
		return Completed{}, fmt.Errorf("complete job: %w", err)
	}
	if c.SearchID != nil && c.RegionID != nil {
		if _, err := tx.Exec(ctx, `UPDATE search_definition_regions SET last_success_at = $3
			WHERE search_id = $1 AND region_id = $2`, *c.SearchID, *c.RegionID, c.StartedAt); err != nil {
			return Completed{}, fmt.Errorf("record last success: %w", err)
		}
	}
	if err := markWorkerIdle(ctx, tx, workerID, now, true); err != nil {
		return Completed{}, err
	}
	// "search" is hardcoded rather than read from the job: only that type
	// exists today (the scheduler never enqueues "enrich" jobs yet). Revisit
	// this label once a second job type is real.
	metrics.JobsTotal.WithLabelValues(c.Source, "search", "succeeded", "").Inc()
	metrics.JobDurationSeconds.WithLabelValues(c.Source, "search").Observe(now.Sub(c.StartedAt).Seconds())
	return c, nil
}

// RecordResult stores a completed job's ingest outcome (new/changed/seen/
// invalid/irrelevant/alerts counts), inside the same transaction as
// Complete and the ingest write — a job is never left "succeeded" with no
// way to later see what it found.
func (q *Queue) RecordResult(ctx context.Context, tx pgx.Tx, jobID string, newCount, changedCount, seenCount, invalidCount, irrelevantCount, alertsCount int) error {
	_, err := tx.Exec(ctx, `UPDATE jobs SET
		result_new = $2, result_changed = $3, result_seen = $4,
		result_invalid = $5, result_irrelevant = $6, result_alerts = $7
		WHERE id = $1`, jobID, newCount, changedCount, seenCount, invalidCount, irrelevantCount, alertsCount)
	if err != nil {
		return fmt.Errorf("record result for job %s: %w", jobID, err)
	}
	return nil
}

// Fail records a classified failure and decides retry, death and source state.
func (q *Queue) Fail(ctx context.Context, jobID string, attempt int, workerID string, kind ErrorKind, message string) (Outcome, error) {
	now := q.Clock()
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback(ctx)

	var source string
	var attempts, maxAttempts int
	err = tx.QueryRow(ctx, `SELECT source, attempts, max_attempts FROM jobs
		WHERE id = $1 AND attempts = $2 AND status = 'running' FOR UPDATE`,
		jobID, attempt).Scan(&source, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, ErrStale
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("load job: %w", err)
	}

	// requeue puts the job back in the queue. consumeAttempt=false means this
	// failure doesn't count against max_attempts (checkpoint/login_required
	// retries are unbounded by design: the source is paused until a human
	// resumes it, not retried on a timer).
	requeue := func(at time.Time, consumeAttempt bool) (Outcome, error) {
		refund := 0
		if !consumeAttempt {
			refund = 1
		}
		_, err := tx.Exec(ctx, `UPDATE jobs
			SET status = 'queued', run_after = $2, lease_until = NULL, worker_id = NULL,
			    attempts = attempts - $3, error_kind = $4, error_message = $5
			WHERE id = $1`, jobID, at, refund, string(kind), message)
		return Outcome{Status: "queued", RunAfter: &at}, err
	}
	bury := func() (Outcome, error) {
		_, err := tx.Exec(ctx, `UPDATE jobs
			SET status = 'dead', finished_at = $2, lease_until = NULL, error_kind = $3, error_message = $4
			WHERE id = $1`, jobID, now, string(kind), message)
		return Outcome{Status: "dead"}, err
	}

	var out Outcome
	switch kind {
	case KindCheckpoint, KindLoginRequired:
		out, err = requeue(now, false)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE source_sessions
				SET status = 'needs_attention', last_checkpoint_at = $2, note = $3, updated_at = $2
				WHERE source = $1`, source, now, message)
		}
		if err == nil {
			err = systemAlert(ctx, tx,
				fmt.Sprintf("system:%s:%s:%d", kind, source, now.Unix()),
				fmt.Sprintf("%s: sessão precisa de atenção (%s). Refaça o login e libere a fonte.", source, kind))
		}
	case KindRateLimited:
		at := now.Add(rateLimitCooldown)
		if attempts < maxAttempts {
			out, err = requeue(at, true)
		} else {
			out, err = bury()
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE source_sessions SET cooldown_until = $2, updated_at = $3
				WHERE source = $1`, source, at, now)
		}
	case KindLayoutChanged:
		if attempts < layoutChangedMaxAttempts && attempts < maxAttempts {
			out, err = requeue(now.Add(layoutChangedRetryDelay), true)
		} else {
			out, err = bury()
		}
		if err == nil {
			err = systemAlert(ctx, tx,
				fmt.Sprintf("system:layout_changed:%s:%s", source, now.Format("2006-01-02")),
				fmt.Sprintf("%s: layout mudou, nenhum anúncio capturado (%s).", source, message))
		}
	default: // network, timeout
		if attempts < maxAttempts {
			out, err = requeue(now.Add(backoffFor(attempts)), true)
		} else {
			out, err = bury()
		}
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("record failure: %w", err)
	}
	if err := markWorkerIdle(ctx, tx, workerID, now, false); err != nil {
		return Outcome{}, err
	}
	metrics.JobsTotal.WithLabelValues(source, "search", out.Status, string(kind)).Inc()
	if err := tx.Commit(ctx); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// markWorkerIdle releases the worker after a job finishes, succeeded or not.
func markWorkerIdle(ctx context.Context, tx pgx.Tx, workerID string, now time.Time, succeeded bool) error {
	counterCol := "jobs_failed"
	if succeeded {
		counterCol = "jobs_ok"
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE workers
		SET status = 'idle', current_job_id = NULL, %s = %s + 1, last_heartbeat_at = $2
		WHERE id = $1`, counterCol, counterCol), workerID, now)
	if err != nil {
		return fmt.Errorf("update worker: %w", err)
	}
	return nil
}

func systemAlert(ctx context.Context, tx pgx.Tx, key, message string) error {
	_, err := tx.Exec(ctx, `INSERT INTO notifications (dedupe_key, kind, message)
		VALUES ($1, 'system', $2) ON CONFLICT (dedupe_key) DO NOTHING`, key, message)
	return err
}

// ReapExpiredLeases returns jobs whose worker vanished to the queue (or buries them).
func (q *Queue) ReapExpiredLeases(ctx context.Context) (int64, error) {
	now := q.Clock()
	tag, err := q.pool.Exec(ctx, `UPDATE jobs SET
		status      = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'queued' END,
		finished_at = CASE WHEN attempts >= max_attempts THEN $1::timestamptz END,
		run_after   = $1, lease_until = NULL, worker_id = NULL, error_kind = 'lease_expired'
		WHERE status = 'running' AND lease_until < $1`, now)
	if err != nil {
		return 0, fmt.Errorf("reap leases: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ResumeSource clears a paused or cooling-down source after a manual re-login.
func (q *Queue) ResumeSource(ctx context.Context, source string) error {
	tag, err := q.pool.Exec(ctx, `UPDATE source_sessions
		SET status = 'ok', cooldown_until = NULL, note = NULL, updated_at = $2
		WHERE source = $1`, source, q.Clock())
	if err != nil {
		return fmt.Errorf("resume source %s: %w", source, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnknownSource
	}
	return nil
}
