// Package workers tracks crawler worker registration and health.
package workers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUnknownWorker   = errors.New("unknown worker")
	ErrInvalidStatus   = errors.New("invalid worker status")
	ErrMissingWorkerID = errors.New("worker_id is required")
)

var validStatuses = map[string]bool{
	"starting": true, "healthy": true, "busy": true, "idle": true,
	"error": true, "cooldown": true, "stopped": true,
}

type Registration struct {
	ID             string   `json:"worker_id"`
	Hostname       string   `json:"hostname"`
	CrawlerVersion string   `json:"crawler_version"`
	Sources        []string `json:"sources"`
}

type Registry struct {
	pool  *pgxpool.Pool
	Clock func() time.Time
}

func New(pool *pgxpool.Pool) *Registry {
	return &Registry{pool: pool, Clock: time.Now}
}

func (r *Registry) Register(ctx context.Context, reg Registration) error {
	if reg.ID == "" {
		return ErrMissingWorkerID
	}
	if reg.Sources == nil {
		reg.Sources = []string{}
	}
	// Re-registering always resets status to 'healthy' and clears
	// current_job_id, even if this worker crashed mid-job. That's fine: the
	// jobs row it was working on is untouched and still 'running' with a live
	// lease, so jobs.ReapExpiredLeases requeues it once the lease expires,
	// independent of this worker row. The only cost is a brief (one lease
	// duration) window where the dashboard shows the worker idle while the
	// orphaned job still says 'running' with no worker actually holding it.
	_, err := r.pool.Exec(ctx, `INSERT INTO workers
		(id, hostname, status, crawler_version, sources, registered_at, last_heartbeat_at)
		VALUES ($1, $2, 'healthy', $3, $4, $5, $5)
		ON CONFLICT (id) DO UPDATE SET
			hostname = EXCLUDED.hostname, status = 'healthy',
			crawler_version = EXCLUDED.crawler_version, sources = EXCLUDED.sources,
			current_job_id = NULL, last_heartbeat_at = EXCLUDED.last_heartbeat_at`,
		reg.ID, reg.Hostname, reg.CrawlerVersion, reg.Sources, r.Clock())
	if err != nil {
		return fmt.Errorf("register worker: %w", err)
	}
	return nil
}

func (r *Registry) Heartbeat(ctx context.Context, id, status string, currentJobID *string) error {
	if !validStatuses[status] {
		return ErrInvalidStatus
	}
	tag, err := r.pool.Exec(ctx, `UPDATE workers
		SET status = $2, current_job_id = $3, last_heartbeat_at = $4 WHERE id = $1`,
		id, status, currentJobID, r.Clock())
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnknownWorker
	}
	return nil
}
