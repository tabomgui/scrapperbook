package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/source/facebook"
	"scrapperbook/backend/internal/testdb"
)

var seenAt = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

func node(id, title, amount string) ingest.Item {
	raw := fmt.Sprintf(`{"id":%q,"marketplace_listing_title":%q,
		"listing_price":{"amount":%q},
		"location":{"reverse_geocode":{"city":"Curitiba","state":"PR"}}}`, id, title, amount)
	return ingest.Item{ExternalID: id, Raw: json.RawMessage(raw)}
}

type env struct {
	pool  *pgxpool.Pool
	svc   *ingest.Service
	seed  testdb.Seeded
	jobID string
}

func setup(t *testing.T) env {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	svc := ingest.New(ingest.NewRegistry(facebook.Normalizer{}),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return env{pool: pool, svc: svc, seed: seed, jobID: testdb.InsertRunningJob(t, pool, seed)}
}

func (e env) run(t *testing.T, at time.Time, items ...ingest.Item) ingest.Stats {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	st, err := e.svc.Ingest(ctx, tx, ingest.Batch{
		JobID: e.jobID, Source: facebook.Source,
		SearchID: e.seed.SearchID, RegionID: e.seed.RegionID,
		CrawlerVersion: "test", ParserVersion: "test", SeenAt: at, Items: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func (e env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBaselineStoresListingsWithoutAlerts(t *testing.T) {
	e := setup(t)
	st := e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"), node("2", "Honda Civic EG5 1994", "21000"))
	if st.New != 2 || st.Alerts != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM listings`); n != 2 {
		t.Fatalf("listings = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM listing_snapshots`); n != 2 {
		t.Fatalf("snapshots = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications`); n != 0 {
		t.Fatalf("notifications = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM raw_captures`); n != 2 {
		t.Fatalf("raw_captures = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM search_definition_regions WHERE baseline_done`); n != 1 {
		t.Fatal("baseline_done not set")
	}
}

func TestIngestIsIdempotent(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Honda Civic EG 1993", "23500"))
	if st.New != 0 || st.Seen != 1 || st.Changed != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM listings`); n != 1 {
		t.Fatalf("listings = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM listing_snapshots`); n != 1 {
		t.Fatalf("snapshots = %d", n)
	}
	var lastSeen time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT last_seen_at FROM listings`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if !lastSeen.Equal(seenAt.Add(time.Hour)) {
		t.Fatalf("last_seen_at = %v", lastSeen)
	}
}

func TestNewListingAfterBaselineAlertsOnce(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Honda Civic EG 1993", "23500"), node("2", "Honda Civic EG5 1994", "21000"))
	if st.New != 1 || st.Alerts != 1 {
		t.Fatalf("stats = %+v", st)
	}
	again := e.run(t, seenAt.Add(2*time.Hour), node("2", "Honda Civic EG5 1994", "21000"))
	if again.Alerts != 0 {
		t.Fatalf("second sighting alerted: %+v", again)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications WHERE kind = 'new'`); n != 1 {
		t.Fatalf("new notifications = %d", n)
	}
}

func TestPriceDropCreatesSnapshotAndAlert(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Honda Civic EG 1993", "21000"))
	if st.Changed != 1 || st.Alerts != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM listing_snapshots`); n != 2 {
		t.Fatalf("snapshots = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications
		WHERE kind = 'price_drop' AND price_cents = 2100000 AND old_price_cents = 2350000`); n != 1 {
		t.Fatal("price_drop notification missing")
	}
}

func TestAlertRespectsMaxPrice(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("2", "Honda Civic EG Si 1994", "45000"))
	if st.New != 1 || st.Alerts != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestInvalidItemsAreSkipped(t *testing.T) {
	e := setup(t)
	bad := ingest.Item{ExternalID: "3", Raw: json.RawMessage(`{"id":"3"}`)}
	notJSON := ingest.Item{ExternalID: "4", Raw: json.RawMessage(`{`)}
	st := e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"), bad, notJSON)
	if st.New != 1 || st.Invalid != 2 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestIrrelevantTitleIsDiscardedEntirely(t *testing.T) {
	e := setup(t)
	st := e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"), node("2", "Fiat Palio 2010", "15000"))
	if st.New != 1 || st.Irrelevant != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM listings`); n != 1 {
		t.Fatalf("listings = %d, want 1 (irrelevant item must not be stored)", n)
	}
	if n := e.count(t, `SELECT count(*) FROM listing_snapshots`); n != 1 {
		t.Fatalf("snapshots = %d, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM search_listings`); n != 1 {
		t.Fatalf("search_listings = %d, want 1 (irrelevant item must not be linked to the search)", n)
	}
}

func TestIrrelevantNewListingNeverAlerts(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("2", "Fiat Palio 2010", "15000"))
	if st.New != 0 || st.Irrelevant != 1 || st.Alerts != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications`); n != 0 {
		t.Fatalf("notifications = %d, want 0", n)
	}
}

func TestHiddenListingPriceDropDoesNotNotify(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	if _, err := e.pool.Exec(context.Background(), `UPDATE listings SET hidden = true WHERE external_id = '1'`); err != nil {
		t.Fatal(err)
	}
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Honda Civic EG 1993", "21000"))
	if st.Changed != 1 {
		t.Fatalf("stats = %+v, want Changed=1", st)
	}
	if st.Alerts != 0 {
		t.Fatalf("alerts = %d, want 0 (listing is hidden)", st.Alerts)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications`); n != 0 {
		t.Fatalf("notifications = %d, want 0", n)
	}
}
