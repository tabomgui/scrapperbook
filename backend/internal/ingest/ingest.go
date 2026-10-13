// Package ingest turns raw crawler items into deduplicated listings,
// snapshots and pending notifications.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"scrapperbook/backend/internal/listing"
	"scrapperbook/backend/internal/metrics"
	"scrapperbook/backend/internal/search"
)

var ErrUnknownSource = errors.New("unknown source")

type Item struct {
	ExternalID string          `json:"external_id"`
	Raw        json.RawMessage `json:"raw"`
}

type Batch struct {
	JobID          string
	Source         string
	SearchID       string
	RegionID       string
	CrawlerVersion string
	ParserVersion  string
	SeenAt         time.Time
	Items          []Item
}

type Stats struct {
	New        int `json:"new"`
	Changed    int `json:"changed"`
	Seen       int `json:"seen"`
	Invalid    int `json:"invalid"`
	Irrelevant int `json:"irrelevant"`
	Alerts     int `json:"alerts"`
}

type Service struct {
	registry Registry
	log      *slog.Logger
}

func New(registry Registry, log *slog.Logger) *Service {
	return &Service{registry: registry, log: log}
}

// Ingest processes a batch inside the caller's transaction.
func (s *Service) Ingest(ctx context.Context, tx pgx.Tx, b Batch) (Stats, error) {
	norm, ok := s.registry[b.Source]
	if !ok {
		return Stats{}, fmt.Errorf("%w: %s", ErrUnknownSource, b.Source)
	}
	var baselineDone bool
	var alertRaw []byte
	var keyword string
	err := tx.QueryRow(ctx, `SELECT sdr.baseline_done, sd.alert, sd.keyword
		FROM search_definition_regions sdr
		JOIN search_definitions sd ON sd.id = sdr.search_id
		WHERE sdr.search_id = $1 AND sdr.region_id = $2`,
		b.SearchID, b.RegionID).Scan(&baselineDone, &alertRaw, &keyword)
	if err != nil {
		return Stats{}, fmt.Errorf("load search context: %w", err)
	}
	rule, err := search.ParseAlertRule(alertRaw)
	if err != nil {
		return Stats{}, err
	}

	var st Stats
	for _, it := range b.Items {
		if !json.Valid(it.Raw) {
			st.Invalid++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "invalid").Inc()
			s.log.Warn("invalid raw json", "job_id", b.JobID, "external_id", it.ExternalID)
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO raw_captures
			(job_id, source, external_id, crawler_version, parser_version, payload, captured_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			b.JobID, b.Source, it.ExternalID, b.CrawlerVersion, b.ParserVersion, it.Raw, b.SeenAt); err != nil {
			return Stats{}, fmt.Errorf("insert raw capture %s/%s: %w", b.Source, it.ExternalID, err)
		}
		c, err := norm.Normalize(it.ExternalID, it.Raw)
		if err != nil {
			st.Invalid++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "invalid").Inc()
			s.log.Warn("normalize failed", "job_id", b.JobID, "external_id", it.ExternalID, "err", err)
			continue
		}
		if !search.TitleMatches(keyword, c.Title) {
			st.Irrelevant++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "irrelevant").Inc()
			continue
		}
		res, err := upsertListing(ctx, tx, c, it.Raw, b)
		if err != nil {
			return Stats{}, fmt.Errorf("upsert listing %s/%s: %w", b.Source, it.ExternalID, err)
		}
		switch res.kind {
		case kindNew:
			st.New++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "new").Inc()
		case kindChanged:
			st.Changed++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "changed").Inc()
		case kindSeen:
			st.Seen++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "seen").Inc()
		}
		newToSearch, err := touchSearchListing(ctx, tx, b.SearchID, res.listingID, b.SeenAt)
		if err != nil {
			return Stats{}, fmt.Errorf("touch search listing %s/%s: %w", b.Source, it.ExternalID, err)
		}
		if !baselineDone {
			continue
		}
		if newToSearch && rule.WantsNew(c) {
			added, err := enqueueNotification(ctx, tx, notification{
				key:  fmt.Sprintf("new:%s:%d", b.SearchID, res.listingID),
				kind: "new", listingID: res.listingID, searchID: b.SearchID, price: c.PriceCents,
			})
			if err != nil {
				return Stats{}, err
			}
			if added {
				st.Alerts++
			}
		}
		if res.oldPrice != nil && c.PriceCents != nil && rule.WantsPriceDrop(*res.oldPrice, *c.PriceCents, c) {
			added, err := enqueueNotification(ctx, tx, notification{
				key:  fmt.Sprintf("price_drop:%s:%d:%d:%d:%s", b.SearchID, res.listingID, *res.oldPrice, *c.PriceCents, b.JobID),
				kind: "price_drop", listingID: res.listingID, searchID: b.SearchID,
				price: c.PriceCents, oldPrice: res.oldPrice,
			})
			if err != nil {
				return Stats{}, err
			}
			if added {
				st.Alerts++
			}
		}
	}

	if !baselineDone {
		if _, err := tx.Exec(ctx, `UPDATE search_definition_regions SET baseline_done = true
			WHERE search_id = $1 AND region_id = $2`, b.SearchID, b.RegionID); err != nil {
			return Stats{}, fmt.Errorf("mark baseline: %w", err)
		}
	}
	return st, nil
}

