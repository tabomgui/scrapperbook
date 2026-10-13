package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/notify"
	"scrapperbook/backend/internal/testdb"
)

type fakeNotifier struct {
	sent   []notify.Notification
	failOn string // Kind to fail on; "" means never fail
}

func (f *fakeNotifier) Send(ctx context.Context, n notify.Notification) error {
	if f.failOn != "" && n.Kind == f.failOn {
		return errors.New("boom")
	}
	f.sent = append(f.sent, n)
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// insertListingAndNotification creates a minimal listing and one pending
// notification pointing at it, enough for DrainPending to build a message.
func insertListingAndNotification(t *testing.T, pool *pgxpool.Pool, dedupeKey, kind string, priceCents, oldPriceCents *int64) {
	t.Helper()
	ctx := context.Background()
	var listingID int64
	err := pool.QueryRow(ctx, `INSERT INTO listings
		(source, external_id, url, title, price_cents, currency, city, state, image_url,
		 status, fingerprint, content_hash, first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', $1, 'https://example/item/'||$1, 'Civic EG', $2, 'BRL',
		        'Curitiba', 'PR', 'https://example/civic.jpg', 'active', 'fp-'||$1, 'ch-'||$1,
		        now(), now(), now(), '{}')
		RETURNING id`, dedupeKey, priceCents).Scan(&listingID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notifications (dedupe_key, kind, listing_id, price_cents, old_price_cents)
		VALUES ($1, $2, $3, $4, $5)`, dedupeKey, kind, listingID, priceCents, oldPriceCents); err != nil {
		t.Fatal(err)
	}
}

func TestDrainPendingSendsAndMarksSent(t *testing.T) {
	pool := testdb.New(t)
	price := int64(2350000)
	insertListingAndNotification(t, pool, "new-1", "new", &price, nil)

	fake := &fakeNotifier{}
	d := notify.NewDrainer(pool, fake, testLogger())
	sent, err := d.DrainPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(fake.sent) != 1 || fake.sent[0].ListingTitle != "Civic EG" {
		t.Fatalf("notifier received = %+v", fake.sent)
	}

	var sentAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT sent_at FROM notifications WHERE dedupe_key = 'new-1'`).Scan(&sentAt); err != nil {
		t.Fatal(err)
	}
	if sentAt == nil {
		t.Fatal("sent_at still null after a successful send")
	}

	if second, err := d.DrainPending(context.Background()); err != nil || second != 0 {
		t.Fatalf("second drain = %d, %v; want 0, nil (already sent)", second, err)
	}
}

func TestDrainPendingLeavesFailedOnesUnsentAndCountsTheAttempt(t *testing.T) {
	pool := testdb.New(t)
	price, oldPrice := int64(2100000), int64(2350000)
	insertListingAndNotification(t, pool, "drop-1", "price_drop", &price, &oldPrice)

	fake := &fakeNotifier{failOn: "price_drop"}
	d := notify.NewDrainer(pool, fake, testLogger())
	sent, err := d.DrainPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0", sent)
	}

	var sentAt *time.Time
	var attempts int
	if err := pool.QueryRow(context.Background(),
		`SELECT sent_at, attempts FROM notifications WHERE dedupe_key = 'drop-1'`).Scan(&sentAt, &attempts); err != nil {
		t.Fatal(err)
	}
	if sentAt != nil {
		t.Fatal("sent_at should still be null after a failed send")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDrainPendingSendsSystemAlertsWithPlainMessage(t *testing.T) {
	pool := testdb.New(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO notifications (dedupe_key, kind, message)
		VALUES ('sys-1', 'system', 'facebook_marketplace: sessão precisa de atenção')`); err != nil {
		t.Fatal(err)
	}
	fake := &fakeNotifier{}
	d := notify.NewDrainer(pool, fake, testLogger())
	sent, err := d.DrainPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || fake.sent[0].Message != "facebook_marketplace: sessão precisa de atenção" {
		t.Fatalf("sent=%d msg=%q", sent, fake.sent[0].Message)
	}
	if fake.sent[0].ListingTitle != "" {
		t.Fatalf("system alert should have no listing title, got %q", fake.sent[0].ListingTitle)
	}
}
