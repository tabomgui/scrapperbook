# Jobs Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist each job's crawl result (new/changed/seen/invalid/irrelevant/alerts counts), and show search name, region name, duration, and that result in the `/health` dashboard's Jobs table.

**Architecture:** A new migration adds six nullable `result_*` integer columns to `jobs`. `jobs.Queue` gains a `RecordResult` method, called from the existing `/internal/v1/jobs/{id}/result` handler in the same transaction as `Complete`/`Ingest`. `jobs.ListJobs` gains two `LEFT JOIN`s (to `search_definitions`/`search_regions`) so `JobSummary` carries human-readable names instead of raw IDs. The frontend's existing Jobs table (in `app/health/page.tsx`) gains four columns built from data that already exists in `JobSummary` once this is wired through.

**Tech Stack:** Go 1.26 / pgx (backend), Next.js App Router server components (frontend). No new dependencies.

---

### Task 1: Schema + `jobs.Queue.RecordResult` + names in `ListJobs`

**Files:**
- Create: `backend/migrations/00003_job_result_stats.sql`
- Modify: `backend/internal/jobs/query.go`
- Modify: `backend/internal/jobs/lifecycle.go`
- Modify: `backend/internal/jobs/query_test.go`

- [ ] **Step 1: Write the migration**

Create `backend/migrations/00003_job_result_stats.sql`:

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

This has no separate test — `testdb.New(t)` runs all migrations against a fresh database for every test, so the next step's tests exercise it directly.

- [ ] **Step 2: Write the failing tests for `RecordResult` and the name joins**

Add to the end of `backend/internal/jobs/query_test.go`:

```go
func TestRecordResultPersistsAndListJobsReturnsIt(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Complete(ctx, tx, j.ID, j.Attempt, "w1"); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordResult(ctx, tx, j.ID, 2, 1, 0, 0, 5, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	list, err := q.ListJobs(ctx, jobs.JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	got := list[0]
	if got.ResultNew == nil || *got.ResultNew != 2 ||
		got.ResultChanged == nil || *got.ResultChanged != 1 ||
		got.ResultSeen == nil || *got.ResultSeen != 0 ||
		got.ResultInvalid == nil || *got.ResultInvalid != 0 ||
		got.ResultIrrelevant == nil || *got.ResultIrrelevant != 5 ||
		got.ResultAlerts == nil || *got.ResultAlerts != 1 {
		t.Fatalf("result stats = %+v", got)
	}
}

func TestListJobsReturnsNilResultForIncompleteJob(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	claim(t, q) // left running: never Completed or RecordResult'd

	list, err := q.ListJobs(context.Background(), jobs.JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ResultNew != nil {
		t.Fatalf("list = %+v, want ResultNew nil for a job with no recorded result", list)
	}
}

func TestListJobsIncludesSearchAndRegionNames(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	claim(t, q)

	list, err := q.ListJobs(context.Background(), jobs.JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SearchName == nil || *list[0].SearchName != "Civic EG" ||
		list[0].RegionName == nil || *list[0].RegionName != "curitiba-pr" {
		t.Fatalf("list = %+v, want search_name=Civic EG region_name=curitiba-pr", list)
	}
}
```

