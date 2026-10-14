# Run Search Now Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user trigger a search's jobs immediately from the dashboard instead of waiting for the next scheduled tick.

**Architecture:** A new `Scheduler.RunNow` method reuses `Tick`'s existing region-lookup and job-enqueue logic (including its dedup-safe insert) but skips the `next_run_at` schedule check and the reschedule step. A new `/api/v1/searches/{id}/run` endpoint exposes it; a new dashboard button calls it via a Server Action.

**Tech Stack:** Go (backend/httpapi), Next.js Server Actions (frontend) — same stack as the rest of this project, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-14-run-search-now-design.md`

---

### Task 1: `scheduler.RunNow`

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go`
- Test: `backend/internal/scheduler/run_now_test.go` (new file, same package as `scheduler_test.go` — reuses its `newScheduler`/`now` helpers)

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/scheduler/run_now_test.go`:

```go
package scheduler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/testdb"
)

func TestRunNowEnqueuesOneJobPerRegionWithoutRescheduling(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, seed.SearchID, "londrina-pr")
	s := newScheduler(pool)

	var before time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT next_run_at FROM search_definitions WHERE id = $1`, seed.SearchID).Scan(&before); err != nil {
		t.Fatal(err)
	}

	n, err := s.RunNow(context.Background(), seed.SearchID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("enqueued %d, want 2", n)
	}

	var after time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT next_run_at FROM search_definitions WHERE id = $1`, seed.SearchID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("next_run_at changed: before=%v after=%v", before, after)
	}

	var jobCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM jobs WHERE search_id = $1`, seed.SearchID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 2 {
		t.Fatalf("jobs in db = %d, want 2", jobCount)
	}
}

func TestRunNowSkipsRegionsWithAlreadyQueuedJobs(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	s := newScheduler(pool)

	if n, err := s.RunNow(context.Background(), seed.SearchID); err != nil || n != 1 {
		t.Fatalf("first call: n=%d err=%v, want 1, nil", n, err)
	}
	n, err := s.RunNow(context.Background(), seed.SearchID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second call enqueued %d, want 0 (already queued)", n)
	}
}

func TestRunNowReturnsZeroForSearchWithNoLinkedRegions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO search_definitions (name, keyword, frequency, next_run_at, filters, alert)
		VALUES ('Sem regiao', 'Teste', '30 minutes', '2000-01-01T00:00:00Z', '{}', '{}')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	s := newScheduler(pool)

	n, err := s.RunNow(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("enqueued %d for a search with no regions, want 0", n)
	}
}

func TestRunNowReturnsErrSearchNotFoundForUnknownID(t *testing.T) {
	pool := testdb.New(t)
	s := newScheduler(pool)

	_, err := s.RunNow(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, scheduler.ErrSearchNotFound) {
		t.Fatalf("err = %v, want ErrSearchNotFound", err)
	}
}

func TestRunNowWorksEvenWhenSearchIsDisabled(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	if _, err := pool.Exec(context.Background(),
		`UPDATE search_definitions SET enabled = false WHERE id = $1`, seed.SearchID); err != nil {
		t.Fatal(err)
	}
	s := newScheduler(pool)

	n, err := s.RunNow(context.Background(), seed.SearchID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("enqueued %d for a disabled search, want 1 (manual run bypasses enabled)", n)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/scheduler/ -run RunNow -v`
Expected: `s.RunNow` undefined, `scheduler.ErrSearchNotFound` undefined.

- [ ] **Step 3: Implementar**

In `backend/internal/scheduler/scheduler.go`, add `"errors"` to the import block:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
)
```

Then, anywhere after the `dueSearch` type definition (e.g. right before `Tick`), add:

```go
// ErrSearchNotFound is returned by RunNow for an unknown search id.
var ErrSearchNotFound = errors.New("search not found")

