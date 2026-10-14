# Hide Listings — Design

**Problem:** The Anúncios page shows every stored listing. There's no way to dismiss a listing the user has already decided isn't what they want, short of it disappearing from Facebook entirely (`status = 'unseen'`).

**Goal:** A manual, per-listing "hide" flag, independent of the crawler-owned `status` column, that (a) is excluded from the dashboard's default view and (b) stops generating Discord notifications for that listing.

## Scope

In scope: one new column, two action endpoints, a dashboard toggle, notification suppression for hidden listings.

Out of scope:
- Bulk hide (hide-all-matching-filter) — one listing at a time, matches how every other per-row action in this dashboard already works (resume session, run search, delete search).
- Auto-unhide on any event (e.g. price drop, status change) — once hidden, stays hidden until the user explicitly unhides it.
- Any change to `listings.status` or the crawler/ingest pipeline's existing new/changed/seen/unseen semantics — `hidden` is a fully separate, user-owned flag layered on top.

## Design

### Data

`backend/migrations/00002_hide_listings.sql` (goose): `ALTER TABLE listings ADD COLUMN hidden boolean NOT NULL DEFAULT false` (`Down`: drop the column).

### Backend

`catalog/listings.go` (currently documented as read-only — that comment gets updated, since this adds a third writer alongside `ingest.Service` and `sweep.MarkUnseen`):
- `Listing.Hidden bool` field.
- `SetListingHidden(ctx, pool, id int64, hidden bool) error` — a plain `UPDATE listings SET hidden = $2 WHERE id = $1`, `ErrNotFound` if no row matched.
- `ListingFilter.Hidden *bool` — nil means no filter (both shown), else filters on the exact value.

`httpapi`: two new action endpoints on `Public`, matching the existing verb-action style already used for `/resume` and `/run` (not a generic PATCH, since this is a single boolean flip, not a multi-field update):
- `POST /api/v1/listings/{id}/hide`
- `POST /api/v1/listings/{id}/unhide`

Both: parse the id as int64 (404 on parse failure, same as `getListing`), call `SetListingHidden`, `ErrNotFound` → 404, else 204.

`GET /api/v1/listings`'s query-param handling changes: a `hidden` param of `"true"` sets `Hidden: &true`; `"all"` leaves `Hidden: nil` (no filter); anything else (including absent) sets `Hidden: &false` — so the default, with no param at all, is "hidden ones excluded," matching the explicit design decision.

### Notification suppression

`ingest.go`'s `upsertListing` already does a `SELECT ... FOR UPDATE` of the existing row (for `oldHash`/`oldPrice`) on the non-new path. That `SELECT` gains `hidden`, and `upsertResult` gains a `hidden bool` field (always `false` on the brand-new-insert path, since a listing can't be hidden before it's ever existed). In `Ingest`'s per-item loop, both notification gates — the "new" check and the "price drop" check — add `&& !res.hidden` to their existing condition, so a hidden listing never enqueues a notification, whether that's its first sighting under a different search or a price change on one already seen. `touchSearchListing` and the `st.New`/`st.Changed`/`st.Seen` counters are untouched — hiding only suppresses the *notification*, not the normal tracking/stats.

### Frontend

- `lib/types.ts`'s `Listing` gains `hidden: boolean`.
- A "ocultar"/"mostrar" button on each card (`app/page.tsx`) and on the detail page (`app/listings/[id]/page.tsx`), each a `<form>` Server Action (`hideListingAction`/`unhideListingAction` in a new `app/listings/actions.ts`) calling the matching endpoint then `revalidatePath` for the current page.
- The Anúncios filter form gains a checkbox, "mostrar ocultos" — unchecked (default) sends no `hidden` param (API defaults to excluding them); checked sends `hidden=all`.

## Testing

Backend: `catalog` gets a test for `SetListingHidden` (toggle, `ErrNotFound` for unknown id) and a `ListListings` test confirming the three-way `Hidden` filter behavior (nil/true/false). `ingest` gets a test confirming a hidden listing's price drop produces zero notifications (otherwise would have). `httpapi` gets tests for both new endpoints (success + 404) and confirms `GET /api/v1/listings` excludes hidden by default and includes them with `?hidden=all`.

Frontend: manual verification against the running stack, same as every other dashboard feature in this project.
