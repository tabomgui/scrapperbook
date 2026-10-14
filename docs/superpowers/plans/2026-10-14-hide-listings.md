# Hide Listings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user hide a listing they're not interested in — excluded from the default dashboard view, silenced from Discord notifications — without it reappearing on its own.

**Architecture:** A new `listings.hidden` boolean column, a third writer (`catalog.SetListingHidden`) alongside the existing ingest/sweep writers, two verb-action endpoints matching the existing `/resume`/`/run` style, a notification-gating check in `ingest.go`, and a dashboard toggle per listing.

**Tech Stack:** Go (backend/catalog/httpapi/ingest), Postgres migration (goose), Next.js Server Actions (frontend) — same stack as the rest of this project.

**Spec:** `docs/superpowers/specs/2026-10-14-hide-listings-design.md`

---

### Task 1: `hidden` column and catalog support

**Files:**
- Create: `backend/migrations/00002_hide_listings.sql`
- Modify: `backend/internal/catalog/listings.go`
- Test: `backend/internal/catalog/listings_test.go`

- [ ] **Step 1: Criar a migration**

`backend/migrations/00002_hide_listings.sql`:

```sql
-- +goose Up
ALTER TABLE listings ADD COLUMN hidden boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE listings DROP COLUMN hidden;
```

- [ ] **Step 2: Escrever os testes que falham**

