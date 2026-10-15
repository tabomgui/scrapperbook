# Jobs visibility: persist result stats, show search/region/duration/results

## Context

`/health` already has a "Jobs" section (source, status, attempts, error, created_at), but it's not enough to answer "is a job running right now, and what did it find":

- `jobs.search_id`/`region_id` are shown nowhere, and wouldn't be useful raw (UUID/text IDs, not names) even if they were.
- `started_at`/`finished_at` exist on the row and in `JobSummary` but aren't rendered — no way to tell how long a job ran or whether it's still in flight.
- A job's actual crawl result (how many listings were new, changed, already-seen, invalid, irrelevant, and how many fired an alert) is computed on every successful job (`ingest.Stats`) but only ever reaches a `slog.Info("job result ingested", ...)` line — never written to the database. The only way to see it today is grepping container logs, which is exactly the friction being solved here.

## Goals

1. Persist each job's `ingest.Stats` (new/changed/seen/invalid/irrelevant/alerts) once it completes.
2. Show search name and region name instead of raw IDs in the Jobs table.
3. Show duration (or "running for Xs" while in flight) and the result counts in the Jobs table.

## Non-goals

- No new page — this extends the existing `/health` Jobs section (per [[2026-10-15-listings-filters-sort-pagination-design]]'s pattern of improving an existing screen rather than always adding new ones; explicitly chosen over a new `/jobs` page).
- No auto-refresh / polling — the page stays plain server-rendered, reloaded manually like the rest of this dashboard.
- No change to what counts as job success/failure, retry logic, or the `jobs` status machine itself.
- No change to the crawler (Python) side — it already sends `stats` in its result payload; this only changes how the Go backend uses the already-computed `ingest.Stats`, not how that data is gathered.

## Design

### 1. Schema

New migration `00003_job_result_stats.sql`:

```sql
-- +goose Up
ALTER TABLE jobs
    ADD COLUMN result_new        integer,
    ADD COLUMN result_changed    integer,
    ADD COLUMN result_seen       integer,
    ADD COLUMN result_invalid    integer,
    ADD COLUMN result_irrelevant integer,
    ADD COLUMN result_alerts     integer;

-- +goose Down
ALTER TABLE jobs
    DROP COLUMN result_new,
    DROP COLUMN result_changed,
    DROP COLUMN result_seen,
    DROP COLUMN result_invalid,
    DROP COLUMN result_irrelevant,
    DROP COLUMN result_alerts;
```

All six columns are nullable `integer` — `NULL` means "this job never completed with a result" (queued/running/dead), not "zero". The six names map 1:1 to `ingest.Stats`'s existing fields (`New`, `Changed`, `Seen`, `Invalid`, `Irrelevant`, `Alerts`).

### 2. Persisting the result

`jobs.Queue` gains one new method, alongside `Complete` in `lifecycle.go` (same file — it's the same "a job finished" moment, just a second UPDATE after the first):

```go
// RecordResult stores a completed job's ingest outcome, inside the
// caller's transaction (same tx as Complete + ingest.Service.Ingest).
func (q *Queue) RecordResult(ctx context.Context, tx pgx.Tx, jobID string, new, changed, seen, invalid, irrelevant, alerts int) error
```

`httpapi.Internal.result` (the `/internal/v1/jobs/{id}/result` handler) calls this right after `h.Ingest.Ingest(...)` succeeds and before `tx.Commit(ctx)` — same transaction, so a job is never left "succeeded but no result recorded" or vice versa. The existing `slog.Info("job result ingested", ...)` log line is unchanged (kept for container-log debugging, no longer the only source of this data).

### 3. Names instead of raw IDs

`jobs.JobSummary` gains `SearchName *string` and `RegionName *string` (nullable, since a job's `search_id`/`region_id` are themselves nullable for a hypothetical future non-search job type). `jobs.ListJobs`'s query gains two `LEFT JOIN`s:

```sql
LEFT JOIN search_definitions sd ON sd.id = jobs.search_id
LEFT JOIN search_regions sr ON sr.id = jobs.region_id
```

selecting `sd.name AS search_name, sr.name AS region_name` alongside the existing columns.

### 4. `JobSummary` also gains the six result fields

`ResultNew *int`, `ResultChanged *int`, `ResultSeen *int`, `ResultInvalid *int`, `ResultIrrelevant *int`, `ResultAlerts *int` — straight columns, no join needed.

### 5. Frontend

`frontend/lib/types.ts`'s `JobSummary` gets the new fields (`search_name?`, `region_name?`, `result_new?`, etc. — all optional, matching the nullable backend columns).

`frontend/app/health/page.tsx`'s Jobs table gains three columns and changes one:

| Column (new/changed) | Content |
|---|---|
| Busca | `j.search_name ?? "—"` |
| Região | `j.region_name ?? "—"` |
| Duração | if `finished_at` set: `formatDuration(seconds between started_at and finished_at)`; else if `status === "running"` and `started_at` set: `"rodando há " + formatDuration(seconds between started_at and now)`; else `"—"` (covers `queued`/`dead` with no `started_at`) |
| Encontrados | if `result_new != null` (i.e. a result was recorded): `"${result_new} novos · ${result_changed} alterados · ${result_alerts} alertas"`; else `"—"` |

`Fonte`/`Status`/`Tentativas`/`Erro`/`Criado em` columns are unchanged. No new filter controls are added (the existing status/error_kind filters already cover "show me running jobs" via `?status=running`).

A small `formatDuration(totalSeconds: number): string` helper is added to `frontend/lib/format.ts` (alongside the existing `formatPrice`/`timeAgo`), reused by both the "finished in Xs" and "running for Xs" cases:

- `< 60`: `"${totalSeconds}s"` (e.g. `"23s"`)
- `< 3600`: `"${Math.floor(totalSeconds / 60)}min"` (e.g. `"3min"`, seconds dropped — this table doesn't need second-level precision past a minute)
- `>= 3600`: `"${Math.floor(totalSeconds / 3600)}h ${Math.floor((totalSeconds % 3600) / 60)}min"` (e.g. `"1h 5min"`)

### Error handling

- A job that fails (`dead` status) or is still `queued`/`running` without ever reaching `result` simply has all six `result_*` columns `NULL` — rendered as "—", not an error.
- `RecordResult` runs inside the same transaction as `Complete`/`Ingest`; if it fails, the whole transaction rolls back (same behavior as any other write in that handler today) — a job never ends up "succeeded" with no way to ever learn its result stats.

### Testing

- `jobs`: a test that `RecordResult` + `ListJobs` round-trips all six values correctly, and that a job with no recorded result shows `nil` for all six fields.
- `jobs`: a test that `ListJobs` returns the correct `search_name`/`region_name` (including the `NULL`/`"—"`-equivalent case when a job has no search/region).
- `httpapi`: a test that `POST /internal/v1/jobs/{id}/result` persists the six result columns (query the DB directly after calling the endpoint, same pattern as existing `internal_test.go` tests).
- Frontend: no test framework covers `app/health/page.tsx` (consistent with the rest of this app) — verified manually via `/run` after implementation.
