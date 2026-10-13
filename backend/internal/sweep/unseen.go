// Package sweep runs periodic maintenance over listings and raw_captures
// that no single request triggers: marking stale listings unseen, and
// purging old raw captures.
package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// markUnseenBatchSize caps how many rows a single MarkUnseen iteration locks
// and processes. Without a cap, a backlog of stale listings (e.g. after the
// sweep was paused, or a crawler bug stopped updating last_seen_at for a
// large slice of rows) would make SELECT ... FOR UPDATE lock an unbounded
// number of rows and hold that lock through the bulk UPDATE and per-row
// snapshot INSERTs — risking a long-held lock that blocks a concurrent
// crawler write to one of those same rows. At this scale (a few thousand
// listings total, single-user system) 500 keeps each transaction short
// while still draining a large backlog in a handful of iterations.
const markUnseenBatchSize = 500

// MarkUnseen flips listings that haven't been seen in `after` to 'unseen',
// recording the transition as a snapshot so history shows when a listing
// disappeared (design spec §10). Absence from a search doesn't prove
// removal — results are paginated and limited — so this is a conservative,
// time-based signal, not a definitive "sold" call.
//
// Processing happens in bounded batches (markUnseenBatchSize rows per
// transaction) so a large backlog of stale listings never holds a single
// FOR UPDATE lock across an unbounded number of rows.
func MarkUnseen(ctx context.Context, pool *pgxpool.Pool, now time.Time, after time.Duration) (int64, error) {
	cutoff := now.Add(-after)
	var total int64
	for {
		n, err := markUnseenBatch(ctx, pool, now, cutoff)
		if err != nil {
			return total, err
		}
		total += n
		if n < markUnseenBatchSize {
			return total, nil
		}
	}
}

// markUnseenBatch processes up to markUnseenBatchSize stale listings in a
// single transaction and returns how many it flipped.
func markUnseenBatch(ctx context.Context, pool *pgxpool.Pool, now, cutoff time.Time) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, price_cents, title, description, raw_data FROM listings
		WHERE status != 'unseen' AND last_seen_at < $1
		ORDER BY id
		LIMIT $2
		FOR UPDATE`, cutoff, markUnseenBatchSize)
	if err != nil {
		return 0, fmt.Errorf("select stale listings: %w", err)
	}
	type stale struct {
		id          int64
		priceCents  *int64
		title       string
		description *string
		rawData     []byte
	}
	var staleRows []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.priceCents, &s.title, &s.description, &s.rawData); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan stale listing: %w", err)
		}
		staleRows = append(staleRows, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale listings: %w", err)
	}
	if len(staleRows) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit mark-unseen: %w", err)
		}
		return 0, nil
	}

	ids := make([]int64, len(staleRows))
	for i, s := range staleRows {
		ids[i] = s.id
	}
	if _, err := tx.Exec(ctx, `UPDATE listings SET status = 'unseen', last_changed_at = $2, updated_at = $2
		WHERE id = ANY($1)`, ids, now); err != nil {
		return 0, fmt.Errorf("mark listings unseen: %w", err)
	}
	for _, s := range staleRows {
		if _, err := tx.Exec(ctx, `INSERT INTO listing_snapshots
			(listing_id, price_cents, title, description, status, job_id, captured_at, raw_data)
			VALUES ($1, $2, $3, $4, 'unseen', NULL, $5, $6)`,
			s.id, s.priceCents, s.title, s.description, now, s.rawData); err != nil {
			return 0, fmt.Errorf("insert unseen snapshot for listing %d: %w", s.id, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit mark-unseen: %w", err)
	}
	return int64(len(staleRows)), nil
}
