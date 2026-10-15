package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Listing struct {
	ID          int64     `json:"id"`
	Source      string    `json:"source"`
	ExternalID  string    `json:"external_id"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Description *string   `json:"description,omitempty"`
	PriceCents  *int64    `json:"price_cents,omitempty"`
	Currency    *string   `json:"currency,omitempty"`
	City        *string   `json:"city,omitempty"`
	State       *string   `json:"state,omitempty"`
	ImageURL    *string   `json:"image_url,omitempty"`
	SellerName  *string   `json:"seller_name,omitempty"`
	Status      string    `json:"status"`
	Hidden      bool      `json:"hidden"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

type Snapshot struct {
	PriceCents *int64    `json:"price_cents,omitempty"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	CapturedAt time.Time `json:"captured_at"`
}

type ListingFilter struct {
	SearchID *string
	State    *string
	City     *string
	MinPrice *int64
	MaxPrice *int64
	Status   *string
	Hidden   *bool  // nil means no filter (both shown)
	Sort     string // "first_seen_at"|"last_seen_at"|"price_cents", "-" prefix for descending
	Limit    int    // 0 or >200 falls back to 50
}

var validSortColumns = map[string]string{
	"first_seen_at": "l.first_seen_at",
	"last_seen_at":  "l.last_seen_at",
	"price_cents":   "l.price_cents",
}

// sortClause only ever returns a hardcoded column name from the map above,
// never user input, regardless of what Sort contains — the WHERE/ORDER BY
// clause is built from literal SQL fragments plus $N placeholders, user
// *values* only ever reach the query through the parametrized args slice.
func sortClause(sort string) string {
	desc := strings.HasPrefix(sort, "-")
	col := strings.TrimPrefix(sort, "-")
	sqlCol, ok := validSortColumns[col]
	if !ok {
		return "l.first_seen_at DESC" // unrecognized sort: default, newest first
	}
	if desc {
		return sqlCol + " DESC"
	}
	return sqlCol + " ASC"
}

func ListListings(ctx context.Context, pool *pgxpool.Pool, f ListingFilter) ([]Listing, error) {
	query := `SELECT l.id, l.source, l.external_id, l.url, l.title, l.description, l.price_cents,
	                  l.currency, l.city, l.state, l.image_url, l.seller_name, l.status, l.hidden,
	                  l.first_seen_at, l.last_seen_at
	           FROM listings l`
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.SearchID != nil {
		query += ` JOIN search_listings sl ON sl.listing_id = l.id`
		where = append(where, "sl.search_id = "+arg(*f.SearchID))
	}
	if f.State != nil {
		where = append(where, "l.state = "+arg(*f.State))
	}
	if f.City != nil {
		where = append(where, "l.city = "+arg(*f.City))
	}
	if f.MinPrice != nil {
		where = append(where, "l.price_cents >= "+arg(*f.MinPrice))
	}
	if f.MaxPrice != nil {
		where = append(where, "l.price_cents <= "+arg(*f.MaxPrice))
	}
	if f.Status != nil {
		where = append(where, "l.status = "+arg(*f.Status))
	}
	if f.Hidden != nil {
		where = append(where, "l.hidden = "+arg(*f.Hidden))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY " + sortClause(f.Sort)
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list listings: %w", err)
	}
	defer rows.Close()
	var out []Listing
	for rows.Next() {
		var l Listing
		if err := rows.Scan(&l.ID, &l.Source, &l.ExternalID, &l.URL, &l.Title, &l.Description, &l.PriceCents,
			&l.Currency, &l.City, &l.State, &l.ImageURL, &l.SellerName, &l.Status, &l.Hidden,
			&l.FirstSeenAt, &l.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan listing: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func GetListing(ctx context.Context, pool *pgxpool.Pool, id int64) (Listing, []Snapshot, error) {
	var l Listing
	err := pool.QueryRow(ctx, `SELECT id, source, external_id, url, title, description, price_cents,
		currency, city, state, image_url, seller_name, status, hidden, first_seen_at, last_seen_at
		FROM listings WHERE id = $1`, id).Scan(
		&l.ID, &l.Source, &l.ExternalID, &l.URL, &l.Title, &l.Description, &l.PriceCents,
		&l.Currency, &l.City, &l.State, &l.ImageURL, &l.SellerName, &l.Status, &l.Hidden, &l.FirstSeenAt, &l.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Listing{}, nil, ErrNotFound
	}
	if err != nil {
		return Listing{}, nil, fmt.Errorf("get listing %d: %w", id, err)
	}

	rows, err := pool.Query(ctx, `SELECT price_cents, title, status, captured_at
		FROM listing_snapshots WHERE listing_id = $1 ORDER BY captured_at`, id)
	if err != nil {
		return Listing{}, nil, fmt.Errorf("list snapshots for %d: %w", id, err)
	}
	defer rows.Close()
	var snaps []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(&s.PriceCents, &s.Title, &s.Status, &s.CapturedAt); err != nil {
			return Listing{}, nil, fmt.Errorf("scan snapshot for %d: %w", id, err)
		}
		snaps = append(snaps, s)
	}
	return l, snaps, rows.Err()
}

// SetListingHidden is the third writer to listings, alongside ingest.Service
// and sweep.MarkUnseen — a user-driven flag, independent of the
// crawler-owned status column.
func SetListingHidden(ctx context.Context, pool *pgxpool.Pool, id int64, hidden bool) error {
	tag, err := pool.Exec(ctx, `UPDATE listings SET hidden = $2, updated_at = now() WHERE id = $1`, id, hidden)
	if err != nil {
		return fmt.Errorf("set listing %d hidden=%v: %w", id, hidden, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
