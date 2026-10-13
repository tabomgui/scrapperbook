package sweep_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/sweep"
	"scrapperbook/backend/internal/testdb"
)

var now = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func insertListing(t *testing.T, pool *pgxpool.Pool, externalID, status string, lastSeenAt time.Time) int64 {
	t.Helper()
	return insertListingWithPrice(t, pool, externalID, status, lastSeenAt, nil)
}

func insertListingWithPrice(t *testing.T, pool *pgxpool.Pool, externalID, status string, lastSeenAt time.Time, priceCents *int64) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data, price_cents)
		VALUES ('facebook_marketplace', $1, 'https://example/item/'||$1, 'Civic', $2, 'fp-'||$1, 'ch-'||$1,
		        $3, $3, $3, '{}', $4)
		RETURNING id`, externalID, status, lastSeenAt, priceCents).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMarkUnseenFlipsOnlyStaleListings(t *testing.T) {
	pool := testdb.New(t)
	stale := insertListing(t, pool, "stale-1", "active", now.Add(-8*24*time.Hour))
	fresh := insertListing(t, pool, "fresh-1", "active", now.Add(-1*time.Hour))

	n, err := sweep.MarkUnseen(context.Background(), pool, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("marked = %d, want 1", n)
	}

	var staleStatus, freshStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM listings WHERE id = $1`, stale).Scan(&staleStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT status FROM listings WHERE id = $1`, fresh).Scan(&freshStatus); err != nil {
		t.Fatal(err)
	}
	if staleStatus != "unseen" {
		t.Fatalf("stale status = %q, want unseen", staleStatus)
	}
	if freshStatus != "active" {
		t.Fatalf("fresh status = %q, want active (untouched)", freshStatus)
	}
}

func TestMarkUnseenRecordsASnapshot(t *testing.T) {
	pool := testdb.New(t)
	wantPrice := int64(123456)
	id := insertListingWithPrice(t, pool, "stale-2", "active", now.Add(-8*24*time.Hour), &wantPrice)

	if _, err := sweep.MarkUnseen(context.Background(), pool, now, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}

	var status, title string
	var capturedAt time.Time
	var priceCents *int64
	if err := pool.QueryRow(context.Background(),
		`SELECT status, captured_at, price_cents, title FROM listing_snapshots WHERE listing_id = $1`, id).
		Scan(&status, &capturedAt, &priceCents, &title); err != nil {
		t.Fatal(err)
	}
	if status != "unseen" || !capturedAt.Equal(now) {
		t.Fatalf("snapshot status=%q captured_at=%v", status, capturedAt)
	}
	if priceCents == nil || *priceCents != wantPrice {
		t.Fatalf("snapshot price_cents = %v, want %d", priceCents, wantPrice)
	}
	if title != "Civic" {
		t.Fatalf("snapshot title = %q, want Civic", title)
	}
}

func TestMarkUnseenIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	insertListing(t, pool, "already-unseen", "unseen", now.Add(-30*24*time.Hour))

	n, err := sweep.MarkUnseen(context.Background(), pool, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("marked = %d, want 0 (already unseen, no new snapshot)", n)
	}
}
