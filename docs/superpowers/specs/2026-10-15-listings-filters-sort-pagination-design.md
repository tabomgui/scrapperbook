# Listings page: date filter, sorting, pagination, view mode

## Context

The `/` (Anúncios) dashboard page already supports filtering by search,
state, city, price range, status, and hidden. It lacks: filtering by
publication date, a way to sort results, pagination beyond a hard 50-row
cap, and any alternative to the grid-of-cards layout. With ~100+ listings
accumulating across 5 recurring searches, the single unpaginated grid is
getting hard to scan.

## Goals

1. Filter listings by publication date range (`first_seen_at`).
2. Sort listings by date (first seen, last seen) or price, either
   direction.
3. Paginate with clickable page numbers instead of a silent 50-row cutoff.
4. Toggle between the existing grid view and a new compact row/list view.

## Non-goals

- Changing how listings are ingested, deduped, or matched to searches.
- Infinite scroll / client-side state — everything stays server-rendered
  via URL query params, consistent with the rest of this page.
- Remembering the user's view-mode preference across sessions (it's a URL
  param; no cookie/localStorage).

## Design

### 1. Date filter

Two new optional query params, `first_seen_after` and `first_seen_before`
(format `YYYY-MM-DD`), rendered as two `<input type="date">` fields in the
existing filter form. Parsed into `time.Time` (start of day UTC for
`after`, end of day UTC for `before`) in `httpapi.listListings`, passed to
a new `ListingFilter.FirstSeenAfter/FirstSeenBefore *time.Time`. Invalid
date strings are silently ignored (same tolerance pattern already used for
`min_price_reais`/`max_price_reais`).

### 2. Sorting

A `<select name="sort">` added to the filter form with six options, all
already supported by `catalog.validSortColumns` — no backend sort-logic
change needed, only wiring the dropdown's value through as the existing
`sort` query param:

| Label                  | value            |
|------------------------|------------------|
| Mais recente (padrão)  | `-first_seen_at` |
| Mais antigo            | `first_seen_at`  |
| Maior preço            | `-price_cents`   |
| Menor preço            | `price_cents`    |
| Visto por último       | `-last_seen_at`  |
| Visto há mais tempo    | `last_seen_at`   |

### 3. Pagination

**API contract change**: `GET /api/v1/listings` currently returns a bare
JSON array. It will instead return `{"listings": [...], "total": N}`,
where `total` is the count of rows matching the filters (ignoring
limit/offset). This is the only consumer of the bare-array shape in the
frontend (`app/page.tsx`); it switches from `apiFetchList` to a one-off
`apiFetch<{listings: Listing[]; total: number}>` call. No other endpoint
or backend caller is affected.

Backend:
- `ListingFilter` gains `Offset int` (existing `Limit int` is reused
  as-is — no new limit param needed).
- `ListListings` runs a second query — same `WHERE` clause, `SELECT
  count(*)` — to get `total`, then applies `LIMIT $limit OFFSET $offset`
  to the existing query.

Frontend:
- New `?page=N` param (1-indexed, default 1).
- Frontend computes `offset = (page-1) * 24` and sends `limit=24&offset=<n>`
  as query params on every request to `GET /api/v1/listings`.
- `totalPages = Math.ceil(total / 24)`.
- Page-number nav below the listing grid/list: numbered links
  `?page=1`, `?page=2`, etc., current page highlighted, all other filters
  preserved in the link's query string. For `totalPages > 7`, show first
  page, last page, current page ±2, and `…` elsewhere (standard
  truncated pagination — avoids a 50-wide number strip at current data
  volumes, headroom for growth).
- Changing any filter/sort resets to page 1 (new form submission always
  omits `page`).

### 4. View mode

New `?view=grid|list` param, default `grid`. Two small toggle links/buttons
above the results (e.g. a grid-icon / list-icon pair), each linking to the
same page with `view` flipped and all other params preserved.

- **Grid** (existing): unchanged card grid.
- **List** (new): one full-width row per listing — small fixed-size
  thumbnail (or 🚗 placeholder) on the left, then title, price · city/state,
  and "visto há Xh" in a horizontal line, hide/unhide button on the right.
  Same click-through to `/listings/{id}` as the grid card.

### Error handling

- Invalid `page` (non-numeric, <1, or beyond `totalPages`) falls back to
  page 1 — same silent-fallback tolerance as the existing invalid-sort
  handling.
- Invalid date strings in the date filter inputs are ignored (filter not
  applied), not an error.

### Testing

- `catalog`: `ListListings` test for `Offset` (page 2 returns the next
  slice) and for `FirstSeenAfter`/`FirstSeenBefore` filtering, plus a test
  that `total` count ignores `Limit`/`Offset` but respects all `WHERE`
  filters.
- `httpapi`: test that `GET /api/v1/listings` response shape is now
  `{listings, total}`; test date-filter and `offset`/`page`-adjacent query
  params parse correctly (and invalid ones are ignored).
- Frontend: no test framework currently exercises `app/page.tsx` (no
  existing tests to extend) — verified manually via `/run` after
  implementation, per existing project practice for this page.
