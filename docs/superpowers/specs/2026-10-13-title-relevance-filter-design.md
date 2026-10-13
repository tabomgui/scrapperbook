# Title Relevance Filter — Design

**Problem:** A search's `keyword` (e.g. "Civic hatch") controls what the crawler asks Facebook Marketplace for, but nothing in the backend checks whether a returned listing's title actually matches that keyword. Facebook's own search can return loosely-related results (wrong model, accessories, unrelated "hatch" items), and those currently flow straight through to storage and Discord notifications as if they were a genuine match.

**Goal:** Reject listings whose title doesn't contain every word of the search's keyword, before any database write happens for that item in that batch.

## Scope

In scope: a title/keyword relevance check inside `backend/internal/ingest`, applied per ingest batch (which is always tied to one `search_id`/`region_id`).

Out of scope (explicitly deferred, not part of this change):
- `search_definitions.filters` is stored but never read by any backend code today (confirmed via investigation). Not touched here — the user's reported problem is title relevance, not price/filter enforcement.
- Accent/diacritic normalization (e.g. "Pálio" vs "Palio") and synonym handling (e.g. "hatch" vs "hatchback") — plain case-insensitive substring matching only, for this first version.
- Any change to the crawler (Python side) — filtering happens entirely in the Go backend, after the crawler has already returned results.

## Design

### Matching rule

`search.TitleMatches(keyword, title string) bool` (new function, `backend/internal/search` package, alongside the existing `alert.go`):
- Lowercase both `keyword` and `title`.
- Split `keyword` into words on whitespace (`strings.Fields`).
- Return `true` only if every word is a substring of the lowercased title (`strings.Contains`). Empty keyword (shouldn't happen — `CreateSearch` already requires a non-empty keyword — but handled defensively) matches everything.

This is deliberately naive: no word-boundary checking (so "hatch" matches "hatchback", which is desired), no accent folding, no synonyms. Good enough for the reported problem (completely unrelated titles slipping through); can be revisited if it proves too strict or too loose in practice.

### Where it plugs into ingest

`backend/internal/ingest/ingest.go`'s per-batch query that already loads `sd.alert` (joined by `sdr.search_id`/`region_id` for the batch's `SearchID`/`RegionID`) will also select `sd.keyword`. For each item in the batch, immediately after the existing JSON-validity/normalize checks (so a structurally invalid item is still counted as `invalid`, not `irrelevant` — invalidity is checked first) and before any dedup/DB-write logic:

```
if !search.TitleMatches(keyword, c.Title) {
    st.Irrelevant++
    metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "irrelevant").Inc()
    continue
}
```

A rejected item gets **no** database write at all in this batch: no `listings` insert/update, no `listing_snapshots` row, no `search_listings` touch, no notification. This matches the explicit choice to discard rather than store-but-suppress — there's no audit trail of what was rejected, by design (accepted tradeoff: harder to retroactively tune the filter, but simpler and matches what was asked for).

Note: if the same physical listing was already stored previously (e.g. because an earlier, differently-worded search matched it), this batch's rejection doesn't touch or remove that existing row — it only means *this* job's batch does nothing with the item, as if the crawler hadn't returned it at all.

### Stats / observability

`ingest.Stats` (already has `New`/`Changed`/`Seen`/`Invalid` counters) gains one more: `Irrelevant`. Logged the same way the others are. The new `"irrelevant"` label value on `metrics.ListingsIngestedTotal` gives visibility into how much of a search's crawl results are being filtered out — useful for judging whether a keyword is too broad.

## Testing

Unit tests for `search.TitleMatches` (table-driven: exact match, extra words in title okay, missing word fails, case-insensitivity, substring-within-word like "hatch"/"hatchback", empty keyword).

Extend `backend/internal/ingest`'s existing test suite with a case where a batch item's title doesn't match the batch's search keyword: confirm zero DB writes for that item (no listing row, no notification) and the `Irrelevant` counter/metric increments.

## Error handling

No new error paths — `TitleMatches` is a pure, panic-free string function. No change to how a missing/malformed `keyword` column would behave (not possible today: `CreateSearch` already rejects an empty keyword at creation time, and the column is `NOT NULL`).