(`testdb.Seed` creates a search named `"Civic EG"` and a region `"curitiba-pr"` whose `name` column is also literally `"curitiba-pr"` — see `backend/internal/testdb/testdb.go`'s `AddRegion`, which sets `name = id`.)

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd backend && go test ./internal/jobs/... -run 'TestRecordResult|TestListJobs' -v`
Expected: compile error — `q.RecordResult` doesn't exist yet, and `JobSummary` has no `ResultNew`/`SearchName`/`RegionName` fields.

- [ ] **Step 4: Implement — `JobSummary` and `ListJobs`**

Replace `JobSummary` and `ListJobs` in `backend/internal/jobs/query.go`:

```go
type JobSummary struct {
	ID               string     `json:"id"`
	Type             string     `json:"type"`
	Source           string     `json:"source"`
	SearchID         *string    `json:"search_id,omitempty"`
	RegionID         *string    `json:"region_id,omitempty"`
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	ErrorKind        *string    `json:"error_kind,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	SearchName       *string    `json:"search_name,omitempty"`
	RegionName       *string    `json:"region_name,omitempty"`
	ResultNew        *int       `json:"result_new,omitempty"`
	ResultChanged    *int       `json:"result_changed,omitempty"`
	ResultSeen       *int       `json:"result_seen,omitempty"`
	ResultInvalid    *int       `json:"result_invalid,omitempty"`
	ResultIrrelevant *int       `json:"result_irrelevant,omitempty"`
	ResultAlerts     *int       `json:"result_alerts,omitempty"`
}
```

```go
func (q *Queue) ListJobs(ctx context.Context, f JobFilter) ([]JobSummary, error) {
	query := `SELECT jobs.id, jobs.type, jobs.source, jobs.search_id, jobs.region_id, jobs.status,
	                  jobs.attempts, jobs.error_kind, jobs.created_at, jobs.started_at, jobs.finished_at,
	                  sd.name, sr.name,
	                  jobs.result_new, jobs.result_changed, jobs.result_seen,
	                  jobs.result_invalid, jobs.result_irrelevant, jobs.result_alerts
	           FROM jobs
	           LEFT JOIN search_definitions sd ON sd.id = jobs.search_id
	           LEFT JOIN search_regions sr ON sr.id = jobs.region_id`
	var where []string
	var args []any
	// arg appends v to args and returns its positional placeholder. Filter
	// values only ever reach the query through this parametrized args slice,
	// never interpolated as text, so there's no injection risk despite the
	// dynamic WHERE construction.
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Status != "" {
		where = append(where, "jobs.status = "+arg(f.Status))
	}
	if f.ErrorKind != "" {
		where = append(where, "jobs.error_kind = "+arg(f.ErrorKind))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY jobs.created_at DESC"
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := q.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var out []JobSummary
	for rows.Next() {
		var j JobSummary
		if err := rows.Scan(&j.ID, &j.Type, &j.Source, &j.SearchID, &j.RegionID, &j.Status,
			&j.Attempts, &j.ErrorKind, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
			&j.SearchName, &j.RegionName,
			&j.ResultNew, &j.ResultChanged, &j.ResultSeen,
			&j.ResultInvalid, &j.ResultIrrelevant, &j.ResultAlerts); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Implement — `RecordResult`**

In `backend/internal/jobs/lifecycle.go`, add this method directly after `Complete` (i.e. right after its closing `}`, before the `// Fail records a classified failure...` comment):

```go
// RecordResult stores a completed job's ingest outcome (new/changed/seen/
// invalid/irrelevant/alerts counts), inside the same transaction as
// Complete and the ingest write — a job is never left "succeeded" with no
// way to later see what it found.
func (q *Queue) RecordResult(ctx context.Context, tx pgx.Tx, jobID string, newCount, changedCount, seenCount, invalidCount, irrelevantCount, alertsCount int) error {
	_, err := tx.Exec(ctx, `UPDATE jobs SET
		result_new = $2, result_changed = $3, result_seen = $4,
		result_invalid = $5, result_irrelevant = $6, result_alerts = $7
		WHERE id = $1`, jobID, newCount, changedCount, seenCount, invalidCount, irrelevantCount, alertsCount)
	if err != nil {
		return fmt.Errorf("record result for job %s: %w", jobID, err)
	}
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd backend && go test ./internal/jobs/... -v`
Expected: PASS (all tests in the package, including the 3 new ones)

- [ ] **Step 7: Commit**

```bash
git add backend/migrations/00003_job_result_stats.sql backend/internal/jobs/query.go backend/internal/jobs/lifecycle.go backend/internal/jobs/query_test.go
git commit -m "feat(jobs): persist per-job result stats, add search/region names to ListJobs"
```

---

### Task 2: Wire `RecordResult` into the `/result` handler

**Files:**
- Modify: `backend/internal/httpapi/internal.go`
- Modify: `backend/internal/httpapi/internal_test.go`

- [ ] **Step 1: Write the failing test**

Add to the end of `backend/internal/httpapi/internal_test.go`:

```go
func TestResultPersistsStatsOnJob(t *testing.T) {
	e := setup(t)
	e.enqueue(t)
	_, job := e.post(t, "/internal/v1/jobs/claim", claimBody())
	jobID := job["job_id"].(string)

	result := map[string]any{
		"worker_id": "w1", "attempt": 1, "crawler_version": "0.1.0", "parser_version": "fb-1",
		"items": []any{item("1", "Honda Civic EG 1993", "23500"), item("2", "Honda Civic EG5 1994", "21000")},
		"stats": map[string]any{"pages": 1, "duration_ms": 1200},
	}
	resp, _ := e.post(t, "/internal/v1/jobs/"+jobID+"/result", result)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("result status = %d", resp.StatusCode)
	}

	var resultNew, resultChanged, resultSeen, resultInvalid, resultIrrelevant, resultAlerts int
	if err := e.pool.QueryRow(context.Background(), `SELECT result_new, result_changed, result_seen,
		result_invalid, result_irrelevant, result_alerts FROM jobs WHERE id = $1`, jobID).Scan(
		&resultNew, &resultChanged, &resultSeen, &resultInvalid, &resultIrrelevant, &resultAlerts); err != nil {
		t.Fatal(err)
	}
	if resultNew != 2 || resultChanged != 0 {
		t.Fatalf("result_new=%d result_changed=%d, want new=2 changed=0", resultNew, resultChanged)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/httpapi/... -run TestResultPersistsStatsOnJob -v`
Expected: FAIL — `result_new` etc. don't exist as columns yet if Task 1 wasn't run first (they do, since Task 1 already added them), OR more likely: the test passes the HTTP call fine but the SELECT returns `NULL` (scanned into a non-pointer `int`, which errors) because the handler never calls `RecordResult` yet. Either way, this step is expected to fail until Step 3 is done.

- [ ] **Step 3: Implement**

In `backend/internal/httpapi/internal.go`, inside the `result` handler, insert a call to `h.Queue.RecordResult` between the `Ingest` call and `tx.Commit`:

```go
	stats, err := h.Ingest.Ingest(ctx, tx, ingest.Batch{
		JobID: id, Source: done.Source, SearchID: *done.SearchID, RegionID: *done.RegionID,
		CrawlerVersion: req.CrawlerVersion, ParserVersion: req.ParserVersion,
		SeenAt: done.FinishedAt, Items: req.Items,
	})
	if err != nil {
		h.internalError(w, "ingest", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	if err := h.Queue.RecordResult(ctx, tx, id, stats.New, stats.Changed, stats.Seen, stats.Invalid, stats.Irrelevant, stats.Alerts); err != nil {
		h.internalError(w, "record result", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.internalError(w, "commit", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
```

(This replaces the existing block that goes straight from the `Ingest` error check to `tx.Commit` — find it by searching for `h.Ingest.Ingest(ctx, tx, ingest.Batch{` in that file.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/httpapi/... -v`
Expected: PASS

Also run the whole backend module to catch any ripple effect:

Run: `cd backend && go build ./... && go test ./...`
Expected: build succeeds, all packages `ok`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpapi/internal.go backend/internal/httpapi/internal_test.go
git commit -m "feat(httpapi): record job result stats on /internal/v1/jobs/{id}/result"
```

---

### Task 3: Frontend — show search/region/duration/results in the Jobs table

**Files:**
- Modify: `frontend/lib/types.ts`
- Modify: `frontend/lib/format.ts`
- Modify: `frontend/app/health/page.tsx`

- [ ] **Step 1: Update `JobSummary`**

In `frontend/lib/types.ts`, replace the `JobSummary` interface:

```ts
export interface JobSummary {
  id: string;
  type: string;
  source: string;
  search_id?: string;
  region_id?: string;
  status: string;
  attempts: number;
  error_kind?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  search_name?: string;
  region_name?: string;
  result_new?: number;
  result_changed?: number;
  result_seen?: number;
  result_invalid?: number;
  result_irrelevant?: number;
  result_alerts?: number;
}
```

- [ ] **Step 2: Add `formatDuration`**

In `frontend/lib/format.ts`, add this function after `formatPrice` (before `timeAgo`, or after — order doesn't matter, just keep it alongside the other two):

```ts
export function formatDuration(totalSeconds: number): string {
  if (totalSeconds < 60) return `${totalSeconds}s`;
  if (totalSeconds < 3600) return `${Math.floor(totalSeconds / 60)}min`;
  return `${Math.floor(totalSeconds / 3600)}h ${Math.floor((totalSeconds % 3600) / 60)}min`;
}
```

- [ ] **Step 3: Update the Jobs table in `app/health/page.tsx`**

First, update the import line at the top of `frontend/app/health/page.tsx` — change:

```tsx
import { apiFetch, apiFetchList } from "@/lib/api";
import type { JobSummary, SourceSession, Stats, Worker } from "@/lib/types";
import { resumeSessionAction } from "./actions";
```

to:

```tsx
import { apiFetch, apiFetchList } from "@/lib/api";
import { formatDuration } from "@/lib/format";
import type { JobSummary, SourceSession, Stats, Worker } from "@/lib/types";
import { resumeSessionAction } from "./actions";

function jobDuration(j: JobSummary): string {
  if (j.started_at && j.finished_at) {
    const seconds = Math.max(
      0,
      Math.round((new Date(j.finished_at).getTime() - new Date(j.started_at).getTime()) / 1000),
    );
    return formatDuration(seconds);
  }
  if (j.status === "running" && j.started_at) {
    const seconds = Math.max(0, Math.round((Date.now() - new Date(j.started_at).getTime()) / 1000));
    return `rodando há ${formatDuration(seconds)}`;
  }
  return "—";
}

function jobResults(j: JobSummary): string {
  if (j.result_new == null) return "—";
  return `${j.result_new} novos · ${j.result_changed ?? 0} alterados · ${j.result_alerts ?? 0} alertas`;
}
```

Then replace the Jobs table's `<thead>`/`<tbody>` (inside the last `<section>`, the one with `<h2>Jobs</h2>`) — change:

```tsx
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Fonte</th>
              <th>Status</th>
              <th>Tentativas</th>
              <th>Erro</th>
              <th>Criado em</th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id} className="border-b border-slate-100">
                <td className="py-2">{j.source}</td>
                <td>{j.status}</td>
                <td>{j.attempts}</td>
                <td>{j.error_kind ?? "—"}</td>
                <td>{new Date(j.created_at).toLocaleString("pt-BR")}</td>
              </tr>
            ))}
          </tbody>
        </table>
```

to:

```tsx
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Fonte</th>
              <th>Busca</th>
              <th>Região</th>
              <th>Status</th>
              <th>Duração</th>
              <th>Tentativas</th>
              <th>Erro</th>
              <th>Encontrados</th>
              <th>Criado em</th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id} className="border-b border-slate-100">
                <td className="py-2">{j.source}</td>
                <td>{j.search_name ?? "—"}</td>
                <td>{j.region_name ?? "—"}</td>
                <td>{j.status}</td>
                <td>{jobDuration(j)}</td>
                <td>{j.attempts}</td>
                <td>{j.error_kind ?? "—"}</td>
                <td>{jobResults(j)}</td>
                <td>{new Date(j.created_at).toLocaleString("pt-BR")}</td>
              </tr>
            ))}
          </tbody>
        </table>
```

- [ ] **Step 4: Typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add frontend/lib/types.ts frontend/lib/format.ts frontend/app/health/page.tsx
git commit -m "feat(dashboard): show search/region/duration/results in the Jobs table"
```

---

### Task 4: Manual verification

**Files:** none (verification only)

- [ ] **Step 1: Run backend test suite**

Run: `cd backend && go test ./...`
Expected: all packages `ok`

- [ ] **Step 2: Deploy and exercise the real flow**

Use the `run` skill (or `docker compose up -d --build api dashboard` from the project's compose directory — do **not** run `docker compose down`) to deploy the change, trigger at least one search run (e.g. via the "executar agora" button on `/searches`), wait for it to finish, then open `/health` and confirm:
- The Jobs table shows the search name and region name (not raw IDs) for the job you just ran.
- Duration shows a sensible value once the job finishes (and ideally catch one mid-run to confirm "rodando há Xs"/"Xmin" shows while `status` is `running`).
- "Encontrados" shows real counts (e.g. "0 novos · 0 alterados · 0 alertas" or similar) for the completed job, and "—" for any job that predates this change (jobs with `NULL` result columns).

- [ ] **Step 3: Report results**

No code changes in this task — just confirm the above manually and report any issues found (if issues are found, they become new tasks or fixes to the tasks above, not silent changes).
