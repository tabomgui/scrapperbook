// Package scheduler turns due search definitions into jobs.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
)

type PageLimits struct {
	MaxPages int `json:"max_pages"`
	MaxItems int `json:"max_items"`
}

type Scheduler struct {
	pool    *pgxpool.Pool
	queue   *jobs.Queue
	sources []string
	limits  PageLimits
	log     *slog.Logger
	Clock   func() time.Time
}

func New(pool *pgxpool.Pool, queue *jobs.Queue, sources []string, log *slog.Logger) *Scheduler {
	return &Scheduler{
		pool: pool, queue: queue, sources: sources, log: log,
		limits: PageLimits{MaxPages: 8, MaxItems: 160},
		Clock:  time.Now,
	}
}

type searchPayload struct {
	ID      string          `json:"id"`
	Keyword string          `json:"keyword"`
	Filters json.RawMessage `json:"filters"`
}

// regionPayload carries fb_location because Facebook is the only MVP source.
type regionPayload struct {
	ID         string   `json:"id"`
	FBLocation string   `json:"fb_location"`
	Lat        *float64 `json:"lat"`
	Lng        *float64 `json:"lng"`
	RadiusKM   *int     `json:"radius_km"`
}

type payload struct {
	Search searchPayload `json:"search"`
	Region regionPayload `json:"region"`
	Limits PageLimits    `json:"limits"`
}

type dueSearch struct {
	search   searchPayload
	priority int
}

// ErrSearchNotFound is returned by RunNow for an unknown search id.
var ErrSearchNotFound = errors.New("search not found")

// RunNow enqueues jobs for a search's enabled regions immediately, using
// the same dedup-safe Enqueue path Tick uses (so a region with an
// already-queued/running job for this search is silently skipped, not
// duplicated). Unlike Tick: it ignores the search's own enabled flag (a
// disabled search can still be run manually) and never touches
// next_run_at. It does NOT bypass jobs.Queue's claim-time rate limits —
// those still apply to a manually-enqueued job exactly as they do to an
// automatic one.
func (s *Scheduler) RunNow(ctx context.Context, searchID string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var d dueSearch
	err = tx.QueryRow(ctx, `SELECT id, keyword, filters, priority FROM search_definitions
		WHERE id = $1`, searchID).Scan(&d.search.ID, &d.search.Keyword, &d.search.Filters, &d.priority)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrSearchNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load search %s: %w", searchID, err)
	}

	regions, err := regionsFor(ctx, tx, d.search.ID)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, rg := range regions {
		raw, err := json.Marshal(payload{Search: d.search, Region: rg, Limits: s.limits})
		if err != nil {
			return 0, err
		}
		for _, src := range s.sources {
			ok, err := s.queue.Enqueue(ctx, tx, jobs.NewJob{
				Type: "search", Source: src, SearchID: d.search.ID, RegionID: rg.ID,
				Priority: d.priority, Payload: raw,
			})
			if err != nil {
				return 0, err
			}
			if ok {
				created++
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return created, nil
}

// Tick enqueues jobs for every due search and returns how many were created.
//
// Everything runs in one transaction: at this MVP's scale (a handful of
// searches) that's cheap and keeps the batch atomic. If the number of search
// definitions grows large enough for this to matter, the fix is to commit
// per-search (or batch) instead of restructuring this function.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	now := s.Clock()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT id, keyword, filters, priority FROM search_definitions
		WHERE enabled AND next_run_at <= $1
		ORDER BY priority DESC
		FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return 0, fmt.Errorf("select due searches: %w", err)
	}
	var due []dueSearch
	for rows.Next() {
		var d dueSearch
		if err := rows.Scan(&d.search.ID, &d.search.Keyword, &d.search.Filters, &d.priority); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	created := 0
	for _, d := range due {
		regions, err := regionsFor(ctx, tx, d.search.ID)
		if err != nil {
			return 0, err
		}
		for _, rg := range regions {
			raw, err := json.Marshal(payload{Search: d.search, Region: rg, Limits: s.limits})
			if err != nil {
				return 0, err
			}
			for _, src := range s.sources {
				ok, err := s.queue.Enqueue(ctx, tx, jobs.NewJob{
					Type: "search", Source: src, SearchID: d.search.ID, RegionID: rg.ID,
					Priority: d.priority, Payload: raw,
				})
				if err != nil {
					return 0, err
				}
				if ok {
					created++
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE search_definitions
			SET next_run_at = $2::timestamptz + frequency * (0.9 + random() * 0.2), updated_at = $2
			WHERE id = $1`, d.search.ID, now); err != nil {
			return 0, fmt.Errorf("reschedule: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	if len(due) > 0 {
		s.log.Info("tick complete", "due_searches", len(due), "jobs_enqueued", created)
	}
	return created, nil
}

// regionsFor doesn't need FOR UPDATE: nothing else writes to jobs for a given
// (search, region, source) tuple except through Enqueue's unique-constraint
// guard, so a concurrent edit to a region's fb_location/enabled mid-tick just
// takes effect on the next tick instead of causing duplicate or corrupt state.
func regionsFor(ctx context.Context, tx pgx.Tx, searchID string) ([]regionPayload, error) {
	rows, err := tx.Query(ctx, `SELECT r.id, r.fb_location, r.lat, r.lng, r.radius_km
		FROM search_definition_regions sdr
		JOIN search_regions r ON r.id = sdr.region_id
		WHERE sdr.search_id = $1 AND r.enabled AND r.fb_location IS NOT NULL
		ORDER BY r.id`, searchID)
	if err != nil {
		return nil, fmt.Errorf("select regions: %w", err)
	}
	defer rows.Close()
	var out []regionPayload
	for rows.Next() {
		var rg regionPayload
		if err := rows.Scan(&rg.ID, &rg.FBLocation, &rg.Lat, &rg.Lng, &rg.RadiusKM); err != nil {
			return nil, err
		}
		out = append(out, rg)
	}
	return out, rows.Err()
}