Add to `backend/internal/catalog/listings_test.go` (reuses `insertTestListing` and `boolPtr`, the latter already declared in `regions_test.go` in the same package — don't redeclare it):

```go
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
```

- [ ] **Step 3: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/catalog/ -run Hidden -v`
Expected: `catalog.SetListingHidden` undefined, `catalog.ListingFilter` has no field `Hidden`, `Listing` has no field `Hidden`.

- [ ] **Step 4: Implementar**

In `backend/internal/catalog/listings.go`, find:

```go
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
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}
```

Replace with:

```go
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
```

Find:

```go
type ListingFilter struct {
	SearchID *string
	State    *string
	City     *string
	MinPrice *int64
	MaxPrice *int64
	Status   *string
	Sort     string // "first_seen_at"|"last_seen_at"|"price_cents", "-" prefix for descending
	Limit    int    // 0 or >200 falls back to 50
}
```

Replace with:

```go
type ListingFilter struct {
	SearchID *string
	State    *string
	City     *string
	MinPrice *int64
	MaxPrice *int64
	Status   *string
	Hidden   *bool // nil means no filter (both shown)
	Sort     string // "first_seen_at"|"last_seen_at"|"price_cents", "-" prefix for descending
	Limit    int    // 0 or >200 falls back to 50
}
```

Find:

```go
func ListListings(ctx context.Context, pool *pgxpool.Pool, f ListingFilter) ([]Listing, error) {
	query := `SELECT l.id, l.source, l.external_id, l.url, l.title, l.description, l.price_cents,
	                  l.currency, l.city, l.state, l.image_url, l.seller_name, l.status,
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
			&l.Currency, &l.City, &l.State, &l.ImageURL, &l.SellerName, &l.Status,
			&l.FirstSeenAt, &l.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan listing: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
```

Replace with:

```go
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
```

Find:

```go
func GetListing(ctx context.Context, pool *pgxpool.Pool, id int64) (Listing, []Snapshot, error) {
	var l Listing
	err := pool.QueryRow(ctx, `SELECT id, source, external_id, url, title, description, price_cents,
		currency, city, state, image_url, seller_name, status, first_seen_at, last_seen_at
		FROM listings WHERE id = $1`, id).Scan(
		&l.ID, &l.Source, &l.ExternalID, &l.URL, &l.Title, &l.Description, &l.PriceCents,
		&l.Currency, &l.City, &l.State, &l.ImageURL, &l.SellerName, &l.Status, &l.FirstSeenAt, &l.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Listing{}, nil, ErrNotFound
	}
```

Replace with:

```go
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
```

Finally, add this new function anywhere else in the file (e.g. right after `GetListing`):

```go
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
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/catalog/ -v`
Expected: all tests PASS, including the 3 new ones and every pre-existing test in the package.

- [ ] **Step 6: Commit**

```bash
git add backend/migrations/00002_hide_listings.sql backend/internal/catalog/listings.go backend/internal/catalog/listings_test.go
git commit -m "feat(backend): add listings.hidden column and catalog support"
```

---

### Task 2: Suppress notifications for hidden listings

**Files:**
- Modify: `backend/internal/ingest/ingest.go`
- Test: `backend/internal/ingest/ingest_test.go`

- [ ] **Step 1: Escrever o teste que falha**

Add to `backend/internal/ingest/ingest_test.go` (reuses `setup`/`node`/`seenAt`/`e.count` already declared in this file):

```go
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
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/ingest/ -run Hidden -v`
Expected: FAIL — `alerts = 1, want 0` (the price drop notification currently fires regardless of `hidden`).

- [ ] **Step 3: Implementar**

In `backend/internal/ingest/ingest.go`, find:

```go
type upsertResult struct {
	listingID int64
	kind      upsertKind
	oldPrice  *int64 // set only when kind == kindChanged
}
```

Replace with:

```go
type upsertResult struct {
	listingID int64
	kind      upsertKind
	oldPrice  *int64 // set only when kind == kindChanged
	hidden    bool   // the listing's hidden flag as of the load/insert in this call
}
```

Find:

```go
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
```

Replace with:

```go
	var oldHash string
	var oldPrice *int64
	var hidden bool
	if err := tx.QueryRow(ctx, `SELECT id, content_hash, price_cents, hidden FROM listings
		WHERE source = $1 AND external_id = $2 FOR UPDATE`,
		c.Source, c.ExternalID).Scan(&id, &oldHash, &oldPrice, &hidden); err != nil {
		return upsertResult{}, fmt.Errorf("load listing: %w", err)
	}

	if oldHash == hash {
		// Status is part of the hash, so this only revives listings marked 'unseen'.
		if _, err := tx.Exec(ctx, `UPDATE listings
			SET last_seen_at = GREATEST(last_seen_at, $2), status = $3, updated_at = now()
			WHERE id = $1`, id, b.SeenAt, string(c.Status)); err != nil {
			return upsertResult{}, fmt.Errorf("touch listing: %w", err)
		}
		return upsertResult{listingID: id, kind: kindSeen, hidden: hidden}, nil
	}
```

Find:

```go
	if err := insertSnapshot(ctx, tx, id, c, raw, b); err != nil {
		return upsertResult{}, err
	}
	return upsertResult{listingID: id, kind: kindChanged, oldPrice: oldPrice}, nil
}
```

Replace with:

```go
	if err := insertSnapshot(ctx, tx, id, c, raw, b); err != nil {
		return upsertResult{}, err
	}
	return upsertResult{listingID: id, kind: kindChanged, oldPrice: oldPrice, hidden: hidden}, nil
}
```

Find:

```go
		if newToSearch && rule.WantsNew(c) {
```

Replace with:

```go
		if newToSearch && !res.hidden && rule.WantsNew(c) {
```

Find:

```go
		if res.oldPrice != nil && c.PriceCents != nil && rule.WantsPriceDrop(*res.oldPrice, *c.PriceCents, c) {
```

Replace with:

```go
		if !res.hidden && res.oldPrice != nil && c.PriceCents != nil && rule.WantsPriceDrop(*res.oldPrice, *c.PriceCents, c) {
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/ingest/ -v`
Expected: all tests PASS, including the new one and every pre-existing test (confirm none of the existing "new"/"price_drop" alert tests broke — they never set `hidden`, so it defaults to `false` and behaves exactly as before).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/ingest/
git commit -m "feat(backend): suppress notifications for hidden listings"
```

---

### Task 3: `/api/v1/listings` hide/unhide endpoints and default filter

**Files:**
- Modify: `backend/internal/httpapi/public.go`
- Modify: `backend/internal/httpapi/listings.go`
- Modify: `backend/internal/httpapi/public_test.go`

- [ ] **Step 1: Escrever os testes que falham**

In `backend/internal/httpapi/public_test.go`, find the import block:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/httpapi"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/testdb"
	"scrapperbook/backend/internal/workers"
)
```

Replace with:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/httpapi"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/testdb"
	"scrapperbook/backend/internal/workers"
)
```

Add these tests anywhere else in the same file:

```go
func TestHideAndUnhideListing(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	var id int64
	if err := e.pool.QueryRow(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', 'fp1', 'ch1',
		        now(), now(), now(), '{}')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	resp, _ := doJSON(t, http.MethodPost, fmt.Sprintf("%s/api/v1/listings/%d/hide", e.srv.URL, id), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hide status = %d", resp.StatusCode)
	}
	var hidden bool
	if err := e.pool.QueryRow(ctx, `SELECT hidden FROM listings WHERE id = $1`, id).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if !hidden {
		t.Fatal("hidden = false after /hide")
	}

	resp, _ = doJSON(t, http.MethodPost, fmt.Sprintf("%s/api/v1/listings/%d/unhide", e.srv.URL, id), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unhide status = %d", resp.StatusCode)
	}
	if err := e.pool.QueryRow(ctx, `SELECT hidden FROM listings WHERE id = $1`, id).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if hidden {
		t.Fatal("hidden = true after /unhide")
	}
}

