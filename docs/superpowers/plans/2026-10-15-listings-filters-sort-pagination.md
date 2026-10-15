# Listings Filters, Sort, Pagination, View Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a publication-date filter, a sort dropdown, numbered pagination, and a grid/list view toggle to the `/` (Anúncios) dashboard page.

**Architecture:** `catalog.ListListings` gains date-range filtering, `Offset`, and now also returns a total row count (same `WHERE` clause, ignoring `LIMIT`/`OFFSET`). `GET /api/v1/listings` changes its response shape from a bare array to `{"listings": [...], "total": N}`. The frontend page (`app/page.tsx`, a server component reading everything from `searchParams`) adds the new form fields, computes `offset` from `?page=N`, and renders either the existing grid or a new compact list layout based on `?view=`.

**Tech Stack:** Go 1.26 / pgx (backend), Next.js App Router server components (frontend). No new dependencies.

---

### Task 1: `catalog.ListListings` — date filter, offset, total count

**Files:**
- Modify: `backend/internal/catalog/listings.go`
- Modify: `backend/internal/catalog/listings_test.go`

- [ ] **Step 1: Write the failing tests**

Add these three tests to the end of `backend/internal/catalog/listings_test.go` (keep the existing `boolPtr` import/helper from `regions_test.go` as-is):

```go
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
```

Also update every existing call site in this file to match the new three-value return (`listings, total, err`) — the `total` is unused by these pre-existing tests, so bind it to `_`:

