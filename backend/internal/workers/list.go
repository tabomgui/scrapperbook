package workers

import (
	"context"
	"fmt"
	"time"
)

type Worker struct {
	ID              string     `json:"id"`
	Hostname        string     `json:"hostname"`
	Status          string     `json:"status"`
	CrawlerVersion  string     `json:"crawler_version"`
	Sources         []string   `json:"sources"`
	CurrentJobID    *string    `json:"current_job_id,omitempty"`
	JobsOK          int        `json:"jobs_ok"`
	JobsFailed      int        `json:"jobs_failed"`
	CooldownUntil   *time.Time `json:"cooldown_until,omitempty"`
	RegisteredAt    time.Time  `json:"registered_at"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at"`
}

func (r *Registry) List(ctx context.Context) ([]Worker, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, hostname, status, crawler_version, sources,
		current_job_id, jobs_ok, jobs_failed, cooldown_until, registered_at, last_heartbeat_at
		FROM workers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		var w Worker
		if err := rows.Scan(&w.ID, &w.Hostname, &w.Status, &w.CrawlerVersion, &w.Sources,
			&w.CurrentJobID, &w.JobsOK, &w.JobsFailed, &w.CooldownUntil,
			&w.RegisteredAt, &w.LastHeartbeatAt); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