func TestHideUnknownListingReturnsNotFound(t *testing.T) {
	e := setupPublic(t)
	resp, _ := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/listings/999999/hide", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestListingsExcludesHiddenByDefaultAndIncludesWithHiddenAll(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, hidden, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', false, 'fp1', 'ch1',
		        now(), now(), now(), '{}'),
		       ('facebook_marketplace', '2', 'https://x/2', 'Civic', 'active', true, 'fp2', 'ch2',
		        now(), now(), now(), '{}')`); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/listings")
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(list) != 1 || list[0]["external_id"] != "1" {
		t.Fatalf("default list = %v, want only non-hidden", list)
	}

	resp, err = http.Get(e.srv.URL + "/api/v1/listings?hidden=all")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var all []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("hidden=all list = %v, want both", all)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/httpapi/ -run Hid -v`
Expected: compile error or 404 — the `/hide`/`/unhide` routes don't exist yet, and `TestListingsExcludesHiddenByDefaultAndIncludesWithHiddenAll` fails because today's default returns both rows.

- [ ] **Step 3: Implementar — `public.go`**

Find, inside `Routes`:

```go
	mux.HandleFunc("GET /api/v1/listings", h.listListings)
	mux.HandleFunc("GET /api/v1/listings/{id}", h.getListing)
```

Replace with:

```go
	mux.HandleFunc("GET /api/v1/listings", h.listListings)
	mux.HandleFunc("GET /api/v1/listings/{id}", h.getListing)
	mux.HandleFunc("POST /api/v1/listings/{id}/hide", h.hideListing)
	mux.HandleFunc("POST /api/v1/listings/{id}/unhide", h.unhideListing)
```

- [ ] **Step 4: Implementar — `listings.go`**

Find:

```go
func (h *Public) listListings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListingFilter{Sort: q.Get("sort")}
	if v := q.Get("search_id"); v != "" {
		f.SearchID = &v
	}
	if v := q.Get("state"); v != "" {
		f.State = &v
	}
	if v := q.Get("city"); v != "" {
		f.City = &v
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
```

Replace with:

```go
func (h *Public) listListings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListingFilter{Sort: q.Get("sort")}
	if v := q.Get("search_id"); v != "" {
		f.SearchID = &v
	}
	if v := q.Get("state"); v != "" {
		f.State = &v
	}
	if v := q.Get("city"); v != "" {
		f.City = &v
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
	switch q.Get("hidden") {
	case "true":
		v := true
		f.Hidden = &v
	case "all":
		f.Hidden = nil
	default:
		v := false
		f.Hidden = &v
	}
```

Add these two handlers anywhere else in the file (e.g. after `getListing`):

```go
func (h *Public) hideListing(w http.ResponseWriter, r *http.Request) {
	h.setListingHidden(w, r, true)
}

func (h *Public) unhideListing(w http.ResponseWriter, r *http.Request) {
	h.setListingHidden(w, r, false)
}

func (h *Public) setListingHidden(w http.ResponseWriter, r *http.Request, hidden bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown listing")
		return
	}
	if err := catalog.SetListingHidden(r.Context(), h.Pool, id, hidden); err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			writeError(w, http.StatusNotFound, "listing not found")
			return
		}
		internalErrorPublic(w, h.Log, "set listing hidden", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/httpapi/ -v`