- Line 38: `got, err := catalog.ListListings(...)` → `got, _, err := catalog.ListListings(...)`
- Line 53: same change
- Line 68: same change
- Line 150: same change
- Line 158: same change
- Line 166: same change

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/catalog/... -run TestListListings -v`
Expected: compile error (`ListListings` still returns 2 values, or `FirstSeenAfter`/`FirstSeenBefore`/`Offset` fields don't exist) — that's the expected failure for this step.

- [ ] **Step 3: Implement**

Replace the whole `ListingFilter` struct and `ListListings` function in `backend/internal/catalog/listings.go` with:

```go
type ListingFilter struct {
	SearchID        *string
	State           *string
	City            *string
	MinPrice        *int64
	MaxPrice        *int64
	Status          *string
	Hidden          *bool // nil means no filter (both shown)
	FirstSeenAfter  *time.Time
	FirstSeenBefore *time.Time // exclusive upper bound: pass the day *after* the last day you want included
	Sort            string     // "first_seen_at"|"last_seen_at"|"price_cents", "-" prefix for descending
	Limit           int        // 0 or >200 falls back to 50
	Offset          int
}
```

```go
// ListListings returns the page of listings matching f, plus the total
// number of rows matching f's filters (ignoring Limit/Offset) — the total
// is what the dashboard needs to render page numbers.
func ListListings(ctx context.Context, pool *pgxpool.Pool, f ListingFilter) ([]Listing, int, error) {
	from := `FROM listings l`
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.SearchID != nil {
		from += ` JOIN search_listings sl ON sl.listing_id = l.id`
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
	if f.FirstSeenAfter != nil {
		where = append(where, "l.first_seen_at >= "+arg(*f.FirstSeenAfter))
	}
	if f.FirstSeenBefore != nil {
		where = append(where, "l.first_seen_at < "+arg(*f.FirstSeenBefore))
	}
	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT count(*) "+from+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count listings: %w", err)
	}

	query := `SELECT l.id, l.source, l.external_id, l.url, l.title, l.description, l.price_cents,
	                  l.currency, l.city, l.state, l.image_url, l.seller_name, l.status, l.hidden,
	                  l.first_seen_at, l.last_seen_at ` + from + whereClause
	query += " ORDER BY " + sortClause(f.Sort)
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, f.Offset)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list listings: %w", err)
	}
	defer rows.Close()
	var out []Listing
	for rows.Next() {
		var l Listing
		if err := rows.Scan(&l.ID, &l.Source, &l.ExternalID, &l.URL, &l.Title, &l.Description, &l.PriceCents,
			&l.Currency, &l.City, &l.State, &l.ImageURL, &l.SellerName, &l.Status, &l.Hidden,
			&l.FirstSeenAt, &l.LastSeenAt); err != nil {
			return nil, 0, fmt.Errorf("scan listing: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/catalog/... -v`
Expected: PASS (all tests in the package, including the 3 new ones and the 6 updated call sites)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/catalog/listings.go backend/internal/catalog/listings_test.go
git commit -m "feat(catalog): add date filter, offset, and total count to ListListings"
```

---

### Task 2: `httpapi` — wire new params, change response shape

**Files:**
- Modify: `backend/internal/httpapi/listings.go`
- Modify: `backend/internal/httpapi/public_test.go`

- [ ] **Step 1: Write the failing tests**

Replace the body of `TestListingsFilterByStateAndPrice` (decoding) in `backend/internal/httpapi/public_test.go` — change:

```go
	var list []map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["external_id"] != "1" {
		t.Fatalf("list = %v", list)
	}
```

to:

```go
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "1" {
		t.Fatalf("body = %+v", body)
	}
```

Replace the body of `TestListingsExcludesHiddenByDefaultAndIncludesWithHiddenAll` — change:

```go
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
```

to:

```go
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if body.Total != 1 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "1" {
		t.Fatalf("default body = %+v, want only non-hidden", body)
	}

	resp, err = http.Get(e.srv.URL + "/api/v1/listings?hidden=all")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var allBody struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&allBody); err != nil {
		t.Fatal(err)
	}
	if allBody.Total != 2 || len(allBody.Listings) != 2 {
		t.Fatalf("hidden=all body = %+v, want both", allBody)
	}
```

Add two new tests to the same file:

```go
func TestListingsFiltersByFirstSeenDateRange(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', 'fp1', 'ch1',
		        '2026-10-01', '2026-10-01', '2026-10-01', '{}'),
		       ('facebook_marketplace', '2', 'https://x/2', 'Civic', 'active', 'fp2', 'ch2',
		        '2026-10-10', '2026-10-10', '2026-10-10', '{}')`); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/listings?first_seen_after=2026-10-05&first_seen_before=2026-10-15")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "2" {
		t.Fatalf("body = %+v, want only external_id=2", body)
	}
}

func TestListingsOffsetAndLimitPaginate(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', 'fp1', 'ch1', now() - interval '2 hours', now(), now(), '{}'),
		       ('facebook_marketplace', '2', 'https://x/2', 'Civic', 'active', 'fp2', 'ch2', now() - interval '1 hour', now(), now(), '{}'),
		       ('facebook_marketplace', '3', 'https://x/3', 'Civic', 'active', 'fp3', 'ch3', now(), now(), now(), '{}')`); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/listings?limit=2&offset=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 3 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "1" {
		t.Fatalf("body = %+v, want total=3 and the oldest row (external_id=1) as the only row past offset 2", body)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/httpapi/... -run TestListings -v`
Expected: FAIL (handler still returns a bare array and doesn't recognize `first_seen_after`/`first_seen_before`/`offset`)

- [ ] **Step 3: Implement**

Replace `listListings` in `backend/internal/httpapi/listings.go`:

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
	if v := q.Get("min_price"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.MinPrice = &n
		}
	}
	if v := q.Get("max_price"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.MaxPrice = &n
		}
	}
	if v := q.Get("first_seen_after"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			f.FirstSeenAfter = &t
		}
	}
	if v := q.Get("first_seen_before"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			// FirstSeenBefore is an exclusive upper bound (see ListingFilter
			// doc comment) — add a day so "before=2026-10-15" includes all
			// of the 15th, not just up to midnight at its start.
			t = t.AddDate(0, 0, 1)
			f.FirstSeenBefore = &t
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			f.Offset = n
		}
	}
	list, total, err := catalog.ListListings(r.Context(), h.Pool, f)
	if err != nil {
		internalErrorPublic(w, h.Log, "list listings", err)
		return
	}
	if list == nil {
		list = []catalog.Listing{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": list, "total": total})
}
```

Add `"time"` to the import block at the top of the file (it currently has `"errors"`, `"net/http"`, `"strconv"`, and `"scrapperbook/backend/internal/catalog"`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/httpapi/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpapi/listings.go backend/internal/httpapi/public_test.go
git commit -m "feat(httpapi): add date filter and offset pagination to GET /listings"
```

---

### Task 3: Frontend — sort dropdown, date filter, response shape

**Files:**
- Modify: `frontend/app/page.tsx`

- [ ] **Step 1: Update the response handling and add the new filter fields**

This step has no separate test (no test framework covers `app/page.tsx` — verified manually in Task 4 via `/run`).

Note on the spec's "invalid page falls back to page 1" requirement: a non-numeric or negative `?page=` value does fall back to 1 (handled below via `Number.isFinite`/`>= 1`). A page number *beyond* `totalPages` (e.g. the API had fewer results than expected) is deliberately **not** redirected — it just renders the existing empty-listings message with the pagination nav still showing the real page numbers, so the user can click back to a valid page. This is simpler than a second round-trip to re-fetch at offset 0 and has the same practical effect (no broken page, no silent surprise redirect).

Replace the whole file `frontend/app/page.tsx` with:

```tsx
import Link from "next/link";
import { apiFetch } from "@/lib/api";
import { formatPrice, timeAgo } from "@/lib/format";
import type { Listing, Search } from "@/lib/types";
import { hideListingAction, unhideListingAction } from "./listings/actions";

const PAGE_SIZE = 24;

type SearchParams = {
  search_id?: string;
  state?: string;
  city?: string;
  min_price_reais?: string;
  max_price_reais?: string;
  status?: string;
  hidden?: string;
  first_seen_after?: string;
  first_seen_before?: string;
  sort?: string;
  page?: string;
  view?: string;
};

const SORT_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "-first_seen_at", label: "Mais recente" },
  { value: "first_seen_at", label: "Mais antigo" },
  { value: "-price_cents", label: "Maior preço" },
  { value: "price_cents", label: "Menor preço" },
  { value: "-last_seen_at", label: "Visto por último" },
  { value: "last_seen_at", label: "Visto há mais tempo" },
];

function withParams(params: SearchParams, overrides: Record<string, string | undefined>): string {
  const merged: Record<string, string | undefined> = { ...params, ...overrides };
  const sp = new URLSearchParams();
  for (const [key, value] of Object.entries(merged)) {
    if (value) sp.set(key, value);
  }
  const qs = sp.toString();
  return qs ? `?${qs}` : "";
}

function pageNumbers(totalPages: number, current: number): Array<number | "…"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const keep = new Set<number>([1, totalPages, current - 2, current - 1, current, current + 1, current + 2]);
  const sorted = [...keep].filter((p) => p >= 1 && p <= totalPages).sort((a, b) => a - b);
  const out: Array<number | "…"> = [];
  let prev = 0;
  for (const p of sorted) {
    if (prev && p - prev > 1) out.push("…");
    out.push(p);
    prev = p;
  }
  return out;
}

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
  if (params.first_seen_after) query.set("first_seen_after", params.first_seen_after);
  if (params.first_seen_before) query.set("first_seen_before", params.first_seen_before);
  if (params.sort) query.set("sort", params.sort);

  const pageNum = Number(params.page);
  const page = Number.isFinite(pageNum) && pageNum >= 1 ? Math.floor(pageNum) : 1;
  query.set("limit", String(PAGE_SIZE));
  query.set("offset", String((page - 1) * PAGE_SIZE));

  const view = params.view === "list" ? "list" : "grid";

  const [{ listings, total }, searches] = await Promise.all([
    apiFetch<{ listings: Listing[]; total: number }>(`/api/v1/listings?${query.toString()}`),
    apiFetch<Search[]>("/api/v1/searches"),
  ]);
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

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
        <label className="flex items-center gap-1 text-slate-500">
          de
          <input
            name="first_seen_after"
            type="date"
            defaultValue={params.first_seen_after ?? ""}
            className="rounded border border-slate-300 px-2 py-1"
          />
        </label>
        <label className="flex items-center gap-1 text-slate-500">
          até
          <input
            name="first_seen_before"
            type="date"
            defaultValue={params.first_seen_before ?? ""}
            className="rounded border border-slate-300 px-2 py-1"
          />
        </label>
        <select name="status" defaultValue={params.status ?? ""} className="rounded border border-slate-300 px-2 py-1">
          <option value="">Todos os status</option>
          <option value="active">Ativo</option>
          <option value="unseen">Sumiu</option>
        </select>
        <select name="sort" defaultValue={params.sort ?? SORT_OPTIONS[0].value} className="rounded border border-slate-300 px-2 py-1">
          {SORT_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <label className="flex items-center gap-1">
          <input type="checkbox" name="hidden" value="all" defaultChecked={params.hidden === "all"} />
          mostrar ocultos
        </label>
        <button type="submit" className="rounded bg-slate-900 px-3 py-1 font-medium text-white">
          Filtrar
        </button>
      </form>

      <div className="mb-4 flex items-center justify-between text-sm">
        <span className="text-slate-500">{total} anúncio{total === 1 ? "" : "s"}</span>
        <div className="flex gap-1">
          <Link
            href={withParams(params, { view: "grid" })}
            className={`rounded border px-2 py-1 ${view === "grid" ? "border-slate-900 bg-slate-900 text-white" : "border-slate-300 text-slate-600"}`}
          >
            grade
          </Link>
          <Link
            href={withParams(params, { view: "list" })}
            className={`rounded border px-2 py-1 ${view === "list" ? "border-slate-900 bg-slate-900 text-white" : "border-slate-300 text-slate-600"}`}
          >
            lista
          </Link>
        </div>
      </div>

      {listings.length === 0 && <p className="text-slate-500">Nenhum anúncio encontrado com esses filtros.</p>}

      {view === "grid" ? (
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
      ) : (
        <div className="flex flex-col gap-2">
          {listings.map((l) => (
            <div
              key={l.id}
              className="flex items-center gap-3 rounded border border-slate-200 bg-white p-2 hover:border-slate-400"
            >
              <Link href={`/listings/${l.id}`} className="flex flex-1 items-center gap-3 overflow-hidden">
                <div className="flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded bg-slate-100">
                  {l.image_url ? (
                    // eslint-disable-next-line @next/next/no-img-element -- arbitrary external CDN hosts
                    <img src={l.image_url} alt={l.title} className="h-full w-full object-cover" />
                  ) : (
                    <span className="text-xl">🚗</span>
                  )}
                </div>
                <div className="flex flex-1 items-center gap-3 overflow-hidden text-sm">
                  <span className="min-w-0 flex-1 truncate font-medium">{l.title}</span>
                  <span className="shrink-0 text-slate-500">{formatPrice(l.price_cents)}</span>
                  <span className="shrink-0 text-slate-500">
                    {l.city ?? "?"}/{l.state ?? "?"}
                  </span>
                  <span className="shrink-0 text-xs text-slate-400">{timeAgo(l.last_seen_at)}</span>
                </div>
              </Link>
              <form action={(l.hidden ? unhideListingAction : hideListingAction).bind(null, l.id)}>
                <button type="submit" className="shrink-0 text-xs text-slate-500 hover:text-slate-900">
                  {l.hidden ? "mostrar" : "ocultar"}
                </button>
              </form>
            </div>
          ))}
        </div>
      )}

      {totalPages > 1 && (
        <nav className="mt-6 flex gap-1 text-sm">
          {pageNumbers(totalPages, page).map((p, i) =>
            p === "…" ? (
              <span key={`ellipsis-${i}`} className="px-2 py-1 text-slate-400">
                …
              </span>
            ) : (
              <Link
                key={p}
                href={withParams(params, { page: String(p) })}
                className={`rounded border px-3 py-1 ${p === page ? "border-slate-900 bg-slate-900 text-white" : "border-slate-300 text-slate-600"}`}
              >
                {p}
              </Link>
            ),
          )}
        </nav>
      )}
    </div>
  );
}
```

- [ ] **Step 2: Verify `lib/types.ts` already matches**

Open `frontend/lib/types.ts` and confirm `Listing` already has `hidden: boolean` (it does, added by the hide-listings feature) — no change needed there.

- [ ] **Step 3: Commit**

```bash
git add frontend/app/page.tsx
git commit -m "feat(dashboard): add date filter, sort, pagination, grid/list view toggle"
```

---

### Task 4: Manual verification

**Files:** none (verification only)

- [ ] **Step 1: Run backend test suite**

Run: `cd backend && go test ./...`
Expected: all packages `ok`

- [ ] **Step 2: Start the app and exercise the new UI**

Use the `run` skill (or `docker compose up -d --build api dashboard` from the project's compose directory — do **not** run `docker compose down`) to start the stack, then open the dashboard's `/` page in a browser and confirm:
- The date inputs ("de"/"até") filter listings by publication date.
- The sort dropdown changes ordering (try "Maior preço" and "Mais antigo").
- "grade"/"lista" toggle switches layout without losing the current filters (check the URL keeps `?state=...` etc. after clicking).
- Numbered pagination appears when there are more than 24 matching listings, and each page number navigates correctly while preserving filters/sort/view.
- Hide/unhide still works in both grid and list view.

- [ ] **Step 3: Report results**

No code changes in this task — just confirm the above manually and report any issues found (if issues are found, they become new tasks or fixes to the tasks above, not silent changes).
