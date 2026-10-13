package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type JobSummary struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Source     string     `json:"source"`
	SearchID   *string    `json:"search_id,omitempty"`
	RegionID   *string    `json:"region_id,omitempty"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	ErrorKind  *string    `json:"error_kind,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type JobFilter struct {
	Status    string
	ErrorKind string
	Limit     int // 0 or >200 falls back to 50
}

func (q *Queue) ListJobs(ctx context.Context, f JobFilter) ([]JobSummary, error) {
	query := `SELECT id, type, source, search_id, region_id, status, attempts, error_kind,
	                  created_at, started_at, finished_at
	           FROM jobs`
	var where []string
	var args []any
	// arg appends v to args and returns its positional placeholder. Filter
	// values only ever reach the query through this parametrized args slice,
	// never interpolated as text, so there's no injection risk despite the
	// dynamic WHERE construction.
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Status != "" {
		where = append(where, "status = "+arg(f.Status))
	}
	if f.ErrorKind != "" {
		where = append(where, "error_kind = "+arg(f.ErrorKind))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC"
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := q.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var out []JobSummary
	for rows.Next() {
		var j JobSummary
		if err := rows.Scan(&j.ID, &j.Type, &j.Source, &j.SearchID, &j.RegionID, &j.Status,
			&j.Attempts, &j.ErrorKind, &j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

type SourceSession struct {
	Source           string     `json:"source"`
	Status           string     `json:"status"`
	LastCheckpointAt *time.Time `json:"last_checkpoint_at,omitempty"`
	CooldownUntil    *time.Time `json:"cooldown_until,omitempty"`
	Note             *string    `json:"note,omitempty"`
}

func (q *Queue) ListSessions(ctx context.Context) ([]SourceSession, error) {
	rows, err := q.pool.Query(ctx, `SELECT source, status, last_checkpoint_at, cooldown_until, note
		FROM source_sessions ORDER BY source`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []SourceSession
	for rows.Next() {
		var s SourceSession
		if err := rows.Scan(&s.Source, &s.Status, &s.LastCheckpointAt, &s.CooldownUntil, &s.Note); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
