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
	got, _, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{State: &pr, MaxPrice: &max})
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

	got, _, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{})
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
	got, _, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Sort: "'; DROP TABLE listings; --"})
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

	all, _, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("no filter: got %d, want 2 (both)", len(all))
	}

	onlyVisible, _, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Hidden: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyVisible) != 1 || onlyVisible[0].ID != visible {
		t.Fatalf("hidden=false: got %+v, want only %d", onlyVisible, visible)
	}

	onlyHidden, _, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Hidden: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyHidden) != 1 || onlyHidden[0].ID != hidden {
		t.Fatalf("hidden=true: got %+v, want only %d", onlyHidden, hidden)
	}
}

func TestListListingsFiltersByFirstSeenDateRange(t *testing.T) {
	pool := testdb.New(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	insertTestListing(t, pool, "too-early", "Curitiba", "PR", 1000000, base.AddDate(0, 0, -1))
	insertTestListing(t, pool, "in-range-1", "Curitiba", "PR", 1000000, base)
	insertTestListing(t, pool, "in-range-2", "Curitiba", "PR", 1000000, base.AddDate(0, 0, 2))
	insertTestListing(t, pool, "too-late", "Curitiba", "PR", 1000000, base.AddDate(0, 0, 10))

	after := base
	before := base.AddDate(0, 0, 3) // exclusive upper bound: "in-range-2" day is included, "too-late" is not
	got, total, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{
		FirstSeenAfter: &after, FirstSeenBefore: &before,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	got2 := []string{got[0].ExternalID, got[1].ExternalID}
	if len(got) != 2 || got2[0] != "in-range-2" || got2[1] != "in-range-1" {
		t.Fatalf("got = %+v, want [in-range-2, in-range-1] (newest first)", got)
	}
}

func TestListListingsOffsetPaginates(t *testing.T) {
	pool := testdb.New(t)
	now := time.Now().UTC()
	insertTestListing(t, pool, "a", "Curitiba", "PR", 1000000, now.Add(-3*time.Hour))
	insertTestListing(t, pool, "b", "Curitiba", "PR", 1000000, now.Add(-2*time.Hour))
	insertTestListing(t, pool, "c", "Curitiba", "PR", 1000000, now.Add(-1*time.Hour))

	page1, total, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(page1) != 2 || page1[0].ExternalID != "c" || page1[1].ExternalID != "b" {
		t.Fatalf("page1 = %+v", page1)
	}

	page2, total2, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 3 {
		t.Fatalf("total2 = %d, want 3", total2)
	}
	if len(page2) != 1 || page2[0].ExternalID != "a" {
		t.Fatalf("page2 = %+v", page2)
	}
}

func TestListListingsTotalCountRespectsFiltersIgnoresLimit(t *testing.T) {
	pool := testdb.New(t)
	now := time.Now().UTC()
	insertTestListing(t, pool, "1", "Curitiba", "PR", 1000000, now)
	insertTestListing(t, pool, "2", "Curitiba", "PR", 1000000, now)
	insertTestListing(t, pool, "3", "São Paulo", "SP", 1000000, now)

	pr := "PR"
	got, total, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{State: &pr, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (filtered by state, ignoring limit=1)", total)
	}
	if len(got) != 1 {
		t.Fatalf("got = %+v, want exactly 1 row (limit applied to rows, not to total)", got)
	}
}
