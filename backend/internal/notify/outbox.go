// Outbox drain: reads pending notifications (sent_at IS NULL), joins the
// listing data a Notifier needs, sends each, and marks it sent on success.
// A send failure leaves sent_at NULL for the next drain to retry (attempts
// is incremented either way, purely for observability — at this volume, a
// handful of alerts a day for one user, a fixed per-drain retry with no
// backoff is simple and sufficient; revisit only if that stops being true).
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/metrics"
)

const drainBatchSize = 20

type Drainer struct {
	pool     *pgxpool.Pool
	notifier Notifier
	log      *slog.Logger
	Clock    func() time.Time
}

func NewDrainer(pool *pgxpool.Pool, notifier Notifier, log *slog.Logger) *Drainer {
	return &Drainer{pool: pool, notifier: notifier, log: log, Clock: time.Now}
}

type pendingNotification struct {
	id int64
	n  Notification
}

// DrainPending sends every pending notification once and returns how many
// were sent successfully.
func (d *Drainer) DrainPending(ctx context.Context) (int, error) {
	batch, err := d.loadPending(ctx)
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, p := range batch {
		if err := d.notifier.Send(ctx, p.n); err != nil {
			metrics.NotificationsTotal.WithLabelValues(p.n.Kind, "failed").Inc()
			d.log.Warn("notification send failed", "notification_id", p.id, "kind", p.n.Kind, "err", err)
			if _, uerr := d.pool.Exec(ctx, `UPDATE notifications SET attempts = attempts + 1 WHERE id = $1`, p.id); uerr != nil {
				d.log.Error("record notification attempt", "notification_id", p.id, "err", uerr)
			}
			continue
		}
		// If Send succeeded but this UPDATE fails (e.g. a transient DB blip),
		// sent_at stays NULL and the next drain re-sends this notification,
		// producing a user-visible duplicate Discord message. Same tradeoff
		// as above: at this volume, simple and sufficient; not worth a
		// dedup/idempotency mechanism.
		if _, err := d.pool.Exec(ctx, `UPDATE notifications SET sent_at = $2, attempts = attempts + 1 WHERE id = $1`,
			p.id, d.Clock()); err != nil {
			return sent, fmt.Errorf("mark notification %d sent: %w", p.id, err)
		}
		metrics.NotificationsTotal.WithLabelValues(p.n.Kind, "sent").Inc()
		sent++
	}
	return sent, nil
}

func (d *Drainer) loadPending(ctx context.Context) ([]pendingNotification, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT n.id, n.kind, n.message, n.price_cents, n.old_price_cents,
		       l.title, l.url, l.image_url, l.city, l.state
		FROM notifications n
		LEFT JOIN listings l ON l.id = n.listing_id
		WHERE n.sent_at IS NULL
		ORDER BY n.created_at
		LIMIT $1`, drainBatchSize)
	if err != nil {
		return nil, fmt.Errorf("select pending notifications: %w", err)
	}
	defer rows.Close()

	var batch []pendingNotification
	for rows.Next() {
		var p pendingNotification
		var message, title, url, image, city, state *string
		if err := rows.Scan(&p.id, &p.n.Kind, &message, &p.n.PriceCents, &p.n.OldPriceCents,
			&title, &url, &image, &city, &state); err != nil {
			return nil, fmt.Errorf("scan pending notification: %w", err)
		}
		p.n.Message, p.n.ListingTitle, p.n.ListingURL, p.n.ImageURL, p.n.City, p.n.State =
			deref(message), deref(title), deref(url), deref(image), deref(city), deref(state)
		batch = append(batch, p)
	}
	return batch, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