type upsertKind int

const (
	kindNew upsertKind = iota
	kindChanged
	kindSeen
)

type upsertResult struct {
	listingID int64
	kind      upsertKind
	oldPrice  *int64 // set only when kind == kindChanged
}

func upsertListing(ctx context.Context, tx pgx.Tx, c listing.Canonical, raw json.RawMessage, b Batch) (upsertResult, error) {
	hash := c.ContentHash()
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO listings
		(source, external_id, url, title, description, price_cents, currency,
		 location_text, city, state, lat, lng, image_url, seller_external_id, seller_name,
		 status, fingerprint, content_hash, first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
		        $16, $17, $18, $19, $19, $19, $20)
		ON CONFLICT (source, external_id) DO NOTHING
		RETURNING id`,
		c.Source, c.ExternalID, c.URL, c.Title, nullable(c.Description), c.PriceCents, nullable(c.Currency),
		nullable(c.LocationText), nullable(c.City), nullable(c.State), c.Lat, c.Lng, nullable(c.ImageURL),
		nullable(c.SellerExternalID), nullable(c.SellerName),
		string(c.Status), c.Fingerprint(), hash, b.SeenAt, raw).Scan(&id)
	if err == nil {
		if err := insertSnapshot(ctx, tx, id, c, raw, b); err != nil {
			return upsertResult{}, err
		}
		return upsertResult{listingID: id, kind: kindNew}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return upsertResult{}, fmt.Errorf("insert listing: %w", err)
	}

	var oldHash string
	var oldPrice *int64
	if err := tx.QueryRow(ctx, `SELECT id, content_hash, price_cents FROM listings
		WHERE source = $1 AND external_id = $2 FOR UPDATE`,
		c.Source, c.ExternalID).Scan(&id, &oldHash, &oldPrice); err != nil {
		return upsertResult{}, fmt.Errorf("load listing: %w", err)
	}

	if oldHash == hash {
		// Status is part of the hash, so this only revives listings marked 'unseen'.
		if _, err := tx.Exec(ctx, `UPDATE listings
			SET last_seen_at = GREATEST(last_seen_at, $2), status = $3, updated_at = now()
			WHERE id = $1`, id, b.SeenAt, string(c.Status)); err != nil {
			return upsertResult{}, fmt.Errorf("touch listing: %w", err)
		}
		return upsertResult{listingID: id, kind: kindSeen}, nil
	}

	if _, err := tx.Exec(ctx, `UPDATE listings SET
		url = $2, title = $3, description = $4, price_cents = $5, currency = $6,
		location_text = $7, city = $8, state = $9, lat = $10, lng = $11, image_url = $12,
		seller_external_id = $13, seller_name = $14, status = $15, fingerprint = $16,
		content_hash = $17, last_seen_at = GREATEST(last_seen_at, $18), last_changed_at = GREATEST(last_changed_at, $18),
		raw_data = $19, updated_at = now()
		WHERE id = $1`,
		id, c.URL, c.Title, nullable(c.Description), c.PriceCents, nullable(c.Currency),
		nullable(c.LocationText), nullable(c.City), nullable(c.State), c.Lat, c.Lng, nullable(c.ImageURL),
		nullable(c.SellerExternalID), nullable(c.SellerName), string(c.Status), c.Fingerprint(),
		hash, b.SeenAt, raw); err != nil {
		return upsertResult{}, fmt.Errorf("update listing: %w", err)
	}
	if err := insertSnapshot(ctx, tx, id, c, raw, b); err != nil {
		return upsertResult{}, err
	}
	return upsertResult{listingID: id, kind: kindChanged, oldPrice: oldPrice}, nil
}

func insertSnapshot(ctx context.Context, tx pgx.Tx, listingID int64, c listing.Canonical, raw json.RawMessage, b Batch) error {
	_, err := tx.Exec(ctx, `INSERT INTO listing_snapshots
		(listing_id, price_cents, title, description, status, job_id, captured_at, raw_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		listingID, c.PriceCents, c.Title, nullable(c.Description), string(c.Status), b.JobID, b.SeenAt, raw)
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	metrics.SnapshotsTotal.Inc()
	return nil
}

// touchSearchListing records the sighting and reports whether it is the
// first time this search has seen the listing.
func touchSearchListing(ctx context.Context, tx pgx.Tx, searchID string, listingID int64, at time.Time) (bool, error) {
	var inserted bool
	err := tx.QueryRow(ctx, `INSERT INTO search_listings (search_id, listing_id, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (search_id, listing_id)
		DO UPDATE SET last_seen_at = GREATEST(search_listings.last_seen_at, EXCLUDED.last_seen_at)
		RETURNING (xmax = 0)`, searchID, listingID, at).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("upsert search listing: %w", err)
	}
	return inserted, nil
}

type notification struct {
	key       string
	kind      string
	listingID int64
	searchID  string
	price     *int64
	oldPrice  *int64
}

func enqueueNotification(ctx context.Context, tx pgx.Tx, n notification) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO notifications
		(dedupe_key, kind, listing_id, search_id, price_cents, old_price_cents)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		n.key, n.kind, n.listingID, n.searchID, n.price, n.oldPrice)
	if err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
