# Run Search Now — Design

**Problem:** Editing a search (keyword, regions, price cap) only takes effect on the next automatic tick (`scheduler.Tick`, which fires per search at `next_run_at`, up to `frequency_seconds` away). There's no way to see the effect of an edit without waiting for that schedule.

**Goal:** A manual "run now" action that enqueues crawl jobs for a search immediately, without touching its automatic schedule.

## Scope

In scope: one new backend method, one new `/api/v1` endpoint, one new dashboard button + Server Action.

Out of scope:
- Rate-limit bypass. `jobs.Queue`'s `MaxPerHour`/`MinGap`/quiet-hours are enforced at claim time (when a worker picks up a job), not at enqueue time — a manually-triggered job is claimed under the exact same budget as an automatic one. This feature only skips the *scheduling wait*, never the safety limits.
- Any UI feedback on how many jobs were actually created (e.g., a toast showing "3 jobs enqueued" or a warning when 0 were because of existing in-flight jobs). Matches the existing silent-action pattern already used by "excluir" (delete search) and "retomar" (resume session) in this dashboard — click, the action fires, the page revalidates, no inline message.
- Changing `next_run_at`. A manual run and the next automatic run can land close together; this is accepted, not a bug (explicit decision).
- Allowing a disabled region to run (regions still need `enabled = true` and a non-null `fb_location`, exactly as `scheduler.regionsFor` already requires for automatic ticks) — only the *search's* own `enabled` flag is bypassed, not the region's.

## Design

### Backend: `scheduler.Scheduler.RunNow`

New method on the existing `Scheduler` struct (`backend/internal/scheduler/scheduler.go`), reusing its already-held `pool`/`queue`/`sources`/`limits` fields — no new struct, no new config.

```go
func (s *Scheduler) RunNow(ctx context.Context, searchID string) (int, error)
```

Behavior:
1. Load the search by ID: `SELECT id, keyword, filters, priority FROM search_definitions WHERE id = $1` — **no `enabled` filter** (per the explicit decision: a disabled search can still be run manually). `pgx.ErrNoRows` → a new sentinel `scheduler.ErrSearchNotFound`.
2. Reuse `regionsFor(ctx, tx, searchID)` (already used by `Tick`) to get enabled regions with a non-null `fb_location`.
3. For each region × each configured source, call `s.queue.Enqueue` with the same `payload` shape `Tick` already builds (`searchPayload`/`regionPayload`/`PageLimits`) — this is the same dedup-safe insert `Tick` uses, so a search with an already-queued/running job for a given region+source simply doesn't get a duplicate (silent no-op for that pair, consistent with `Tick`'s own behavior).
4. Does **not** touch `next_run_at`.
5. Returns the count of jobs actually created (0 if every region already had an in-flight job, or if the search has no enabled regions with a usable `fb_location`).

Runs in its own transaction (`pool.Begin`/`Commit`), separate from `Tick`'s — this is a one-off manual action, not part of the periodic batch.

### Backend: `/api/v1` endpoint

New handler on `httpapi.Public` (`backend/internal/httpapi/searches.go`): `POST /api/v1/searches/{id}/run`.

`Public` gains a `Scheduler *scheduler.Scheduler` field (currently it only has `Pool`/`Queue`/`Workers`/`Log`) — wired in `main.go` alongside the existing `sched := scheduler.New(...)` call, which `Public` doesn't currently reference at all.

Handler: `isUUID` guard (404 on malformed id, same pattern as `getSearch`/`updateSearch`/`deleteSearch`) → `s.Scheduler.RunNow(ctx, id)` → `errors.Is(err, scheduler.ErrSearchNotFound)` → 404 → else 200 with `{"jobs_enqueued": N}`.

### Frontend

`frontend/app/searches/actions.ts` gains `runSearchAction(id: string): Promise<void>` — `POST /api/v1/searches/${id}/run`, then `revalidatePath("/searches")` (matches `resumeSessionAction`'s shape in `app/health/actions.ts` exactly: no `ActionResult`, no try/catch, a failure propagates to `app/error.tsx`).

`frontend/app/searches/page.tsx`'s searches table gains an "executar agora" button next to "editar"/"excluir" in each row, a `<form action={runSearchAction.bind(null, s.id)}>` with a submit button — same shape as the existing "excluir" form in the same table.

## Testing

Backend: table-driven tests for `RunNow` in a new `backend/internal/scheduler/run_now_test.go` — covers: enqueues one job per enabled region (reusing `testdb.Seed`/`testdb.AddRegion`/`testdb.LinkRegion` helpers already used by `scheduler_test.go`), returns `0` and no error when regions exist but a job is already queued for all of them, returns `0` when the search has no linked regions, returns `ErrSearchNotFound` for an unknown id, does NOT change `next_run_at`, works identically whether the search is `enabled` or not.

`httpapi`: extend `public_test.go` with a case posting to `/api/v1/searches/{id}/run` and asserting `jobs_enqueued` in the response plus a row appearing in `jobs`; a 404 case for an unknown id.

Frontend: manual verification against the running stack (same as every other dashboard task in this project — no automated frontend test suite).
