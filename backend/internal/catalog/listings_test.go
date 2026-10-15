package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/catalog"
	"scrapperbook/backend/internal/testdb"
)

func insertTestListing(t *testing.T, pool *pgxpool.Pool, externalID, city, state string, priceCents int64, seenAt time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO listings
		(source, external_id, url, title, price_cents, currency, city, state, status,
		 fingerprint, content_hash, first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', $1, 'https://example/item/'||$1, 'Civic', $2, 'BRL', $3, $4,
		        'active', 'fp-'||$1, 'ch-'||$1, $5, $5, $5, '{}')
		RETURNING id`, externalID, priceCents, city, state, seenAt).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListListingsFiltersByStateAndPrice(t *testing.T) {
	pool := testdb.New(t)
	now := time.Now().UTC()
	insertTestListing(t, pool, "1", "Curitiba", "PR", 2000000, now)
	insertTestListing(t, pool, "2", "Curitiba", "PR", 4000000, now)
	insertTestListing(t, pool, "3", "São Paulo", "SP", 2000000, now)

	pr := "PR"
	max := int64(3000000)
	got, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{State: &pr, MaxPrice: &max})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ExternalID != "1" {
		t.Fatalf("got = %+v", got)
	}
}

func TestListListingsSortsNewestFirstByDefault(t *testing.T) {
	pool := testdb.New(t)
	now := time.Now().UTC()
	insertTestListing(t, pool, "older", "Curitiba", "PR", 1000000, now.Add(-time.Hour))
	insertTestListing(t, pool, "newer", "Curitiba", "PR", 1000000, now)

	got, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ExternalID != "newer" {
		t.Fatalf("got = %+v, want newer first", got)
	}
}

func TestListListingsRejectsUnknownSortColumnSafely(t *testing.T) {
	pool := testdb.New(t)
	insertTestListing(t, pool, "1", "Curitiba", "PR", 1000000, time.Now().UTC())

	// An unrecognized sort value must not error or inject SQL — it silently
	// falls back to the default order.
	got, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Sort: "'; DROP TABLE listings; --"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got = %+v", got)
	}
}

func TestGetListingWithSnapshots(t *testing.T) {
	pool := testdb.New(t)
	id := insertTestListing(t, pool, "1", "Curitiba", "PR", 2350000, time.Now().UTC())
	if _, err := pool.Exec(context.Background(), `INSERT INTO listing_snapshots
		(listing_id, price_cents, title, status, captured_at, raw_data)
		VALUES ($1, 2350000, 'Civic', 'active', now(), '{}')`, id); err != nil {
		t.Fatal(err)
	}

	l, snaps, err := catalog.GetListing(context.Background(), pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if l.ExternalID != "1" {
		t.Errorf("listing = %+v", l)
	}
	if len(snaps) != 1 || *snaps[0].PriceCents != 2350000 {
		t.Fatalf("snapshots = %+v", snaps)
	}
}

func TestGetListingNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, _, err := catalog.GetListing(context.Background(), pool, 999999)
	if err != catalog.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSetListingHiddenTogglesFlag(t *testing.T) {
	pool := testdb.New(t)
	id := insertTestListing(t, pool, "1", "Curitiba", "PR", 2000000, time.Now().UTC())

	if err := catalog.SetListingHidden(context.Background(), pool, id, true); err != nil {
		t.Fatal(err)
	}
	l, _, err := catalog.GetListing(context.Background(), pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Hidden {
		t.Fatal("hidden = false, want true")
	}

	if err := catalog.SetListingHidden(context.Background(), pool, id, false); err != nil {
		t.Fatal(err)
	}
	l, _, err = catalog.GetListing(context.Background(), pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Hidden {
		t.Fatal("hidden = true, want false")
	}
}

func TestSetListingHiddenNotFoundForUnknownID(t *testing.T) {
	pool := testdb.New(t)
	err := catalog.SetListingHidden(context.Background(), pool, 999999, true)
	if err != catalog.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListListingsFiltersByHidden(t *testing.T) {
	pool := testdb.New(t)
	now := time.Now().UTC()
	visible := insertTestListing(t, pool, "1", "Curitiba", "PR", 2000000, now)
	hidden := insertTestListing(t, pool, "2", "Curitiba", "PR", 2000000, now)
	if err := catalog.SetListingHidden(context.Background(), pool, hidden, true); err != nil {
		t.Fatal(err)
	}

	all, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("no filter: got %d, want 2 (both)", len(all))
	}

	onlyVisible, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Hidden: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyVisible) != 1 || onlyVisible[0].ID != visible {
		t.Fatalf("hidden=false: got %+v, want only %d", onlyVisible, visible)
	}

	onlyHidden, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Hidden: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyHidden) != 1 || onlyHidden[0].ID != hidden {
		t.Fatalf("hidden=true: got %+v, want only %d", onlyHidden, hidden)
	}
}