Expected: all tests PASS — the 3 new ones plus every pre-existing test in the package (in particular, `TestListingsFilterByStateAndPrice` must still pass: it never sets `hidden`, so both its inserted rows default to `hidden = false` and the new default filter doesn't exclude them).

- [ ] **Step 6: Rodar a suíte completa**

Run: `cd backend && go test ./... -count=1`
Expected: every package passes.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/httpapi/
git commit -m "feat(backend): add listing hide/unhide endpoints and default filter"
```

---

### Task 4: Dashboard hide/unhide toggle

**Files:**
- Modify: `frontend/lib/types.ts`
- Create: `frontend/app/listings/actions.ts`
- Modify: `frontend/app/page.tsx`
- Modify: `frontend/app/listings/[id]/page.tsx`

- [ ] **Step 1: Adicionar o campo ao tipo `Listing`**

In `frontend/lib/types.ts`, find:

```ts
export interface Listing {
  id: number;
  source: string;
  external_id: string;
  url: string;
  title: string;
  description?: string;
  price_cents?: number;
  currency?: string;
  city?: string;
  state?: string;
  image_url?: string;
  seller_name?: string;
  status: string;
  first_seen_at: string;
  last_seen_at: string;
}
```

Replace with:

```ts
export interface Listing {
  id: number;
  source: string;
  external_id: string;
  url: string;
  title: string;
  description?: string;
  price_cents?: number;
  currency?: string;
  city?: string;
  state?: string;
  image_url?: string;
  seller_name?: string;
  status: string;
  hidden: boolean;
  first_seen_at: string;
  last_seen_at: string;
}
```

- [ ] **Step 2: Criar as Server Actions**

`frontend/app/listings/actions.ts`:

```ts
"use server";

import { revalidatePath } from "next/cache";
import { apiFetch } from "@/lib/api";

export async function hideListingAction(id: number): Promise<void> {
  await apiFetch(`/api/v1/listings/${id}/hide`, { method: "POST" });
  revalidatePath("/");
  revalidatePath(`/listings/${id}`);
}

export async function unhideListingAction(id: number): Promise<void> {
  await apiFetch(`/api/v1/listings/${id}/unhide`, { method: "POST" });
  revalidatePath("/");
  revalidatePath(`/listings/${id}`);
}
```

- [ ] **Step 3: Atualizar a página de listagem**

`frontend/app/page.tsx` — replace the entire file with:

```tsx
import Link from "next/link";
import { apiFetchList } from "@/lib/api";
import { formatPrice, timeAgo } from "@/lib/format";
import type { Listing, Search } from "@/lib/types";
import { hideListingAction, unhideListingAction } from "./listings/actions";

type SearchParams = {
  search_id?: string;
  state?: string;
  city?: string;
  min_price_reais?: string;
  max_price_reais?: string;
  status?: string;
  hidden?: string;
};

export default async function ListingsPage({
  searchParams,
}: {
  searchParams: Promise<SearchParams>;
}) {
  const params = await searchParams;
  const query = new URLSearchParams();
  if (params.search_id) query.set("search_id", params.search_id);
  if (params.state) query.set("state", params.state);
  if (params.city) query.set("city", params.city);
  if (params.min_price_reais) {
    const reais = Number(params.min_price_reais);
    if (Number.isFinite(reais)) {
      query.set("min_price", String(Math.round(reais * 100)));
    }
  }
  if (params.max_price_reais) {
    const reais = Number(params.max_price_reais);
    if (Number.isFinite(reais)) {
      query.set("max_price", String(Math.round(reais * 100)));
    }
  }
  if (params.status) query.set("status", params.status);
  if (params.hidden) query.set("hidden", params.hidden);

  const [listings, searches] = await Promise.all([
    apiFetchList<Listing>(`/api/v1/listings?${query.toString()}`),
    apiFetchList<Search>("/api/v1/searches"),
  ]);

  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Anúncios</h1>
      <form className="mb-6 flex flex-wrap items-center gap-3 rounded border border-slate-200 bg-white p-4 text-sm">
        <select
          name="search_id"
          defaultValue={params.search_id ?? ""}
          className="rounded border border-slate-300 px-2 py-1"
        >
          <option value="">Todas as buscas</option>
          {searches.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <input
          name="state"
          placeholder="Estado (ex: PR)"
          defaultValue={params.state ?? ""}
          className="w-28 rounded border border-slate-300 px-2 py-1"
        />
        <input
          name="city"
          placeholder="Cidade"
          defaultValue={params.city ?? ""}
          className="w-36 rounded border border-slate-300 px-2 py-1"
        />
        <input
          name="min_price_reais"
          type="number"
          placeholder="Preço mín (R$)"
          defaultValue={params.min_price_reais ?? ""}
          className="w-32 rounded border border-slate-300 px-2 py-1"
        />
        <input
          name="max_price_reais"
          type="number"
          placeholder="Preço máx (R$)"
          defaultValue={params.max_price_reais ?? ""}
          className="w-32 rounded border border-slate-300 px-2 py-1"
        />
        <select name="status" defaultValue={params.status ?? ""} className="rounded border border-slate-300 px-2 py-1">
          <option value="">Todos os status</option>
          <option value="active">Ativo</option>
          <option value="unseen">Sumiu</option>
        </select>
        <label className="flex items-center gap-1">
          <input type="checkbox" name="hidden" value="all" defaultChecked={params.hidden === "all"} />
          mostrar ocultos
        </label>
        <button type="submit" className="rounded bg-slate-900 px-3 py-1 font-medium text-white">
          Filtrar
        </button>
      </form>

      {listings.length === 0 && <p className="text-slate-500">Nenhum anúncio encontrado com esses filtros.</p>}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {listings.map((l) => (
          <div key={l.id} className="rounded border border-slate-200 bg-white p-3 hover:border-slate-400">
            <Link href={`/listings/${l.id}`}>
              <div className="mb-2 flex h-32 items-center justify-center overflow-hidden rounded bg-slate-100">
                {l.image_url ? (
                  // eslint-disable-next-line @next/next/no-img-element -- arbitrary external CDN hosts, next/image would need an allowlist we can't predict
                  <img src={l.image_url} alt={l.title} className="h-full w-full object-cover" />
                ) : (
                  <span className="text-3xl">🚗</span>
                )}
              </div>
              <div className="text-sm font-medium">{l.title}</div>
              <div className="text-xs text-slate-500">
                {formatPrice(l.price_cents)} · {l.city ?? "?"}/{l.state ?? "?"}
              </div>
              <div className="text-xs text-slate-400">{timeAgo(l.last_seen_at)}</div>
            </Link>
            <form action={(l.hidden ? unhideListingAction : hideListingAction).bind(null, l.id)} className="mt-2">
              <button type="submit" className="text-xs text-slate-500 hover:text-slate-900">
                {l.hidden ? "mostrar" : "ocultar"}
              </button>
            </form>
          </div>
        ))}
      </div>
    </div>
  );
}
```

(Note the structural change from the previous version: the card's outer element is now a `<div>`, not a `<Link>` — a `<form>` can't be nested inside an `<a>`, so the clickable title/photo/price area is wrapped in its own `<Link>` inside the card, with the hide/unhide form as a sibling below it, not inside it.)

- [ ] **Step 4: Atualizar a página de detalhe**

`frontend/app/listings/[id]/page.tsx` — replace the entire file with:

```tsx
import { notFound } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";
import { formatPrice } from "@/lib/format";
import type { Listing, Snapshot } from "@/lib/types";
import { hideListingAction, unhideListingAction } from "../actions";

export default async function ListingDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  let data: { listing: Listing; snapshots: Snapshot[] | null };
  try {
    data = await apiFetch<{ listing: Listing; snapshots: Snapshot[] | null }>(`/api/v1/listings/${id}`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }
  const snapshots = data.snapshots ?? [];

  return (
    <div>
      <a href="/" className="text-sm text-slate-500 hover:text-slate-900">
        ← voltar
      </a>
      <h1 className="mt-2 mb-1 text-xl font-semibold">{data.listing.title}</h1>
      <p className="mb-4 text-slate-600">
        {formatPrice(data.listing.price_cents)} · {data.listing.city ?? "?"}/{data.listing.state ?? "?"} · status:{" "}
        {data.listing.status}
      </p>
      {data.listing.image_url && (
        // eslint-disable-next-line @next/next/no-img-element -- arbitrary external CDN hosts
        <img
          src={data.listing.image_url}
          alt={data.listing.title}
          className="mb-4 max-h-80 rounded border border-slate-200 object-cover"
        />
      )}
      <div className="mb-6 flex gap-2">
        <a
          href={data.listing.url}
          target="_blank"
          rel="noreferrer"
          className="inline-block rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white"
        >
          Ver no Facebook Marketplace
        </a>
        <form action={(data.listing.hidden ? unhideListingAction : hideListingAction).bind(null, data.listing.id)}>
          <button
            type="submit"
            className="rounded border border-slate-300 px-3 py-1 text-sm font-medium text-slate-700"
          >
            {data.listing.hidden ? "mostrar" : "ocultar"}
          </button>
        </form>
      </div>

      <h2 className="mb-2 text-lg font-medium">Histórico</h2>
      {snapshots.length === 0 ? (
        <p className="text-slate-500">Sem histórico ainda.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {[...snapshots].reverse().map((s, i) => (
            <li key={i} className="rounded border border-slate-200 bg-white p-2">
              {new Date(s.captured_at).toLocaleString("pt-BR")} — {formatPrice(s.price_cents)} — {s.status}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 5: Verificar**

```bash
cd frontend && npm run build
```
Expected: build succeeds.

```bash
docker compose up -d --build dashboard api
curl -s "http://127.0.0.1:3000/" | grep -o "mostrar ocultos"
curl -s "http://127.0.0.1:3000/" | grep -o "ocultar"
```
(Rebuild `api` too — Tasks 1-3 changed the Go backend, including a new migration that `db.Migrate` applies automatically on startup. Use `127.0.0.1`, not `localhost`, per this project's known host port conflict.)

Expected: both greps find matches.

Then, manually (real browser — Server Actions, same caveat as every other one in this project): click "ocultar" on a listing card, confirm it disappears from the default Anúncios view on reload; check "mostrar ocultos" and filter, confirm it reappears (grayed in no special way, just present) with a "mostrar" button instead; click "mostrar", confirm it's back to normal. Also confirm via `curl -s http://127.0.0.1:8080/api/v1/listings?hidden=all | python3 -m json.tool` or `psql` that the `hidden` column round-trips correctly.

- [ ] **Step 6: Commit**

```bash
git add frontend/lib/types.ts frontend/app/listings/
git commit -m "feat(dashboard): add hide/unhide toggle for listings"
```

---

### Task 5: End-to-end verification

No new code — confirms Tasks 1-4 work together against the real running stack, including the real notification-suppression path (not just the ingest unit test).

- [ ] **Step 1**: `docker compose up -d --build api dashboard` (picks up the migration, the backend changes, and the frontend changes). Confirm via `docker compose logs api --tail 20` that the migration ran without error (goose logs each applied migration).
- [ ] **Step 2**: Pick a real listing from the dashboard, hide it. Confirm it's gone from the default `/` view and confirm `GET http://127.0.0.1:8080/api/v1/listings?hidden=all` still shows it with `"hidden": true`.
- [ ] **Step 3**: Manually insert a price-drop scenario for that same (now-hidden) listing directly via `psql` — update its `price_cents` to a lower value and insert a matching `listing_snapshots` row with the new price, simulating what `ingest.go` would do on a real crawl — then confirm no new row appears in `notifications` for it (query `SELECT * FROM notifications WHERE listing_id = <id>` before and after). This is the one path Task 2's unit test already covers in isolation; this step just confirms the real schema/migration applied against the live database behaves the same way.
- [ ] **Step 4**: Unhide the listing, confirm it reappears in the default view.
- [ ] **Step 5**: `docker compose logs api dashboard --tail 30` — confirm no errors in either container.
- [ ] **Step 6**: Report back: does hide/unhide work end-to-end in the browser, does the default view correctly exclude hidden listings, and does the notification suppression hold against the real database (not just the unit test)?
