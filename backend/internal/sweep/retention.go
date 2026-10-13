package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PurgeOldRawCaptures deletes raw_captures rows older than `retention`.
// Raw payloads exist to let a normalizer bug be fixed and re-applied
// without a re-crawl shortly after capture — not to be a permanent archive.
func PurgeOldRawCaptures(ctx context.Context, pool *pgxpool.Pool, now time.Time, retention time.Duration) (int64, error) {
	cutoff := now.Add(-retention)
	// Unlike unseen.go's batched delete, this isn't read-then-write on the same
	// row (no FOR UPDATE race), and captured_at is indexed, so a single
	// unbatched index-range delete is safe without a transaction.
	tag, err := pool.Exec(ctx, `DELETE FROM raw_captures WHERE captured_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge raw captures: %w", err)
	}
	return tag.RowsAffected(), nil
}
