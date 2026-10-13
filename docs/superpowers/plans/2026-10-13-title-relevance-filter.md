# Title Relevance Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject listings whose title doesn't contain every word of a search's keyword, before any meaningful database write happens for that item — so a broad keyword like "Civic hatch" stops generating storage/notifications for obviously-unrelated Facebook Marketplace results.

**Architecture:** A small, pure matching function in `backend/internal/search` (sibling to the existing `AlertRule`), wired into `backend/internal/ingest`'s per-item loop right after normalization (once the listing's title is known) and before any upsert/notification logic.

**Tech Stack:** Go 1.26 (same backend module), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-13-title-relevance-filter-design.md`

---

### Task 1: `search.TitleMatches`

**Files:**
- Create: `backend/internal/search/relevance.go`
- Test: `backend/internal/search/relevance_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/search/relevance_test.go`:

```go
package search_test

import (
	"testing"

	"scrapperbook/backend/internal/search"
)

func TestTitleMatchesRequiresEveryKeywordWord(t *testing.T) {
	cases := []struct {
		name    string
		keyword string
		title   string
		want    bool
	}{
		{"all words present", "Civic hatch", "Honda Civic Hatch 2015", true},
		{"extra words in title are fine", "Civic hatch", "Vendo Honda Civic Hatch LX 2015 completo", true},
		{"case insensitive", "Civic hatch", "HONDA CIVIC HATCH", true},
		{"missing one word fails", "Civic hatch", "Honda Civic Sedan 2015", false},
		{"missing all words fails", "Civic hatch", "Fiat Palio 2010", false},
		{"substring-within-word counts (hatch matches hatchback)", "Civic hatch", "Honda Civic Hatchback", true},
		{"single-word keyword", "Civic", "Honda Civic EXL", true},
		{"empty keyword matches everything", "", "anything at all", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := search.TitleMatches(c.keyword, c.title)
			if got != c.want {
				t.Errorf("TitleMatches(%q, %q) = %v, want %v", c.keyword, c.title, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/search/ -run TitleMatches -v`
Expected: `package scrapperbook/backend/internal/search` compiles but `search.TitleMatches` is undefined.

- [ ] **Step 3: Implementar**

`backend/internal/search/relevance.go`:

```go
package search

import "strings"

// TitleMatches reports whether every word of keyword appears somewhere in
// title (case-insensitive, substring match — "hatch" matches "hatchback").
// This is deliberately naive: no accent folding, no synonyms, no word-
// boundary checking. It exists to reject listings a crawler's own search
// returned that are obviously unrelated to what was asked for (wrong
// model, accessories, etc.) — not to be a precise relevance ranker.
func TitleMatches(keyword, title string) bool {
	title = strings.ToLower(title)
	for _, word := range strings.Fields(strings.ToLower(keyword)) {
		if !strings.Contains(title, word) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/search/ -v`
Expected: all tests PASS, including every pre-existing test in the package (`alert_test.go`, if present) plus the new table-driven `TestTitleMatchesRequiresEveryKeywordWord` (8 sub-tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/search/relevance.go backend/internal/search/relevance_test.go
git commit -m "feat(backend): add title relevance matching for search keywords"
```

---

### Task 2: Wire the relevance check into `ingest.Ingest`

This task both adds new behavior and updates existing `ingest` test fixtures. The package's shared test fixture, `testdb.Seed`, creates a search with keyword `"Honda Civic EG"` (used verbatim by several other packages' tests that assert on that exact string — do not change `testdb.Seed`). Several of `ingest_test.go`'s existing `node(...)` calls use titles like `"Civic EG"` (missing the word "Honda") that would now be rejected as irrelevant, breaking those tests' `New`/`Changed`/`Alerts` assertions. Those titles must be updated to include all three keyword words so the existing tests keep testing what they were testing before this change (dedup, alerts, price drops — not relevance).

**Files:**
- Modify: `backend/internal/ingest/ingest.go`
- Modify: `backend/internal/ingest/ingest_test.go`

- [ ] **Step 1: Atualizar os fixtures existentes para incluir todas as palavras da keyword**

In `backend/internal/ingest/ingest_test.go`, every `node(id, title, amount)` call's `title` must contain "honda", "civic" and "eg" (case-insensitive, as substrings) so it keeps matching `testdb.Seed`'s keyword `"Honda Civic EG"` once the relevance filter is wired in. Apply these exact renames (every other argument and every other line stays unchanged):

In `TestBaselineStoresListingsWithoutAlerts`:
```go
	st := e.run(t, seenAt, node("1", "Civic EG", "23500"), node("2", "Civic EG5", "21000"))
```
becomes:
```go
	st := e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"), node("2", "Honda Civic EG5 1994", "21000"))
```

In `TestIngestIsIdempotent`, both occurrences:
```go
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Civic EG", "23500"))
```
become:
```go
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Honda Civic EG 1993", "23500"))
```

In `TestNewListingAfterBaselineAlertsOnce`, all three occurrences:
```go
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Civic EG", "23500"), node("2", "Civic EG5", "21000"))
	again := e.run(t, seenAt.Add(2*time.Hour), node("2", "Civic EG5", "21000"))
```
become:
```go
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Honda Civic EG 1993", "23500"), node("2", "Honda Civic EG5 1994", "21000"))
	again := e.run(t, seenAt.Add(2*time.Hour), node("2", "Honda Civic EG5 1994", "21000"))
```

In `TestPriceDropCreatesSnapshotAndAlert`, both occurrences:
```go
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Civic EG", "21000"))
```
become:
```go
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Honda Civic EG 1993", "21000"))
```

In `TestAlertRespectsMaxPrice`:
```go
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("2", "Civic Si", "45000"))
```
becomes:
```go
	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("2", "Honda Civic EG Si 1994", "45000"))
```

In `TestInvalidItemsAreSkipped`:
```go
	st := e.run(t, seenAt, node("1", "Civic EG", "23500"), bad, notJSON)
```
becomes:
```go
	st := e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"), bad, notJSON)
```

- [ ] **Step 2: Rodar e confirmar que os testes existentes ainda passam (ainda sem a mudança de implementação)**

Run: `cd backend && go test ./internal/ingest/ -v`
Expected: all existing tests still PASS (this step only touched test fixtures, not `ingest.go` — this confirms the renamed titles didn't accidentally break anything on their own before the relevance filter exists).

- [ ] **Step 3: Escrever os testes que falham para o novo comportamento**

Add to `backend/internal/ingest/ingest_test.go`:

```go
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
```

- [ ] **Step 4: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/ingest/ -run Irrelevant -v`
Expected: both new tests FAIL to compile — `ingest.Stats` has no field `Irrelevant`.

- [ ] **Step 5: Implementar**

In `backend/internal/ingest/ingest.go`, add the new counter to `Stats`:

```go
type Stats struct {
	New        int `json:"new"`
	Changed    int `json:"changed"`
	Seen       int `json:"seen"`
	Invalid    int `json:"invalid"`
	Irrelevant int `json:"irrelevant"`
	Alerts     int `json:"alerts"`
}
```

Find:
```go
	var baselineDone bool
	var alertRaw []byte
	err := tx.QueryRow(ctx, `SELECT sdr.baseline_done, sd.alert
		FROM search_definition_regions sdr
		JOIN search_definitions sd ON sd.id = sdr.search_id
		WHERE sdr.search_id = $1 AND sdr.region_id = $2`,
		b.SearchID, b.RegionID).Scan(&baselineDone, &alertRaw)
	if err != nil {
		return Stats{}, fmt.Errorf("load search context: %w", err)
	}
```
Replace with:
```go
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
```

Find:
```go
		c, err := norm.Normalize(it.ExternalID, it.Raw)
		if err != nil {
			st.Invalid++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "invalid").Inc()
			s.log.Warn("normalize failed", "job_id", b.JobID, "external_id", it.ExternalID, "err", err)
			continue
		}
		res, err := upsertListing(ctx, tx, c, it.Raw, b)
```
Replace with:
```go
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
```

(The `search` package is already imported in this file — it's used for `search.ParseAlertRule` a few lines above.)

In `backend/internal/httpapi/internal.go`, find the log line that reports ingest stats:
```go
	h.Log.Info("job result ingested", "job_id", id, "worker_id", req.WorkerID, "attempt", req.Attempt,
		"new", stats.New, "changed", stats.Changed, "seen", stats.Seen,
		"invalid", stats.Invalid, "alerts", stats.Alerts, "crawler_stats", string(req.Stats))
```
Replace with:
```go
	h.Log.Info("job result ingested", "job_id", id, "worker_id", req.WorkerID, "attempt", req.Attempt,
		"new", stats.New, "changed", stats.Changed, "seen", stats.Seen,
		"invalid", stats.Invalid, "irrelevant", stats.Irrelevant, "alerts", stats.Alerts, "crawler_stats", string(req.Stats))
```

- [ ] **Step 6: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/ingest/ ./internal/httpapi/ -v`
Expected: every test in both packages PASS — all pre-existing `ingest` tests (now using renamed fixtures), the 2 new relevance tests, and every pre-existing `httpapi` test (the log-line change has no test assertions tied to it, so nothing there should break; `stats["new"]` is the only field any `httpapi` test checks on this response, per earlier inspection).

- [ ] **Step 7: Rodar a suíte completa do backend**

Run: `cd backend && go test ./... -count=1`
Expected: every package PASSes — this change touches `ingest` (used by `httpapi`) and nothing else, but running the full suite confirms no indirect breakage.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/ingest/ingest.go backend/internal/ingest/ingest_test.go backend/internal/httpapi/internal.go
git commit -m "feat(backend): discard listings whose title doesn't match the search keyword"
```

---

### Task 3: Verificação manual contra a stack rodando

- [ ] **Step 1**: `docker compose up -d --build api` (pega a mudança nova).
- [ ] **Step 2**: Confirmar que a busca real do usuário (keyword tipo "Civic hatch", múltiplas regiões) continua rodando sem erro: `docker compose logs api --tail 20` — sem panics, sem erros de query.
- [ ] **Step 3**: Depois do próximo ciclo de crawl, checar quantos itens foram descartados como irrelevantes: `docker compose exec postgres psql -U monitor -d monitor -c "SELECT status FROM jobs ORDER BY created_at DESC LIMIT 5;"` e nos logs do `api`, procurar por `"irrelevant"` nas linhas `"job result ingested"` — confirmar que o número não é zero quando o crawler realmente trouxe lixo, e confirmar visualmente (comparando com os anúncios reais) que o que foi descartado de fato não tinha relação com "Civic hatch".