// RunNow enqueues jobs for a search's enabled regions immediately, using
// the same dedup-safe Enqueue path Tick uses (so a region with an
// already-queued/running job for this search is silently skipped, not
// duplicated). Unlike Tick: it ignores the search's own enabled flag (a
// disabled search can still be run manually) and never touches
// next_run_at. It does NOT bypass jobs.Queue's claim-time rate limits —
// those still apply to a manually-enqueued job exactly as they do to an
// automatic one.
func (s *Scheduler) RunNow(ctx context.Context, searchID string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var d dueSearch
	err = tx.QueryRow(ctx, `SELECT id, keyword, filters, priority FROM search_definitions
		WHERE id = $1`, searchID).Scan(&d.search.ID, &d.search.Keyword, &d.search.Filters, &d.priority)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrSearchNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load search %s: %w", searchID, err)
	}

	regions, err := regionsFor(ctx, tx, d.search.ID)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, rg := range regions {
		raw, err := json.Marshal(payload{Search: d.search, Region: rg, Limits: s.limits})
		if err != nil {
			return 0, err
		}
		for _, src := range s.sources {
			ok, err := s.queue.Enqueue(ctx, tx, jobs.NewJob{
				Type: "search", Source: src, SearchID: d.search.ID, RegionID: rg.ID,
				Priority: d.priority, Payload: raw,
			})
			if err != nil {
				return 0, err
			}
			if ok {
				created++
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return created, nil
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/scheduler/ -v`
Expected: all tests PASS — the 5 new ones plus every pre-existing test in `scheduler_test.go`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/scheduler/
git commit -m "feat(backend): add Scheduler.RunNow for manual search execution"
```

---

### Task 2: `/api/v1/searches/{id}/run` endpoint

**Files:**
- Modify: `backend/internal/httpapi/public.go`
- Modify: `backend/internal/httpapi/searches.go`
- Modify: `backend/internal/httpapi/public_test.go`
- Modify: `backend/cmd/server/main.go`

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

Find `setupPublic`:

```go
func setupPublic(t *testing.T) publicEnv {
	t.Helper()
	pool := testdb.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := &httpapi.Public{
		Pool: pool, Queue: jobs.New(pool, jobs.Limits{MaxPerHour: 100, Lease: 10 * 60 * 1e9}),
		Workers: workers.New(pool), Log: log,
	}
	mux := http.NewServeMux()
	pub.Routes(mux)
	httpapi.Metrics(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return publicEnv{srv: srv, pool: pool}
}
```

Replace with:

```go
func setupPublic(t *testing.T) publicEnv {
	t.Helper()
	pool := testdb.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	queue := jobs.New(pool, jobs.Limits{MaxPerHour: 100, Lease: 10 * 60 * 1e9})
	sched := scheduler.New(pool, queue, []string{"facebook_marketplace"}, log)
	pub := &httpapi.Public{
		Pool: pool, Queue: queue, Workers: workers.New(pool), Scheduler: sched, Log: log,
	}
	mux := http.NewServeMux()
	pub.Routes(mux)
	httpapi.Metrics(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return publicEnv{srv: srv, pool: pool}
}
```

Then add these two tests anywhere else in the same file:

```go
func TestRunSearchEnqueuesJobs(t *testing.T) {
	e := setupPublic(t)
	seed := testdb.Seed(t, e.pool)

	resp, body := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/searches/"+seed.SearchID+"/run", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["jobs_enqueued"] != float64(1) {
		t.Fatalf("jobs_enqueued = %v", body["jobs_enqueued"])
	}
	var count int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM jobs WHERE search_id = $1`, seed.SearchID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("jobs in db = %d, want 1", count)
	}
}

func TestRunSearchUnknownIDReturnsNotFound(t *testing.T) {
	e := setupPublic(t)
	resp, _ := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/searches/00000000-0000-0000-0000-000000000000/run", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/httpapi/ -run RunSearch -v`
Expected: compile error — `httpapi.Public` has no field `Scheduler`.

- [ ] **Step 3: Implementar — `public.go`**

Find:

```go
import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/workers"
)

type Public struct {
	Pool    *pgxpool.Pool
	Queue   *jobs.Queue
	Workers *workers.Registry
	Log     *slog.Logger
}
```

Replace with:

```go
import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/workers"
)

type Public struct {
	Pool      *pgxpool.Pool
	Queue     *jobs.Queue
	Workers   *workers.Registry
	Scheduler *scheduler.Scheduler
	Log       *slog.Logger
}
```

Find, inside `Routes`:

```go
	mux.HandleFunc("GET /api/v1/searches/{id}", h.getSearch)
	mux.HandleFunc("PATCH /api/v1/searches/{id}", h.updateSearch)
	mux.HandleFunc("DELETE /api/v1/searches/{id}", h.deleteSearch)
```

Replace with:

```go
	mux.HandleFunc("GET /api/v1/searches/{id}", h.getSearch)
	mux.HandleFunc("PATCH /api/v1/searches/{id}", h.updateSearch)
	mux.HandleFunc("DELETE /api/v1/searches/{id}", h.deleteSearch)
	mux.HandleFunc("POST /api/v1/searches/{id}/run", h.runSearch)
```

- [ ] **Step 4: Implementar — `searches.go`**

Find the import block:

```go
import (
	"encoding/json"
	"errors"
	"net/http"

	"scrapperbook/backend/internal/catalog"
)
```

Replace with:

```go
import (
	"encoding/json"
	"errors"
	"net/http"

	"scrapperbook/backend/internal/catalog"
	"scrapperbook/backend/internal/scheduler"
)
```

Add this handler anywhere else in the file (e.g. after `deleteSearch`):

```go
func (h *Public) runSearch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	n, err := h.Scheduler.RunNow(r.Context(), id)
	if errors.Is(err, scheduler.ErrSearchNotFound) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "run search", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"jobs_enqueued": n})
}
```

- [ ] **Step 5: Implementar — `main.go`**

Find:

```go
	public := &httpapi.Public{Pool: pool, Queue: queue, Workers: workers.New(pool), Log: log}
```

Replace with:

```go
	public := &httpapi.Public{Pool: pool, Queue: queue, Workers: workers.New(pool), Scheduler: sched, Log: log}
```

- [ ] **Step 6: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/httpapi/ ./internal/scheduler/ -v`
Expected: all tests PASS. Then: `cd backend && go build ./...` — confirms `main.go`'s edit compiles (it references `sched`, which `run` already defines earlier in that function from plan 1's `sched := scheduler.New(...)` call).

- [ ] **Step 7: Rodar a suíte completa**

Run: `cd backend && go test ./... -count=1`
Expected: every package passes.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/httpapi/ backend/cmd/server/main.go
git commit -m "feat(backend): add POST /api/v1/searches/{id}/run endpoint"
```

---

### Task 3: Dashboard button

**Files:**
- Modify: `frontend/app/searches/actions.ts`
- Modify: `frontend/app/searches/page.tsx`

- [ ] **Step 1: Adicionar a Server Action**

In `frontend/app/searches/actions.ts`, add this function anywhere after the imports (e.g. right after `deleteSearchAction`):

```ts
export async function runSearchAction(id: string): Promise<void> {
  await apiFetch(`/api/v1/searches/${id}/run`, { method: "POST" });
  revalidatePath("/searches");
}
```

(`apiFetch` and `revalidatePath` are already imported at the top of this file — no new imports needed.)

- [ ] **Step 2: Adicionar o botão na tabela**

In `frontend/app/searches/page.tsx`, find the import of the actions module:

```tsx
import { deleteSearchAction } from "./actions";
```

Replace with:

```tsx
import { deleteSearchAction, runSearchAction } from "./actions";
```

Find the searches table's action cell:

```tsx
                <td className="space-x-2 text-right">
                  <Link href={`/searches/${s.id}/edit`} className="text-slate-600 hover:text-slate-900">
                    editar
                  </Link>
                  <form action={deleteSearchAction.bind(null, s.id)} className="inline">
                    <button type="submit" className="text-red-600 hover:text-red-800">
                      excluir
                    </button>
                  </form>
                </td>
```

Replace with:

```tsx
                <td className="space-x-2 text-right">
                  <form action={runSearchAction.bind(null, s.id)} className="inline">
                    <button type="submit" className="text-slate-600 hover:text-slate-900">
                      executar agora
                    </button>
                  </form>
                  <Link href={`/searches/${s.id}/edit`} className="text-slate-600 hover:text-slate-900">
                    editar
                  </Link>
                  <form action={deleteSearchAction.bind(null, s.id)} className="inline">
                    <button type="submit" className="text-red-600 hover:text-red-800">
                      excluir
                    </button>
                  </form>
                </td>
```

- [ ] **Step 3: Verificar**

```bash
cd frontend && npm run build
```
Expected: build succeeds.

```bash
docker compose up -d --build dashboard api
curl -s http://127.0.0.1:3000/searches | grep -o "executar agora"
```
(Rebuild `api` too — Task 2 changed the Go backend. Use `127.0.0.1`, not `localhost`, per this project's known host port-3000 conflict with an unrelated process.)

Expected: the button text appears.

Then, manually (real browser, since this is a Server Action — a plain `curl -X POST` to the page won't invoke it, same caveat as every other Server Action in this project): click "executar agora" on the seeded "Civic EG" search, then confirm via `curl -s http://127.0.0.1:8080/api/v1/jobs | python3 -m json.tool` (or `psql`) that a new job row appeared for that search without waiting for the scheduler tick. Click it again immediately and confirm no duplicate job was created (the existing one is still queued/running).

- [ ] **Step 4: Commit**

```bash
git add frontend/app/searches/
git commit -m "feat(dashboard): add run-now button for searches"
```
