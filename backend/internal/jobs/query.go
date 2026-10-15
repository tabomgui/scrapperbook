package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type JobSummary struct {
	ID               string     `json:"id"`
	Type             string     `json:"type"`
	Source           string     `json:"source"`
	SearchID         *string    `json:"search_id,omitempty"`
	RegionID         *string    `json:"region_id,omitempty"`
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	ErrorKind        *string    `json:"error_kind,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	SearchName       *string    `json:"search_name,omitempty"`
	RegionName       *string    `json:"region_name,omitempty"`
	ResultNew        *int       `json:"result_new,omitempty"`
	ResultChanged    *int       `json:"result_changed,omitempty"`
	ResultSeen       *int       `json:"result_seen,omitempty"`
	ResultInvalid    *int       `json:"result_invalid,omitempty"`
	ResultIrrelevant *int       `json:"result_irrelevant,omitempty"`
	ResultAlerts     *int       `json:"result_alerts,omitempty"`
}

type JobFilter struct {
	Status    string
	ErrorKind string
	Limit     int // 0 or >200 falls back to 50
}

func (q *Queue) ListJobs(ctx context.Context, f JobFilter) ([]JobSummary, error) {
	query := `SELECT jobs.id, jobs.type, jobs.source, jobs.search_id, jobs.region_id, jobs.status,
	                  jobs.attempts, jobs.error_kind, jobs.created_at, jobs.started_at, jobs.finished_at,
	                  sd.name, sr.name,
	                  jobs.result_new, jobs.result_changed, jobs.result_seen,
	                  jobs.result_invalid, jobs.result_irrelevant, jobs.result_alerts
	           FROM jobs
	           LEFT JOIN search_definitions sd ON sd.id = jobs.search_id
	           LEFT JOIN search_regions sr ON sr.id = jobs.region_id`
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
		where = append(where, "jobs.status = "+arg(f.Status))
	}
	if f.ErrorKind != "" {
		where = append(where, "jobs.error_kind = "+arg(f.ErrorKind))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY jobs.created_at DESC"
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
			&j.Attempts, &j.ErrorKind, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
			&j.SearchName, &j.RegionName,
			&j.ResultNew, &j.ResultChanged, &j.ResultSeen,
			&j.ResultInvalid, &j.ResultIrrelevant, &j.ResultAlerts); err != nil {
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
