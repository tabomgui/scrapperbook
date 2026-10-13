# Discord Notifications + Public API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the pending alerts that plan 1's ingest pipeline already writes to the `notifications` table into real Discord messages, and give the backend a read/write `/api/v1` so the user (and, later, a dashboard) can manage searches and browse listings without touching SQL directly. Also close two small gaps plan 1 deliberately deferred: marking listings `unseen` after they stop appearing, and purging old `raw_captures`.

**Architecture:** Everything here is additive to the existing Go backend (plan 1) — no new services, no new containers. A `notify` package drains the `notifications` outbox to Discord. A `catalog` package owns read/write access to searches, regions, and listings (the data a human manages, as opposed to `jobs`, which owns the queue). `httpapi`'s existing `/internal/v1` (worker-facing, token-authenticated) gets a sibling `/api/v1` (human-facing, no auth, bound to `127.0.0.1` only — same posture as the design spec's single-user decision). Two background sweeps (unseen marking, raw_captures retention) join the scheduler tick and lease reaper already running in `main.go`.

**Tech Stack:** Go 1.26 (same backend module), `prometheus/client_golang` (new dependency, for `/metrics`), `net/http` stdlib, PostgreSQL 17 (same schema, no new migration needed — `listings.status = 'unseen'` and `raw_captures` already exist from plan 1).

**Spec:** `docs/superpowers/specs/2026-10-09-marketplace-monitor-mvp-design.md` §5 (Notifier), §7 (API pública), §8 (Observabilidade)
**Depends on:** `docs/superpowers/plans/2026-10-09-backend-core.md` (plan 1) and `docs/superpowers/plans/2026-10-09-python-crawler.md` (plan 2), both already implemented, reviewed, and proven end-to-end (including one real run against live Facebook — see plan 2's Task 13).

---

## Scope note

The original SPEC's §23 "Dashboard futuro" describes a Next.js UI. That is **out of scope here** — this plan only builds the API the dashboard would eventually call. Plan 1's own roadmap note ("Plano 3... Plano 4 (dashboard): Next.js") already drew this line; nothing here contradicts it.

## Deviations from the design spec

| Spec | Plano | Motivo |
|---|---|---|
| `/api/v1` descrito só como lista de rotas | Rotas vivem no mesmo pacote `httpapi` que `/internal/v1` (não um pacote novo) | Reaproveita `respond.go`'s `writeJSON`/`writeError`/`decodeJSON`/`isUUID` sem duplicar. |
| — | Novo pacote `catalog` (não mencionado explicitamente na spec) | A spec não separa "dados que o humano gerencia" (buscas, regiões, anúncios) de "fila" (`jobs`) — mas o código já segue essa linha (plan 1's `jobs` só sabe de filas, não de CRUD). `catalog` fecha essa lacuna sem inflar `httpapi` com SQL direto. |
| — | Novo pacote `metrics` com contadores globais (`promauto`) | Mais simples que threadear um registry por toda função de `ingest`/`jobs`; é o padrão idiomático do `client_golang` e evita mudar assinaturas de código já revisado. |
| `frequency` como string de duração no payload de `/searches` | API aceita `frequency_seconds: int` | `frequency` no Postgres é `interval`; pgx v5 mapeia `time.Duration` para `interval` nativamente, mas um literal de duração estilo Go ("30m") não é sintaxe de `interval` do Postgres ("30 minutes"). Expor segundos evita essa ambiguidade de formato. |

---

## File structure

```text
backend/internal/
├── metrics/
│   └── metrics.go                  prometheus counters/histograms (package vars)
├── notify/
│   ├── notifier.go                 Notifier interface, Notification struct
│   ├── discord.go                  DiscordNotifier (webhook HTTP POST)
│   └── outbox.go                   DrainPending — reads notifications, sends, marks sent_at
├── sweep/
│   ├── unseen.go                   MarkUnseen
│   └── retention.go                PurgeOldRawCaptures
├── catalog/
│   ├── regions.go                  Region CRUD
│   ├── searches.go                 SearchDefinition CRUD (+ region linking)
│   └── listings.go                 Listings read (List w/ filters, Get+snapshots)
├── config/config.go                 (modified) + DiscordWebhookURL, ListingUnseenAfter, RawCapturesRetention
├── ingest/ingest.go                 (modified) metrics counters on insert/skip/invalid
├── jobs/
│   ├── query.go                     (new) Queue.ListJobs, Queue.ListSessions
│   └── lifecycle.go                 (modified) metrics counters on Complete/Fail
├── workers/
│   └── list.go                      (new) Worker struct, Registry.List
└── httpapi/
    ├── public.go                    Public struct, Routes(mux) — wires everything below
    ├── searches.go                  /api/v1/searches handlers
    ├── regions.go                   /api/v1/regions handlers
    ├── listings.go                  /api/v1/listings handlers
    ├── ops.go                       /api/v1/jobs, /workers, /sessions(+resume) handlers
    ├── stats.go                     /api/v1/stats handler
    └── metrics.go                   /metrics handler (promhttp)

backend/cmd/server/main.go            (modified) wire notifier loop, sweeps, Public routes
compose.yaml                          (modified) DISCORD_WEBHOOK_URL already present; add LISTING_UNSEEN_AFTER, RAW_CAPTURES_RETENTION
```

Prerequisites: same as plan 1 — Go 1.26+, Docker running (testcontainers for tests).

---

### Task 1: Config — Discord webhook and sweep intervals

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`

- [ ] **Step 1: Escrever os testes que falham**

Add to `backend/internal/config/config_test.go` (keep every existing test in the file unchanged, just add these):

```go
func TestDiscordAndSweepDefaults(t *testing.T) {
	c, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DiscordWebhookURL != "" {
		t.Fatalf("discord webhook default = %q, want empty (disabled)", c.DiscordWebhookURL)
	}
	if c.ListingUnseenAfter != 7*24*time.Hour {
		t.Fatalf("listing unseen after = %s, want 168h", c.ListingUnseenAfter)
	}
	if c.RawCapturesRetention != 14*24*time.Hour {
		t.Fatalf("raw captures retention = %s, want 336h", c.RawCapturesRetention)
	}
}

func TestDiscordAndSweepOverridesAndValidation(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t",
		"DISCORD_WEBHOOK_URL": "https://discord.com/api/webhooks/1/abc",
		"LISTING_UNSEEN_AFTER": "48h", "RAW_CAPTURES_RETENTION": "72h",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DiscordWebhookURL != "https://discord.com/api/webhooks/1/abc" {
		t.Fatalf("discord webhook = %q", c.DiscordWebhookURL)
	}
	if c.ListingUnseenAfter != 48*time.Hour || c.RawCapturesRetention != 72*time.Hour {
		t.Fatalf("unseen=%s retention=%s", c.ListingUnseenAfter, c.RawCapturesRetention)
	}

	for _, bad := range []map[string]string{
		{"LISTING_UNSEEN_AFTER": "soon"},
		{"LISTING_UNSEEN_AFTER": "0h"},
		{"RAW_CAPTURES_RETENTION": "-1h"},
	} {
		m := map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}
		for k, v := range bad {
			m[k] = v
		}
		if _, err := config.Load(env(m)); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/config/ -run Discord -v`
Expected: compile error, `Config` has no field `DiscordWebhookURL`.

- [ ] **Step 3: Implementar**

In `backend/internal/config/config.go`, add fields to `Config`:

```go
type Config struct {
	DatabaseURL string
	WorkerToken string
	HTTPAddr    string
	Limits      jobs.Limits

	DiscordWebhookURL    string // empty disables Discord notifications, not an error
	ListingUnseenAfter   time.Duration
	RawCapturesRetention time.Duration
}
```

In `Load`, right before `return c, nil`, add:

```go
	listingUnseenAfter, err := time.ParseDuration(or(getenv("LISTING_UNSEEN_AFTER"), "168h"))
	if err != nil {
		return Config{}, fmt.Errorf("LISTING_UNSEEN_AFTER: %w", err)
	}
	if listingUnseenAfter <= 0 {
		return Config{}, fmt.Errorf("LISTING_UNSEEN_AFTER: must be positive, got %s", listingUnseenAfter)
	}
	rawCapturesRetention, err := time.ParseDuration(or(getenv("RAW_CAPTURES_RETENTION"), "336h"))
	if err != nil {
		return Config{}, fmt.Errorf("RAW_CAPTURES_RETENTION: %w", err)
	}
	if rawCapturesRetention <= 0 {
		return Config{}, fmt.Errorf("RAW_CAPTURES_RETENTION: must be positive, got %s", rawCapturesRetention)
	}
	c.DiscordWebhookURL = getenv("DISCORD_WEBHOOK_URL") // no default: empty means disabled
	c.ListingUnseenAfter = listingUnseenAfter
	c.RawCapturesRetention = rawCapturesRetention
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/config/ -v`
Expected: all tests PASS, including the 2 new ones and every pre-existing one.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/
git commit -m "feat(backend): add discord webhook and sweep interval config"
```

---

### Task 2: Metrics package

Package-level `promauto` vars, not threaded through constructors — the idiomatic `client_golang` pattern, and it means Task 3's wiring is a one-line `.Inc()`/`.Observe()` at each call site instead of a signature change to already-reviewed code.

**Files:**
- Create: `backend/internal/metrics/metrics.go`
- Test: `backend/internal/metrics/metrics_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`backend/internal/metrics/metrics_test.go`:

```go
package metrics_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"scrapperbook/backend/internal/metrics"
)

func TestCountersAreIndependentByLabel(t *testing.T) {
	before := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("test-source", "search", "succeeded", ""))
	metrics.JobsTotal.WithLabelValues("test-source", "search", "succeeded", "").Inc()
	after := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("test-source", "search", "succeeded", ""))
	if after-before != 1 {
		t.Fatalf("delta = %v, want 1", after-before)
	}
	// A different error_kind label is a distinct series, untouched by the above.
	other := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("test-source", "search", "dead", "network"))
	if other != 0 {
		t.Fatalf("other label series = %v, want 0 (independent)", other)
	}
}

func TestHistogramAndPlainCounter(t *testing.T) {
	metrics.JobDurationSeconds.WithLabelValues("test-source", "search").Observe(1.5)
	before := testutil.ToFloat64(metrics.SnapshotsTotal)
	metrics.SnapshotsTotal.Inc()
	after := testutil.ToFloat64(metrics.SnapshotsTotal)
	if after-before != 1 {
		t.Fatalf("snapshots delta = %v, want 1", after-before)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/metrics/ -v`
Expected: `ModuleNotFoundError`-equivalent Go error: `package scrapperbook/backend/internal/metrics is not in std` (package doesn't exist yet).

- [ ] **Step 3: Implementar**

`backend/internal/metrics/metrics.go`:

```go
// Package metrics exposes Prometheus counters and histograms describing
// pipeline activity. These are package-level vars, not fields on a struct
// passed around — the idiomatic promauto pattern, and it means callers
// anywhere in the backend can record a metric with one line, with no
// constructor or signature changes to the code being instrumented.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// JobsTotal counts every terminal job outcome. error_kind is empty for
	// a successful job.
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_total",
		Help: "Jobs processed, by source, type, status and error_kind.",
	}, []string{"source", "type", "status", "error_kind"})

	// JobDurationSeconds is observed only on success — a failed job's
	// "duration" isn't a meaningful latency signal the way a completed
	// crawl's is.
	JobDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "job_duration_seconds",
		Help: "Successful job duration in seconds, by source and type.",
	}, []string{"source", "type"})

	// ListingsIngestedTotal's result label is one of new|changed|seen|invalid.
	ListingsIngestedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "listings_ingested_total",
		Help: "Listing items processed by ingest, by source and result.",
	}, []string{"source", "result"})

	SnapshotsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "snapshots_total",
		Help: "listing_snapshots rows written.",
	})

	// NotificationsTotal's status label is one of sent|failed.
	NotificationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "notifications_total",
		Help: "Notifications processed by the outbox drain, by kind and status.",
	}, []string{"kind", "status"})
)
```

- [ ] **Step 4: Baixar a dependência e rodar**

Run: `cd backend && go get github.com/prometheus/client_golang@latest && go mod tidy && go test ./internal/metrics/ -v`
Expected: 2 testes PASS (`TestCountersAreIndependentByLabel`, `TestHistogramAndPlainCounter`).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/metrics/ backend/go.mod backend/go.sum
git commit -m "feat(backend): add prometheus metrics package"
```

---

### Task 3: Wire metrics into ingest and job lifecycle

These are additive one-line edits to two already-reviewed, already-tested files from plan 1. Every existing test in both files must keep passing unchanged — these tests only verify the new increments happen, never contradicting existing assertions.

**Files:**
- Modify: `backend/internal/ingest/ingest.go`
- Modify: `backend/internal/jobs/lifecycle.go`
- Test: `backend/internal/ingest/ingest_metrics_test.go` (new file, same package as `ingest_test.go` — reuses its `setup`/`node`/`env` helpers)
- Test: `backend/internal/jobs/lifecycle_metrics_test.go` (new file, same package as `queue_test.go`/`lifecycle_test.go` — reuses `newQueue`/`enqueue`/`claim`/`complete` helpers)

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/ingest/ingest_metrics_test.go`:

```go
package ingest_test

import (
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/metrics"
)

func TestIngestIncrementsListingsAndSnapshotMetrics(t *testing.T) {
	e := setup(t)
	beforeNew := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "new"))
	beforeSnap := testutil.ToFloat64(metrics.SnapshotsTotal)

	e.run(t, seenAt, node("1", "Civic EG", "23500"))

	if delta := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "new")) - beforeNew; delta != 1 {
		t.Fatalf("new metric delta = %v, want 1", delta)
	}
	if delta := testutil.ToFloat64(metrics.SnapshotsTotal) - beforeSnap; delta != 1 {
		t.Fatalf("snapshots metric delta = %v, want 1", delta)
	}
}

func TestIngestIncrementsInvalidMetric(t *testing.T) {
	e := setup(t)
	before := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "invalid"))

	bad := ingest.Item{ExternalID: "x", Raw: json.RawMessage(`{`)}
	e.run(t, seenAt, bad)

	if delta := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "invalid")) - before; delta != 1 {
		t.Fatalf("invalid metric delta = %v, want 1", delta)
	}
}
```

`backend/internal/jobs/lifecycle_metrics_test.go`:

```go
package jobs_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/metrics"
	"scrapperbook/backend/internal/testdb"
)

func TestCompleteIncrementsJobsAndDurationMetrics(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	before := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "succeeded", ""))
	complete(t, pool, q, j, 1)
	if delta := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "succeeded", "")) - before; delta != 1 {
		t.Fatalf("jobs_total delta = %v, want 1", delta)
	}
}

func TestFailIncrementsJobsMetricWithErrorKind(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	before := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "queued", "network"))
	if _, err := q.Fail(context.Background(), j.ID, j.Attempt, "w1", jobs.KindNetwork, "boom"); err != nil {
		t.Fatal(err)
	}
	if delta := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "queued", "network")) - before; delta != 1 {
		t.Fatalf("jobs_total delta = %v, want 1", delta)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/ingest/ ./internal/jobs/ -run Metrics -v`
Expected: both fail — `metrics.ListingsIngestedTotal`/`metrics.JobsTotal` deltas are 0, since nothing increments them yet.

- [ ] **Step 3: Implementar — `ingest.go`**

Add the import:

```go
	"scrapperbook/backend/internal/metrics"
```

Find:
```go
		if !json.Valid(it.Raw) {
			st.Invalid++
			s.log.Warn("invalid raw json", "job_id", b.JobID, "external_id", it.ExternalID)
			continue
		}
```
Replace with:
```go
		if !json.Valid(it.Raw) {
			st.Invalid++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "invalid").Inc()
			s.log.Warn("invalid raw json", "job_id", b.JobID, "external_id", it.ExternalID)
			continue
		}
```

Find:
```go
		c, err := norm.Normalize(it.ExternalID, it.Raw)
		if err != nil {
			st.Invalid++
			s.log.Warn("normalize failed", "job_id", b.JobID, "external_id", it.ExternalID, "err", err)
			continue
		}
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
```

Find:
```go
		switch res.kind {
		case kindNew:
			st.New++
		case kindChanged:
			st.Changed++
		case kindSeen:
			st.Seen++
		}
```
Replace with:
```go
		switch res.kind {
		case kindNew:
			st.New++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "new").Inc()
		case kindChanged:
			st.Changed++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "changed").Inc()
		case kindSeen:
			st.Seen++
			metrics.ListingsIngestedTotal.WithLabelValues(b.Source, "seen").Inc()
		}
```

Find:
```go
func insertSnapshot(ctx context.Context, tx pgx.Tx, listingID int64, c listing.Canonical, raw json.RawMessage, b Batch) error {
	_, err := tx.Exec(ctx, `INSERT INTO listing_snapshots
		(listing_id, price_cents, title, description, status, job_id, captured_at, raw_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		listingID, c.PriceCents, c.Title, nullable(c.Description), string(c.Status), b.JobID, b.SeenAt, raw)
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}
```
Replace with:
```go
func insertSnapshot(ctx context.Context, tx pgx.Tx, listingID int64, c listing.Canonical, raw json.RawMessage, b Batch) error {
	_, err := tx.Exec(ctx, `INSERT INTO listing_snapshots
		(listing_id, price_cents, title, description, status, job_id, captured_at, raw_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		listingID, c.PriceCents, c.Title, nullable(c.Description), string(c.Status), b.JobID, b.SeenAt, raw)
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	metrics.SnapshotsTotal.Inc()
	return nil
}
```

- [ ] **Step 4: Implementar — `lifecycle.go`**

Add the import:

```go
	"scrapperbook/backend/internal/metrics"
```

Find, in `Complete`:
```go
	if err := markWorkerIdle(ctx, tx, workerID, now, true); err != nil {
		return Completed{}, err
	}
	return c, nil
}
```
Replace with:
```go
	if err := markWorkerIdle(ctx, tx, workerID, now, true); err != nil {
		return Completed{}, err
	}
	// "search" is hardcoded rather than read from the job: only that type
	// exists today (the scheduler never enqueues "enrich" jobs yet). Revisit
	// this label once a second job type is real.
	metrics.JobsTotal.WithLabelValues(c.Source, "search", "succeeded", "").Inc()
	metrics.JobDurationSeconds.WithLabelValues(c.Source, "search").Observe(now.Sub(c.StartedAt).Seconds())
	return c, nil
}
```

Find, in `Fail`:
```go
	if err := markWorkerIdle(ctx, tx, workerID, now, false); err != nil {
		return Outcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Outcome{}, err
	}
	return out, nil
}
```
Replace with:
```go
	if err := markWorkerIdle(ctx, tx, workerID, now, false); err != nil {
		return Outcome{}, err
	}
	metrics.JobsTotal.WithLabelValues(source, "search", out.Status, string(kind)).Inc()
	if err := tx.Commit(ctx); err != nil {
		return Outcome{}, err
	}
	return out, nil
}
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/ingest/ ./internal/jobs/ ./internal/metrics/ -v`
Expected: every test in all three packages PASS — the 4 new metrics tests, plus every pre-existing test in `ingest` (6 tests) and `jobs` (18 tests) with no regressions.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/ingest/ backend/internal/jobs/
git commit -m "feat(backend): record metrics for ingest results and job outcomes"
```

---

### Task 4: Notifier interface and Discord implementation

**Files:**
- Create: `backend/internal/notify/notifier.go`
- Create: `backend/internal/notify/discord.go`
- Test: `backend/internal/notify/discord_test.go`

- [ ] **Step 1: Escrever a interface (sem teste próprio — é só um contrato)**

`backend/internal/notify/notifier.go`:

```go
// Package notify sends pending notifications (new listing, price drop,
// system alert) to an external channel. Discord is the only implementation
// today; Notifier exists so a different channel can be swapped in later
// without touching the outbox-draining logic in outbox.go.
package notify

import "context"

// Notification mirrors one row of the notifications table joined with the
// listing data a Notifier needs to build a message — no DB access needed
// inside a Notifier implementation.
type Notification struct {
	Kind          string // "new" | "price_drop" | "system"
	Message       string // only set for kind == "system"
	ListingTitle  string
	ListingURL    string
	ImageURL      string
	City          string
	State         string
	PriceCents    *int64
	OldPriceCents *int64 // only set for kind == "price_drop"
}

type Notifier interface {
	// Send delivers one notification. A non-nil error means the caller must
	// NOT mark it sent — it gets retried on the next drain.
	Send(ctx context.Context, n Notification) error
}
```

- [ ] **Step 2: Escrever o teste que falha**

`backend/internal/notify/discord_test.go`:

```go
package notify_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrapperbook/backend/internal/notify"
)

func int64Ptr(v int64) *int64 { return &v }

func TestDiscordSendsAnEmbedForANewListing(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := notify.NewDiscordNotifier(srv.URL)
	err := d.Send(t.Context(), notify.Notification{
		Kind: "new", ListingTitle: "Honda Civic EG 1993", ListingURL: "https://example/item/1",
		ImageURL: "https://example/civic.jpg", City: "Curitiba", State: "PR",
		PriceCents: int64Ptr(2350000),
	})
	if err != nil {
		t.Fatal(err)
	}
	embeds, ok := gotBody["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("body = %+v", gotBody)
	}
	embed := embeds[0].(map[string]any)
	if embed["title"] != "Honda Civic EG 1993" || embed["url"] != "https://example/item/1" {
		t.Errorf("embed = %+v", embed)
	}
	desc, _ := embed["description"].(string)
	if desc != "R$ 23500,00 · Curitiba/PR" {
		t.Errorf("description = %q", desc)
	}
	thumb, ok := embed["thumbnail"].(map[string]any)
	if !ok || thumb["url"] != "https://example/civic.jpg" {
		t.Errorf("thumbnail = %+v", embed["thumbnail"])
	}
}

func TestDiscordShowsOldAndNewPriceForAPriceDrop(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := notify.NewDiscordNotifier(srv.URL)
	err := d.Send(t.Context(), notify.Notification{
		Kind: "price_drop", ListingTitle: "Civic EG5", ListingURL: "https://example/item/2",
		City: "Curitiba", PriceCents: int64Ptr(2100000), OldPriceCents: int64Ptr(2350000),
	})
	if err != nil {
		t.Fatal(err)
	}
	embed := gotBody["embeds"].([]any)[0].(map[string]any)
	if embed["description"] != "R$ 23500,00 → R$ 21000,00 · Curitiba" {
		t.Errorf("description = %v", embed["description"])
	}
}

func TestDiscordSendsPlainContentForASystemAlert(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := notify.NewDiscordNotifier(srv.URL)
	err := d.Send(t.Context(), notify.Notification{Kind: "system", Message: "fonte precisa de atenção"})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["content"] != "fonte precisa de atenção" {
		t.Errorf("content = %v", gotBody["content"])
	}
	if _, hasEmbeds := gotBody["embeds"]; hasEmbeds {
		t.Errorf("system alert should not have embeds, got %v", gotBody["embeds"])
	}
}

func TestDiscordReturnsErrorOnRateLimitAndServerError(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		d := notify.NewDiscordNotifier(srv.URL)
		if err := d.Send(t.Context(), notify.Notification{Kind: "system", Message: "x"}); err == nil {
			t.Errorf("status %d: expected error", status)
		}
		srv.Close()
	}
}
```

- [ ] **Step 3: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/notify/ -v`
Expected: `ModuleNotFoundError`-equivalent: `package scrapperbook/backend/internal/notify` compiles but `notify.NewDiscordNotifier` is undefined.

- [ ] **Step 4: Implementar**

`backend/internal/notify/discord.go`:

```go
// Discord implementation of Notifier: one POST per notification to a
// webhook URL — a rich embed (title, price, location, thumbnail) for
// new/price_drop, plain text content for system alerts.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type DiscordNotifier struct {
	WebhookURL string
	HTTPClient *http.Client
}

func NewDiscordNotifier(webhookURL string) *DiscordNotifier {
	return &DiscordNotifier{WebhookURL: webhookURL, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

type discordThumbnail struct {
	URL string `json:"url"`
}

type discordEmbed struct {
	Title       string            `json:"title"`
	URL         string            `json:"url,omitempty"`
	Description string            `json:"description,omitempty"`
	Thumbnail   *discordThumbnail `json:"thumbnail,omitempty"`
}

type discordPayload struct {
	Content string         `json:"content,omitempty"`
	Embeds  []discordEmbed `json:"embeds,omitempty"`
}

func (d *DiscordNotifier) Send(ctx context.Context, n Notification) error {
	body, err := json.Marshal(buildPayload(n))
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post to discord: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("discord rate limited this webhook (429)")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned %d", resp.StatusCode)
	}
	return nil
}

func buildPayload(n Notification) discordPayload {
	if n.Kind == "system" {
		return discordPayload{Content: n.Message}
	}

	price := "preço não informado"
	if n.PriceCents != nil {
		price = formatCents(*n.PriceCents)
	}
	if n.Kind == "price_drop" && n.OldPriceCents != nil && n.PriceCents != nil {
		price = fmt.Sprintf("%s → %s", formatCents(*n.OldPriceCents), formatCents(*n.PriceCents))
	}

	location := n.City
	switch {
	case location != "" && n.State != "":
		location += "/" + n.State
	case location == "":
		location = n.State
	}

	desc := price
	if location != "" {
		desc += " · " + location
	}

	embed := discordEmbed{Title: n.ListingTitle, URL: n.ListingURL, Description: desc}
	if n.ImageURL != "" {
		embed.Thumbnail = &discordThumbnail{URL: n.ImageURL}
	}
	return discordPayload{Embeds: []discordEmbed{embed}}
}

func formatCents(cents int64) string {
	return fmt.Sprintf("R$ %d,%02d", cents/100, cents%100)
}
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/notify/ -v`
Expected: 5 testes PASS (one top-level test runs as a loop over 2 status codes, so the test binary reports it as 1 test with 2 assertions, not 2 separate results — `go test -v` output will show 4 distinct test names).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/notify/
git commit -m "feat(backend): add notifier interface and discord implementation"
```

---

### Task 5: Outbox drain loop

**Files:**
- Create: `backend/internal/notify/outbox.go`
- Test: `backend/internal/notify/outbox_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/notify/outbox_test.go`:

```go
package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/notify"
	"scrapperbook/backend/internal/testdb"
)

type fakeNotifier struct {
	sent   []notify.Notification
	failOn string // Kind to fail on; "" means never fail
}

func (f *fakeNotifier) Send(ctx context.Context, n notify.Notification) error {
	if f.failOn != "" && n.Kind == f.failOn {
		return errors.New("boom")
	}
	f.sent = append(f.sent, n)
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// insertListingAndNotification creates a minimal listing and one pending
// notification pointing at it, enough for DrainPending to build a message.
func insertListingAndNotification(t *testing.T, pool *pgxpool.Pool, dedupeKey, kind string, priceCents, oldPriceCents *int64) {
	t.Helper()
	ctx := context.Background()
	var listingID int64
	err := pool.QueryRow(ctx, `INSERT INTO listings
		(source, external_id, url, title, price_cents, currency, city, state, image_url,
		 status, fingerprint, content_hash, first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', $1, 'https://example/item/'||$1, 'Civic EG', $2, 'BRL',
		        'Curitiba', 'PR', 'https://example/civic.jpg', 'active', 'fp-'||$1, 'ch-'||$1,
		        now(), now(), now(), '{}')
		RETURNING id`, dedupeKey, priceCents).Scan(&listingID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notifications (dedupe_key, kind, listing_id, price_cents, old_price_cents)
		VALUES ($1, $2, $3, $4, $5)`, dedupeKey, kind, listingID, priceCents, oldPriceCents); err != nil {
		t.Fatal(err)
	}
}

func TestDrainPendingSendsAndMarksSent(t *testing.T) {
	pool := testdb.New(t)
	price := int64(2350000)
	insertListingAndNotification(t, pool, "new-1", "new", &price, nil)

	fake := &fakeNotifier{}
	d := notify.NewDrainer(pool, fake, testLogger())
	sent, err := d.DrainPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(fake.sent) != 1 || fake.sent[0].ListingTitle != "Civic EG" {
		t.Fatalf("notifier received = %+v", fake.sent)
	}

	var sentAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT sent_at FROM notifications WHERE dedupe_key = 'new-1'`).Scan(&sentAt); err != nil {
		t.Fatal(err)
	}
	if sentAt == nil {
		t.Fatal("sent_at still null after a successful send")
	}

	if second, err := d.DrainPending(context.Background()); err != nil || second != 0 {
		t.Fatalf("second drain = %d, %v; want 0, nil (already sent)", second, err)
	}
}

func TestDrainPendingLeavesFailedOnesUnsentAndCountsTheAttempt(t *testing.T) {
	pool := testdb.New(t)
	price, oldPrice := int64(2100000), int64(2350000)
	insertListingAndNotification(t, pool, "drop-1", "price_drop", &price, &oldPrice)

	fake := &fakeNotifier{failOn: "price_drop"}
	d := notify.NewDrainer(pool, fake, testLogger())
	sent, err := d.DrainPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0", sent)
	}

	var sentAt *time.Time
	var attempts int
	if err := pool.QueryRow(context.Background(),
		`SELECT sent_at, attempts FROM notifications WHERE dedupe_key = 'drop-1'`).Scan(&sentAt, &attempts); err != nil {
		t.Fatal(err)
	}
	if sentAt != nil {
		t.Fatal("sent_at should still be null after a failed send")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDrainPendingSendsSystemAlertsWithPlainMessage(t *testing.T) {
	pool := testdb.New(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO notifications (dedupe_key, kind, message)
		VALUES ('sys-1', 'system', 'facebook_marketplace: sessão precisa de atenção')`); err != nil {
		t.Fatal(err)
	}
	fake := &fakeNotifier{}
	d := notify.NewDrainer(pool, fake, testLogger())
	sent, err := d.DrainPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || fake.sent[0].Message != "facebook_marketplace: sessão precisa de atenção" {
		t.Fatalf("sent=%d msg=%q", sent, fake.sent[0].Message)
	}
	if fake.sent[0].ListingTitle != "" {
		t.Fatalf("system alert should have no listing title, got %q", fake.sent[0].ListingTitle)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/notify/ -run Drain -v`
Expected: `notify.NewDrainer` undefined.

- [ ] **Step 3: Implementar**

`backend/internal/notify/outbox.go`:

```go
// Outbox drain: reads pending notifications (sent_at IS NULL), joins the
// listing data a Notifier needs, sends each, and marks it sent on success.
// A send failure leaves sent_at NULL for the next drain to retry (attempts
// is incremented either way, purely for observability — at this volume, a
// handful of alerts a day for one user, a fixed per-drain retry with no
// backoff is simple and sufficient; revisit only if that stops being true).
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/metrics"
)

const drainBatchSize = 20

type Drainer struct {
	pool     *pgxpool.Pool
	notifier Notifier
	log      *slog.Logger
	Clock    func() time.Time
}

func NewDrainer(pool *pgxpool.Pool, notifier Notifier, log *slog.Logger) *Drainer {
	return &Drainer{pool: pool, notifier: notifier, log: log, Clock: time.Now}
}

type pendingNotification struct {
	id int64
	n  Notification
}

// DrainPending sends every pending notification once and returns how many
// were sent successfully.
func (d *Drainer) DrainPending(ctx context.Context) (int, error) {
	batch, err := d.loadPending(ctx)
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, p := range batch {
		if err := d.notifier.Send(ctx, p.n); err != nil {
			metrics.NotificationsTotal.WithLabelValues(p.n.Kind, "failed").Inc()
			d.log.Warn("notification send failed", "notification_id", p.id, "kind", p.n.Kind, "err", err)
			if _, uerr := d.pool.Exec(ctx, `UPDATE notifications SET attempts = attempts + 1 WHERE id = $1`, p.id); uerr != nil {
				d.log.Error("record notification attempt", "notification_id", p.id, "err", uerr)
			}
			continue
		}
		if _, err := d.pool.Exec(ctx, `UPDATE notifications SET sent_at = $2, attempts = attempts + 1 WHERE id = $1`,
			p.id, d.Clock()); err != nil {
			return sent, fmt.Errorf("mark notification %d sent: %w", p.id, err)
		}
		metrics.NotificationsTotal.WithLabelValues(p.n.Kind, "sent").Inc()
		sent++
	}
	return sent, nil
}

func (d *Drainer) loadPending(ctx context.Context) ([]pendingNotification, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT n.id, n.kind, n.message, n.price_cents, n.old_price_cents,
		       l.title, l.url, l.image_url, l.city, l.state
		FROM notifications n
		LEFT JOIN listings l ON l.id = n.listing_id
		WHERE n.sent_at IS NULL
		ORDER BY n.created_at
		LIMIT $1`, drainBatchSize)
	if err != nil {
		return nil, fmt.Errorf("select pending notifications: %w", err)
	}
	defer rows.Close()

	var batch []pendingNotification
	for rows.Next() {
		var p pendingNotification
		var message, title, url, image, city, state *string
		if err := rows.Scan(&p.id, &p.n.Kind, &message, &p.n.PriceCents, &p.n.OldPriceCents,
			&title, &url, &image, &city, &state); err != nil {
			return nil, fmt.Errorf("scan pending notification: %w", err)
		}
		p.n.Message, p.n.ListingTitle, p.n.ListingURL, p.n.ImageURL, p.n.City, p.n.State =
			deref(message), deref(title), deref(url), deref(image), deref(city), deref(state)
		batch = append(batch, p)
	}
	return batch, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/notify/ -v`
Expected: todos os testes do pacote PASS (4 do Discord + 3 do outbox = 7 resultados de teste).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/notify/
git commit -m "feat(backend): drain the notifications outbox to the configured notifier"
```

---

### Task 6: Unseen-listing sweep

**Files:**
- Create: `backend/internal/sweep/unseen.go`
- Test: `backend/internal/sweep/unseen_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/sweep/unseen_test.go`:

```go
package sweep_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/sweep"
	"scrapperbook/backend/internal/testdb"
)

var now = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func insertListing(t *testing.T, pool *pgxpool.Pool, externalID, status string, lastSeenAt time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', $1, 'https://example/item/'||$1, 'Civic', $2, 'fp-'||$1, 'ch-'||$1,
		        $3, $3, $3, '{}')
		RETURNING id`, externalID, status, lastSeenAt).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMarkUnseenFlipsOnlyStaleListings(t *testing.T) {
	pool := testdb.New(t)
	stale := insertListing(t, pool, "stale-1", "active", now.Add(-8*24*time.Hour))
	fresh := insertListing(t, pool, "fresh-1", "active", now.Add(-1*time.Hour))

	n, err := sweep.MarkUnseen(context.Background(), pool, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("marked = %d, want 1", n)
	}

	var staleStatus, freshStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM listings WHERE id = $1`, stale).Scan(&staleStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT status FROM listings WHERE id = $1`, fresh).Scan(&freshStatus); err != nil {
		t.Fatal(err)
	}
	if staleStatus != "unseen" {
		t.Fatalf("stale status = %q, want unseen", staleStatus)
	}
	if freshStatus != "active" {
		t.Fatalf("fresh status = %q, want active (untouched)", freshStatus)
	}
}

func TestMarkUnseenRecordsASnapshot(t *testing.T) {
	pool := testdb.New(t)
	id := insertListing(t, pool, "stale-2", "active", now.Add(-8*24*time.Hour))

	if _, err := sweep.MarkUnseen(context.Background(), pool, now, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}

	var status string
	var capturedAt time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT status, captured_at FROM listing_snapshots WHERE listing_id = $1`, id).Scan(&status, &capturedAt); err != nil {
		t.Fatal(err)
	}
	if status != "unseen" || !capturedAt.Equal(now) {
		t.Fatalf("snapshot status=%q captured_at=%v", status, capturedAt)
	}
}

func TestMarkUnseenIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	insertListing(t, pool, "already-unseen", "unseen", now.Add(-30*24*time.Hour))

	n, err := sweep.MarkUnseen(context.Background(), pool, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("marked = %d, want 0 (already unseen, no new snapshot)", n)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/sweep/ -v`
Expected: `package scrapperbook/backend/internal/sweep is not in std` (package doesn't exist).

- [ ] **Step 3: Implementar**

`backend/internal/sweep/unseen.go`:

```go
// Package sweep runs periodic maintenance over listings and raw_captures
// that no single request triggers: marking stale listings unseen, and
// purging old raw captures.
package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MarkUnseen flips listings that haven't been seen in `after` to 'unseen',
// recording the transition as a snapshot so history shows when a listing
// disappeared (design spec §10). Absence from a search doesn't prove
// removal — results are paginated and limited — so this is a conservative,
// time-based signal, not a definitive "sold" call.
func MarkUnseen(ctx context.Context, pool *pgxpool.Pool, now time.Time, after time.Duration) (int64, error) {
	cutoff := now.Add(-after)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, price_cents, title, description, raw_data FROM listings
		WHERE status != 'unseen' AND last_seen_at < $1
		FOR UPDATE`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("select stale listings: %w", err)
	}
	type stale struct {
		id          int64
		priceCents  *int64
		title       string
		description *string
		rawData     []byte
	}
	var staleRows []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.priceCents, &s.title, &s.description, &s.rawData); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan stale listing: %w", err)
		}
		staleRows = append(staleRows, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale listings: %w", err)
	}
	if len(staleRows) == 0 {
		return 0, tx.Commit(ctx)
	}

	ids := make([]int64, len(staleRows))
	for i, s := range staleRows {
		ids[i] = s.id
	}
	if _, err := tx.Exec(ctx, `UPDATE listings SET status = 'unseen', last_changed_at = $2, updated_at = $2
		WHERE id = ANY($1)`, ids, now); err != nil {
		return 0, fmt.Errorf("mark listings unseen: %w", err)
	}
	for _, s := range staleRows {
		if _, err := tx.Exec(ctx, `INSERT INTO listing_snapshots
			(listing_id, price_cents, title, description, status, job_id, captured_at, raw_data)
			VALUES ($1, $2, $3, $4, 'unseen', NULL, $5, $6)`,
			s.id, s.priceCents, s.title, s.description, now, s.rawData); err != nil {
			return 0, fmt.Errorf("insert unseen snapshot for listing %d: %w", s.id, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(staleRows)), nil
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/sweep/ -v`
Expected: 3 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/sweep/unseen.go backend/internal/sweep/unseen_test.go
git commit -m "feat(backend): mark stale listings unseen and record the transition"
```

---

### Task 7: raw_captures retention sweep

**Files:**
- Create: `backend/internal/sweep/retention.go`
- Test: `backend/internal/sweep/retention_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`backend/internal/sweep/retention_test.go`:

```go
package sweep_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/sweep"
	"scrapperbook/backend/internal/testdb"
)

func insertRawCapture(t *testing.T, pool *pgxpool.Pool, jobID, externalID string, capturedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO raw_captures
		(job_id, source, external_id, crawler_version, parser_version, payload, captured_at)
		VALUES ($1, 'facebook_marketplace', $2, 'test', 'test', '{}', $3)`,
		jobID, externalID, capturedAt); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeOldRawCapturesDeletesOnlyStale(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	jobID := testdb.InsertRunningJob(t, pool, seed)
	insertRawCapture(t, pool, jobID, "old-1", now.Add(-20*24*time.Hour))
	insertRawCapture(t, pool, jobID, "new-1", now.Add(-1*time.Hour))

	n, err := sweep.PurgeOldRawCaptures(context.Background(), pool, now, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}
	var remaining int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM raw_captures`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
}
```

(`now` is already declared in `unseen_test.go`, same package — no need to redeclare it here.)

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/sweep/ -run Purge -v`
Expected: `sweep.PurgeOldRawCaptures` undefined.

- [ ] **Step 3: Implementar**

`backend/internal/sweep/retention.go`:

```go
package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PurgeOldRawCaptures deletes raw_captures rows older than `retention`.
// Raw payloads exist to let a normalizer bug be fixed and re-applied
// without a re-crawl shortly after capture — not to be a permanent archive.
func PurgeOldRawCaptures(ctx context.Context, pool *pgxpool.Pool, now time.Time, retention time.Duration) (int64, error) {
	cutoff := now.Add(-retention)
	tag, err := pool.Exec(ctx, `DELETE FROM raw_captures WHERE captured_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge raw captures: %w", err)
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/sweep/ -v`
Expected: todos os 4 testes do pacote PASS (3 de `unseen_test.go` + 1 de `retention_test.go`).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/sweep/retention.go backend/internal/sweep/retention_test.go
git commit -m "feat(backend): purge raw_captures past the configured retention"
```

---

### Task 8: Catalog — regions

`catalog` owns read/write for the data a human manages (searches, regions, listings) — distinct from `jobs`, which only owns the queue. `ErrNotFound` is defined once here and reused by `searches.go`/`listings.go` in Tasks 9-10 (same package).

**Files:**
- Create: `backend/internal/catalog/regions.go`
- Test: `backend/internal/catalog/regions_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/catalog/regions_test.go`:

```go
package catalog_test

import (
	"context"
	"errors"
	"testing"

	"scrapperbook/backend/internal/catalog"
	"scrapperbook/backend/internal/testdb"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }
func strPtr(v string) *string     { return &v }
func boolPtr(v bool) *bool        { return &v }

func TestCreateGetAndListRegions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	r := catalog.Region{
		ID: "curitiba-pr", Name: "Curitiba/PR", Kind: "city",
		City: strPtr("Curitiba"), State: strPtr("PR"),
		Lat: floatPtr(-25.4284), Lng: floatPtr(-49.2733), RadiusKM: intPtr(60),
		FBLocation: strPtr("curitiba"), Enabled: true,
	}
	if err := catalog.CreateRegion(ctx, pool, r); err != nil {
		t.Fatal(err)
	}

	got, err := catalog.GetRegion(ctx, pool, "curitiba-pr")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Curitiba/PR" || *got.FBLocation != "curitiba" || !got.Enabled {
		t.Fatalf("got = %+v", got)
	}

	list, err := catalog.ListRegions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "curitiba-pr" {
		t.Fatalf("list = %+v", list)
	}
}

func TestGetRegionNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, err := catalog.GetRegion(context.Background(), pool, "nowhere")
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateRegionPatchesOnlyGivenFields(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := catalog.CreateRegion(ctx, pool, catalog.Region{
		ID: "londrina-pr", Name: "Londrina/PR", Kind: "city", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	updated, err := catalog.UpdateRegion(ctx, pool, "londrina-pr", catalog.RegionPatch{
		FBLocation: strPtr("londrina"), Enabled: boolPtr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Londrina/PR" { // untouched
		t.Errorf("name = %q, want unchanged", updated.Name)
	}
	if updated.FBLocation == nil || *updated.FBLocation != "londrina" || updated.Enabled {
		t.Fatalf("updated = %+v", updated)
	}
}

func TestUpdateRegionNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, err := catalog.UpdateRegion(context.Background(), pool, "nowhere", catalog.RegionPatch{Enabled: boolPtr(true)})
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/catalog/ -v`
Expected: `package scrapperbook/backend/internal/catalog is not in std`.

- [ ] **Step 3: Implementar**

`backend/internal/catalog/regions.go`:

```go
// Package catalog owns read/write access to the data a human manages:
// search definitions, regions, and (read-only here) the listings they
// produce. This is distinct from jobs, which only owns the queue.
package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by any Get/Update/Delete for a missing row.
var ErrNotFound = errors.New("not found")

type Region struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // city | point | state
	City       *string  `json:"city,omitempty"`
	State      *string  `json:"state,omitempty"`
	Lat        *float64 `json:"lat,omitempty"`
	Lng        *float64 `json:"lng,omitempty"`
	RadiusKM   *int     `json:"radius_km,omitempty"`
	FBLocation *string  `json:"fb_location,omitempty"`
	Enabled    bool     `json:"enabled"`
}

func CreateRegion(ctx context.Context, pool *pgxpool.Pool, r Region) error {
	if r.ID == "" || r.Name == "" || r.Kind == "" {
		return fmt.Errorf("id, name and kind are required")
	}
	_, err := pool.Exec(ctx, `INSERT INTO search_regions
		(id, name, kind, city, state, lat, lng, radius_km, fb_location, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		r.ID, r.Name, r.Kind, r.City, r.State, r.Lat, r.Lng, r.RadiusKM, r.FBLocation, r.Enabled)
	if err != nil {
		return fmt.Errorf("create region %s: %w", r.ID, err)
	}
	return nil
}

func GetRegion(ctx context.Context, pool *pgxpool.Pool, id string) (Region, error) {
	var r Region
	err := pool.QueryRow(ctx, `SELECT id, name, kind, city, state, lat, lng, radius_km, fb_location, enabled
		FROM search_regions WHERE id = $1`, id).Scan(
		&r.ID, &r.Name, &r.Kind, &r.City, &r.State, &r.Lat, &r.Lng, &r.RadiusKM, &r.FBLocation, &r.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Region{}, ErrNotFound
	}
	if err != nil {
		return Region{}, fmt.Errorf("get region %s: %w", id, err)
	}
	return r, nil
}

func ListRegions(ctx context.Context, pool *pgxpool.Pool) ([]Region, error) {
	rows, err := pool.Query(ctx, `SELECT id, name, kind, city, state, lat, lng, radius_km, fb_location, enabled
		FROM search_regions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	defer rows.Close()
	var out []Region
	for rows.Next() {
		var r Region
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.City, &r.State, &r.Lat, &r.Lng,
			&r.RadiusKM, &r.FBLocation, &r.Enabled); err != nil {
			return nil, fmt.Errorf("scan region: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegionPatch's fields are nil-means-unchanged — a PATCH, not a PUT.
type RegionPatch struct {
	Name       *string
	Kind       *string
	City       *string
	State      *string
	Lat        *float64
	Lng        *float64
	RadiusKM   *int
	FBLocation *string
	Enabled    *bool
}

func UpdateRegion(ctx context.Context, pool *pgxpool.Pool, id string, p RegionPatch) (Region, error) {
	tag, err := pool.Exec(ctx, `UPDATE search_regions SET
		name = COALESCE($2, name), kind = COALESCE($3, kind), city = COALESCE($4, city),
		state = COALESCE($5, state), lat = COALESCE($6, lat), lng = COALESCE($7, lng),
		radius_km = COALESCE($8, radius_km), fb_location = COALESCE($9, fb_location),
		enabled = COALESCE($10, enabled)
		WHERE id = $1`,
		id, p.Name, p.Kind, p.City, p.State, p.Lat, p.Lng, p.RadiusKM, p.FBLocation, p.Enabled)
	if err != nil {
		return Region{}, fmt.Errorf("update region %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return Region{}, ErrNotFound
	}
	return GetRegion(ctx, pool, id)
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/catalog/ -v`
Expected: 4 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/catalog/regions.go backend/internal/catalog/regions_test.go
git commit -m "feat(backend): add region CRUD in the catalog package"
```

---

### Task 9: Catalog — search definitions

`frequency_seconds` (not a duration string) is the API's unit — see the plan's deviations table for why.

**Files:**
- Create: `backend/internal/catalog/searches.go`
- Test: `backend/internal/catalog/searches_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/catalog/searches_test.go`:

```go
package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"scrapperbook/backend/internal/catalog"
	"scrapperbook/backend/internal/testdb"
)

func TestCreateGetAndListSearches(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	testdb.AddRegion(t, pool, "curitiba-pr", "curitiba")

	id, err := catalog.CreateSearch(ctx, pool, catalog.Search{
		Name: "Civic EG", Keyword: "Honda Civic EG", Enabled: true, Priority: 1,
		FrequencySecs: 1800,
		Filters:       json.RawMessage(`{"max_price": 30000}`),
		Alert:         json.RawMessage(`{"enabled": true, "on_new": true}`),
		RegionIDs:     []string{"curitiba-pr"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected a generated id")
	}

	got, err := catalog.GetSearch(ctx, pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Keyword != "Honda Civic EG" || got.FrequencySecs != 1800 || !got.Enabled {
		t.Fatalf("got = %+v", got)
	}
	if len(got.RegionIDs) != 1 || got.RegionIDs[0] != "curitiba-pr" {
		t.Fatalf("region_ids = %v", got.RegionIDs)
	}
	var filters map[string]any
	if err := json.Unmarshal(got.Filters, &filters); err != nil || filters["max_price"] != float64(30000) {
		t.Fatalf("filters = %s, err = %v", got.Filters, err)
	}

	list, err := catalog.ListSearches(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v", list)
	}
}

func TestCreateSearchRejectsMissingKeywordOrFrequency(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := catalog.CreateSearch(ctx, pool, catalog.Search{Name: "x", FrequencySecs: 60}); err == nil {
		t.Error("missing keyword: expected error")
	}
	if _, err := catalog.CreateSearch(ctx, pool, catalog.Search{Name: "x", Keyword: "k"}); err == nil {
		t.Error("missing frequency: expected error")
	}
}

func TestGetSearchNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, err := catalog.GetSearch(context.Background(), pool, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateSearchPatchesFieldsAndReplacesRegions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	testdb.AddRegion(t, pool, "curitiba-pr", "curitiba")
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	id, err := catalog.CreateSearch(ctx, pool, catalog.Search{
		Name: "Civic", Keyword: "Civic", Enabled: true, FrequencySecs: 1800, RegionIDs: []string{"curitiba-pr"},
	})
	if err != nil {
		t.Fatal(err)
	}

	newRegions := []string{"londrina-pr"}
	updated, err := catalog.UpdateSearch(ctx, pool, id, catalog.SearchPatch{
		Keyword: strPtr("Honda Civic"), RegionIDs: &newRegions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Keyword != "Honda Civic" || updated.Name != "Civic" { // name untouched
		t.Errorf("updated = %+v", updated)
	}
	if len(updated.RegionIDs) != 1 || updated.RegionIDs[0] != "londrina-pr" {
		t.Fatalf("region_ids after replace = %v, want only londrina-pr", updated.RegionIDs)
	}
}

func TestDeleteSearch(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	id, err := catalog.CreateSearch(ctx, pool, catalog.Search{Name: "x", Keyword: "k", FrequencySecs: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.DeleteSearch(ctx, pool, id); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.GetSearch(ctx, pool, id); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err after delete = %v, want ErrNotFound", err)
	}
	if err := catalog.DeleteSearch(ctx, pool, id); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/catalog/ -run Search -v`
Expected: `catalog.CreateSearch` undefined.

- [ ] **Step 3: Implementar**

`backend/internal/catalog/searches.go`:

```go
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Search struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Keyword       string          `json:"keyword"`
	Enabled       bool            `json:"enabled"`
	Priority      int16           `json:"priority"`
	FrequencySecs int             `json:"frequency_seconds"`
	Filters       json.RawMessage `json:"filters"`
	Alert         json.RawMessage `json:"alert"`
	RegionIDs     []string        `json:"region_ids"`
}

func CreateSearch(ctx context.Context, pool *pgxpool.Pool, s Search) (string, error) {
	if s.Keyword == "" {
		return "", fmt.Errorf("keyword is required")
	}
	if s.FrequencySecs <= 0 {
		return "", fmt.Errorf("frequency_seconds must be positive")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `INSERT INTO search_definitions
		(name, keyword, enabled, priority, frequency, filters, alert)
		VALUES ($1, $2, $3, $4, make_interval(secs => $5), $6, $7)
		RETURNING id`,
		s.Name, s.Keyword, s.Enabled, s.Priority, s.FrequencySecs, orEmptyJSON(s.Filters), orEmptyJSON(s.Alert)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create search: %w", err)
	}
	if err := linkRegions(ctx, tx, id, s.RegionIDs); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func GetSearch(ctx context.Context, pool *pgxpool.Pool, id string) (Search, error) {
	var s Search
	var freqSecs float64
	err := pool.QueryRow(ctx, `SELECT sd.id, sd.name, sd.keyword, sd.enabled, sd.priority,
		       EXTRACT(EPOCH FROM sd.frequency), sd.filters, sd.alert,
		       COALESCE(array_agg(sdr.region_id) FILTER (WHERE sdr.region_id IS NOT NULL), '{}')
		FROM search_definitions sd
		LEFT JOIN search_definition_regions sdr ON sdr.search_id = sd.id
		WHERE sd.id = $1
		GROUP BY sd.id`, id).Scan(
		&s.ID, &s.Name, &s.Keyword, &s.Enabled, &s.Priority, &freqSecs, &s.Filters, &s.Alert, &s.RegionIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return Search{}, ErrNotFound
	}
	if err != nil {
		return Search{}, fmt.Errorf("get search %s: %w", id, err)
	}
	s.FrequencySecs = int(freqSecs)
	return s, nil
}

func ListSearches(ctx context.Context, pool *pgxpool.Pool) ([]Search, error) {
	rows, err := pool.Query(ctx, `SELECT sd.id, sd.name, sd.keyword, sd.enabled, sd.priority,
		       EXTRACT(EPOCH FROM sd.frequency), sd.filters, sd.alert,
		       COALESCE(array_agg(sdr.region_id) FILTER (WHERE sdr.region_id IS NOT NULL), '{}')
		FROM search_definitions sd
		LEFT JOIN search_definition_regions sdr ON sdr.search_id = sd.id
		GROUP BY sd.id
		ORDER BY sd.created_at`)
	if err != nil {
		return nil, fmt.Errorf("list searches: %w", err)
	}
	defer rows.Close()
	var out []Search
	for rows.Next() {
		var s Search
		var freqSecs float64
		if err := rows.Scan(&s.ID, &s.Name, &s.Keyword, &s.Enabled, &s.Priority,
			&freqSecs, &s.Filters, &s.Alert, &s.RegionIDs); err != nil {
			return nil, fmt.Errorf("scan search: %w", err)
		}
		s.FrequencySecs = int(freqSecs)
		out = append(out, s)
	}
	return out, rows.Err()
}

// SearchPatch's fields are nil-means-unchanged. RegionIDs is a pointer to a
// slice: nil means unchanged, a non-nil slice (even empty) fully replaces
// the current set — this is a PATCH of the whole relationship, not an
// add/remove delta, kept simple on purpose.
type SearchPatch struct {
	Name          *string
	Keyword       *string
	Enabled       *bool
	Priority      *int16
	FrequencySecs *int
	Filters       json.RawMessage
	Alert         json.RawMessage
	RegionIDs     *[]string
}

func UpdateSearch(ctx context.Context, pool *pgxpool.Pool, id string, p SearchPatch) (Search, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Search{}, err
	}
	defer tx.Rollback(ctx)

	var freqInterval any
	if p.FrequencySecs != nil {
		freqInterval = fmt.Sprintf("%d seconds", *p.FrequencySecs)
	}
	tag, err := tx.Exec(ctx, `UPDATE search_definitions SET
		name = COALESCE($2, name), keyword = COALESCE($3, keyword), enabled = COALESCE($4, enabled),
		priority = COALESCE($5, priority), frequency = COALESCE($6::interval, frequency),
		filters = COALESCE($7, filters), alert = COALESCE($8, alert), updated_at = now()
		WHERE id = $1`,
		id, p.Name, p.Keyword, p.Enabled, p.Priority, freqInterval, nullableJSON(p.Filters), nullableJSON(p.Alert))
	if err != nil {
		return Search{}, fmt.Errorf("update search %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return Search{}, ErrNotFound
	}
	if p.RegionIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM search_definition_regions WHERE search_id = $1`, id); err != nil {
			return Search{}, fmt.Errorf("clear regions for %s: %w", id, err)
		}
		if err := linkRegions(ctx, tx, id, *p.RegionIDs); err != nil {
			return Search{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Search{}, err
	}
	return GetSearch(ctx, pool, id)
}

func DeleteSearch(ctx context.Context, pool *pgxpool.Pool, id string) error {
	tag, err := pool.Exec(ctx, `DELETE FROM search_definitions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete search %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func linkRegions(ctx context.Context, tx pgx.Tx, searchID string, regionIDs []string) error {
	for _, rid := range regionIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO search_definition_regions (search_id, region_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, searchID, rid); err != nil {
			return fmt.Errorf("link region %s: %w", rid, err)
		}
	}
	return nil
}

func orEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/catalog/ -v`
Expected: todos os testes do pacote PASS (4 de regions + 5 de searches = 9).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/catalog/searches.go backend/internal/catalog/searches_test.go
git commit -m "feat(backend): add search definition CRUD in the catalog package"
```

---

### Task 10: Catalog — listings (read-only)

Writes to `listings` already happen exclusively through `ingest.Service` (plan 1) and `sweep.MarkUnseen` (Task 6) — this is a read-only query layer for the API, not a third writer.

**Files:**
- Create: `backend/internal/catalog/listings.go`
- Test: `backend/internal/catalog/listings_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/catalog/listings_test.go`:

```go
package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/catalog"
	"scrapperbook/backend/internal/testdb"
)

func insertTestListing(t *testing.T, pool *pgxpool.Pool, externalID, city, state string, priceCents int64, seenAt time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO listings
		(source, external_id, url, title, price_cents, currency, city, state, status,
		 fingerprint, content_hash, first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', $1, 'https://example/item/'||$1, 'Civic', $2, 'BRL', $3, $4,
		        'active', 'fp-'||$1, 'ch-'||$1, $5, $5, $5, '{}')
		RETURNING id`, externalID, priceCents, city, state, seenAt).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListListingsFiltersByStateAndPrice(t *testing.T) {
	pool := testdb.New(t)
	now := time.Now().UTC()
	insertTestListing(t, pool, "1", "Curitiba", "PR", 2000000, now)
	insertTestListing(t, pool, "2", "Curitiba", "PR", 4000000, now)
	insertTestListing(t, pool, "3", "São Paulo", "SP", 2000000, now)

	pr := "PR"
	max := int64(3000000)
	got, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{State: &pr, MaxPrice: &max})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ExternalID != "1" {
		t.Fatalf("got = %+v", got)
	}
}

func TestListListingsSortsNewestFirstByDefault(t *testing.T) {
	pool := testdb.New(t)
	now := time.Now().UTC()
	insertTestListing(t, pool, "older", "Curitiba", "PR", 1000000, now.Add(-time.Hour))
	insertTestListing(t, pool, "newer", "Curitiba", "PR", 1000000, now)

	got, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ExternalID != "newer" {
		t.Fatalf("got = %+v, want newer first", got)
	}
}

func TestListListingsRejectsUnknownSortColumnSafely(t *testing.T) {
	pool := testdb.New(t)
	insertTestListing(t, pool, "1", "Curitiba", "PR", 1000000, time.Now().UTC())

	// An unrecognized sort value must not error or inject SQL — it silently
	// falls back to the default order.
	got, err := catalog.ListListings(context.Background(), pool, catalog.ListingFilter{Sort: "'; DROP TABLE listings; --"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got = %+v", got)
	}
}

func TestGetListingWithSnapshots(t *testing.T) {
	pool := testdb.New(t)
	id := insertTestListing(t, pool, "1", "Curitiba", "PR", 2350000, time.Now().UTC())
	if _, err := pool.Exec(context.Background(), `INSERT INTO listing_snapshots
		(listing_id, price_cents, title, status, captured_at, raw_data)
		VALUES ($1, 2350000, 'Civic', 'active', now(), '{}')`, id); err != nil {
		t.Fatal(err)
	}

	l, snaps, err := catalog.GetListing(context.Background(), pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if l.ExternalID != "1" {
		t.Errorf("listing = %+v", l)
	}
	if len(snaps) != 1 || *snaps[0].PriceCents != 2350000 {
		t.Fatalf("snapshots = %+v", snaps)
	}
}

func TestGetListingNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, _, err := catalog.GetListing(context.Background(), pool, 999999)
	if err != catalog.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/catalog/ -run Listing -v`
Expected: `catalog.ListListings` undefined.

- [ ] **Step 3: Implementar**

`backend/internal/catalog/listings.go`:

```go
package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

type Snapshot struct {
	PriceCents *int64    `json:"price_cents,omitempty"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	CapturedAt time.Time `json:"captured_at"`
}

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

var validSortColumns = map[string]string{
	"first_seen_at": "l.first_seen_at",
	"last_seen_at":  "l.last_seen_at",
	"price_cents":   "l.price_cents",
}

// sortClause only ever returns a hardcoded column name from the map above,
// never user input, regardless of what Sort contains — the WHERE/ORDER BY
// clause is built from literal SQL fragments plus $N placeholders, user
// *values* only ever reach the query through the parametrized args slice.
func sortClause(sort string) string {
	desc := strings.HasPrefix(sort, "-")
	col := strings.TrimPrefix(sort, "-")
	sqlCol, ok := validSortColumns[col]
	if !ok {
		return "l.first_seen_at DESC" // unrecognized sort: default, newest first
	}
	if desc {
		return sqlCol + " DESC"
	}
	return sqlCol + " ASC"
}

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
	if err != nil {
		return Listing{}, nil, fmt.Errorf("get listing %d: %w", id, err)
	}

	rows, err := pool.Query(ctx, `SELECT price_cents, title, status, captured_at
		FROM listing_snapshots WHERE listing_id = $1 ORDER BY captured_at`, id)
	if err != nil {
		return Listing{}, nil, fmt.Errorf("list snapshots for %d: %w", id, err)
	}
	defer rows.Close()
	var snaps []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(&s.PriceCents, &s.Title, &s.Status, &s.CapturedAt); err != nil {
			return Listing{}, nil, fmt.Errorf("scan snapshot for %d: %w", id, err)
		}
		snaps = append(snaps, s)
	}
	return l, snaps, rows.Err()
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/catalog/ -v`
Expected: todos os testes do pacote PASS (9 de regions+searches + 5 de listings = 14).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/catalog/listings.go backend/internal/catalog/listings_test.go
git commit -m "feat(backend): add read-only listings queries in the catalog package"
```

---

### Task 11: List queries for workers, jobs and sessions

**Files:**
- Create: `backend/internal/workers/list.go`
- Test: `backend/internal/workers/list_test.go`
- Create: `backend/internal/jobs/query.go`
- Test: `backend/internal/jobs/query_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/workers/list_test.go`:

```go
package workers_test

import (
	"context"
	"testing"

	"scrapperbook/backend/internal/testdb"
	"scrapperbook/backend/internal/workers"
)

func TestListReturnsRegisteredWorkers(t *testing.T) {
	pool := testdb.New(t)
	r := workers.New(pool)
	ctx := context.Background()
	if err := r.Register(ctx, workers.Registration{
		ID: "w1", Hostname: "box", CrawlerVersion: "0.1.0", Sources: []string{"facebook_marketplace"},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "w1" || list[0].Hostname != "box" || list[0].Status != "healthy" {
		t.Fatalf("list = %+v", list)
	}
	if len(list[0].Sources) != 1 || list[0].Sources[0] != "facebook_marketplace" {
		t.Fatalf("sources = %v", list[0].Sources)
	}
}

func TestListReturnsEmptyWhenNoWorkers(t *testing.T) {
	pool := testdb.New(t)
	r := workers.New(pool)
	list, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %+v, want empty", list)
	}
}
```

`backend/internal/jobs/query_test.go`:

```go
package jobs_test

import (
	"context"
	"testing"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/testdb"
)

func TestListJobsFiltersByStatusAndErrorKind(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)
	if _, err := q.Fail(context.Background(), j.ID, j.Attempt, "w1", jobs.KindNetwork, "boom"); err != nil {
		t.Fatal(err)
	}

	queued, err := q.ListJobs(context.Background(), jobs.JobFilter{Status: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].ID != j.ID {
		t.Fatalf("queued = %+v", queued)
	}

	byKind, err := q.ListJobs(context.Background(), jobs.JobFilter{ErrorKind: "network"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind) != 1 || byKind[0].ErrorKind == nil || *byKind[0].ErrorKind != "network" {
		t.Fatalf("by error_kind = %+v", byKind)
	}

	none, err := q.ListJobs(context.Background(), jobs.JobFilter{Status: "dead"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("dead jobs = %+v, want none", none)
	}
}

func TestListSessionsReturnsSeededSource(t *testing.T) {
	pool := testdb.New(t)
	q, _ := newQueue(t, pool, openLimits())
	sessions, err := q.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Source != "facebook_marketplace" || sessions[0].Status != "ok" {
		t.Fatalf("sessions = %+v", sessions)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/workers/ ./internal/jobs/ -run 'List' -v`
Expected: `workers.Registry.List`/`jobs.Queue.ListJobs`/`jobs.Queue.ListSessions` undefined.

- [ ] **Step 3: Implementar — `workers/list.go`**

```go
package workers

import (
	"context"
	"fmt"
	"time"
)

type Worker struct {
	ID              string     `json:"id"`
	Hostname        string     `json:"hostname"`
	Status          string     `json:"status"`
	CrawlerVersion  string     `json:"crawler_version"`
	Sources         []string   `json:"sources"`
	CurrentJobID    *string    `json:"current_job_id,omitempty"`
	JobsOK          int        `json:"jobs_ok"`
	JobsFailed      int        `json:"jobs_failed"`
	CooldownUntil   *time.Time `json:"cooldown_until,omitempty"`
	RegisteredAt    time.Time  `json:"registered_at"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at"`
}

func (r *Registry) List(ctx context.Context) ([]Worker, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, hostname, status, crawler_version, sources,
		current_job_id, jobs_ok, jobs_failed, cooldown_until, registered_at, last_heartbeat_at
		FROM workers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		var w Worker
		if err := rows.Scan(&w.ID, &w.Hostname, &w.Status, &w.CrawlerVersion, &w.Sources,
			&w.CurrentJobID, &w.JobsOK, &w.JobsFailed, &w.CooldownUntil,
			&w.RegisteredAt, &w.LastHeartbeatAt); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Implementar — `jobs/query.go`**

```go
package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type JobSummary struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Source     string     `json:"source"`
	SearchID   *string    `json:"search_id,omitempty"`
	RegionID   *string    `json:"region_id,omitempty"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	ErrorKind  *string    `json:"error_kind,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type JobFilter struct {
	Status    string
	ErrorKind string
	Limit     int // 0 or >200 falls back to 50
}

func (q *Queue) ListJobs(ctx context.Context, f JobFilter) ([]JobSummary, error) {
	query := `SELECT id, type, source, search_id, region_id, status, attempts, error_kind,
	                  created_at, started_at, finished_at
	           FROM jobs`
	var where []string
	var args []any
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.ErrorKind != "" {
		args = append(args, f.ErrorKind)
		where = append(where, fmt.Sprintf("error_kind = $%d", len(args)))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC"
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
			&j.Attempts, &j.ErrorKind, &j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

type SourceSession struct {
	Source           string     `json:"source"`
	Status           string     `json:"status"`
	LastCheckpointAt *time.Time `json:"last_checkpoint_at,omitempty"`
	CooldownUntil    *time.Time `json:"cooldown_until,omitempty"`
	Note             *string    `json:"note,omitempty"`
}

func (q *Queue) ListSessions(ctx context.Context) ([]SourceSession, error) {
	rows, err := q.pool.Query(ctx, `SELECT source, status, last_checkpoint_at, cooldown_until, note
		FROM source_sessions ORDER BY source`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []SourceSession
	for rows.Next() {
		var s SourceSession
		if err := rows.Scan(&s.Source, &s.Status, &s.LastCheckpointAt, &s.CooldownUntil, &s.Note); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/workers/ ./internal/jobs/ -v`
Expected: todos os testes PASS — os 2 novos de `workers` (total 4 no pacote) e os 2 novos de `jobs` (total 21 no pacote, somando Tasks 7/8 do plan 1 e Task 3 deste plano).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/workers/list.go backend/internal/workers/list_test.go \
  backend/internal/jobs/query.go backend/internal/jobs/query_test.go
git commit -m "feat(backend): add list queries for workers, jobs and source sessions"
```

---

### Task 12: Public API — `/api/v1` and `/metrics`

Wires Tasks 8-11's `catalog`/`workers`/`jobs` query layers behind HTTP. Lives in the same `httpapi` package as `Internal` (plan 1, Task 10) so it reuses `respond.go`'s `writeJSON`/`writeError`/`decodeJSON`/`isUUID` without duplicating them — see this plan's deviations table.

**Files:**
- Create: `backend/internal/httpapi/public.go`
- Create: `backend/internal/httpapi/searches.go`
- Create: `backend/internal/httpapi/regions.go`
- Create: `backend/internal/httpapi/listings.go`
- Create: `backend/internal/httpapi/ops.go`
- Create: `backend/internal/httpapi/stats.go`
- Create: `backend/internal/httpapi/metrics.go`
- Test: `backend/internal/httpapi/public_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/httpapi/public_test.go`:

```go
package httpapi_test

import (
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

type publicEnv struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
}

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

func doJSON(t *testing.T, method, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reqBody = bytesReader(b)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.ContentLength != 0 {
		_ = json.NewDecoder(resp.Body).Decode(&out)
	}
	return resp, out
}

func TestRegionAndSearchLifecycle(t *testing.T) {
	e := setupPublic(t)

	resp, _ := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/regions", map[string]any{
		"id": "curitiba-pr", "name": "Curitiba/PR", "kind": "city", "fb_location": "curitiba", "enabled": true,
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("create region status = %d", resp.StatusCode)
	}

	resp, created := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/searches", map[string]any{
		"name": "Civic EG", "keyword": "Honda Civic EG", "enabled": true,
		"frequency_seconds": 1800, "region_ids": []string{"curitiba-pr"},
		"filters": map[string]any{"max_price": 30000},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create search status = %d", resp.StatusCode)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("no id returned")
	}

	resp, got := doJSON(t, http.MethodGet, e.srv.URL+"/api/v1/searches/"+id, nil)
	if resp.StatusCode != http.StatusOK || got["keyword"] != "Honda Civic EG" {
		t.Fatalf("get search status=%d body=%v", resp.StatusCode, got)
	}

	resp, _ = doJSON(t, http.MethodGet, e.srv.URL+"/api/v1/searches", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list searches status = %d", resp.StatusCode)
	}

	resp, updated := doJSON(t, http.MethodPatch, e.srv.URL+"/api/v1/searches/"+id, map[string]any{"enabled": false})
	if resp.StatusCode != http.StatusOK || updated["enabled"] != false {
		t.Fatalf("update search status=%d body=%v", resp.StatusCode, updated)
	}

	resp, _ = doJSON(t, http.MethodDelete, e.srv.URL+"/api/v1/searches/"+id, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete search status = %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, http.MethodGet, e.srv.URL+"/api/v1/searches/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete status = %d", resp.StatusCode)
	}
}

func TestListingsFilterByStateAndPrice(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO listings
		(source, external_id, url, title, price_cents, currency, city, state, status,
		 fingerprint, content_hash, first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 2000000, 'BRL', 'Curitiba', 'PR',
		        'active', 'fp1', 'ch1', now(), now(), now(), '{}'),
		       ('facebook_marketplace', '2', 'https://x/2', 'Civic', 5000000, 'BRL', 'São Paulo', 'SP',
		        'active', 'fp2', 'ch2', now(), now(), now(), '{}')`); err != nil {
		t.Fatal(err)
	}

	resp, body := doJSON(t, http.MethodGet, e.srv.URL+"/api/v1/listings?state=PR&max_price=3000000", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	listJSON, err := json.Marshal(body) // re-marshal is awkward for a top-level array; decode fresh instead
	_ = listJSON
	_ = err

	// doJSON decodes into map[string]any, which doesn't fit a top-level
	// JSON array response — fetch and decode this one directly instead.
	resp2, err := http.Get(e.srv.URL + "/api/v1/listings?state=PR&max_price=3000000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["external_id"] != "1" {
		t.Fatalf("list = %v", list)
	}
}

func TestOpsEndpointsAndResume(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	w := workers.New(e.pool)
	if err := w.Register(ctx, workers.Registration{ID: "w1", Hostname: "box", CrawlerVersion: "0.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE source_sessions SET status = 'needs_attention'`); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/workers")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("list workers status=%d err=%v", resp.StatusCode, err)
	}
	resp.Body.Close()

	resp, err = http.Get(e.srv.URL + "/api/v1/sessions")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("list sessions status=%d err=%v", resp.StatusCode, err)
	}
	var sessions []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&sessions)
	resp.Body.Close()
	if len(sessions) != 1 || sessions[0]["status"] != "needs_attention" {
		t.Fatalf("sessions = %v", sessions)
	}

	resp, _ = doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/sessions/facebook_marketplace/resume", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("resume status = %d", resp.StatusCode)
	}
	var status string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM source_sessions WHERE source = 'facebook_marketplace'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatalf("status after resume = %q", status)
	}

	resp, err = http.Get(e.srv.URL + "/api/v1/jobs")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("list jobs status=%d err=%v", resp.StatusCode, err)
	}
	resp.Body.Close()
}

func TestStatsReturnsExpectedShape(t *testing.T) {
	e := setupPublic(t)
	resp, body := doJSON(t, http.MethodGet, e.srv.URL+"/api/v1/stats", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, key := range []string{"total_listings", "listings_by_state", "enabled_searches", "jobs_last_24h", "workers_by_status"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in %v", key, body)
		}
	}
}

func TestMetricsEndpointServesPrometheusFormat(t *testing.T) {
	e := setupPublic(t)
	resp, err := http.Get(e.srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !contains(string(body), "jobs_total") {
		t.Fatalf("expected jobs_total metric name in output, got %d bytes", len(body))
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
```

Add this tiny helper at the bottom of the same file (Go's `bytes.NewReader` needs the `bytes` package, kept as a one-liner so the imports above stay focused):

```go
func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}
```

...and add `"bytes"` to the import block.

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/httpapi/ -run 'Region\|Listings\|Ops\|Stats\|Metrics' -v`
Expected: `httpapi.Public` undefined.

- [ ] **Step 3: Implementar `public.go`**

```go
// Public serves the human-facing API under /api/v1 — read/write for
// searches and regions, read-only for listings/jobs/workers/sessions, plus
// aggregate stats. No auth: bound to 127.0.0.1 only (see compose.yaml),
// consistent with this being a single-user tool.
package httpapi

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

func (h *Public) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/searches", h.listSearches)
	mux.HandleFunc("POST /api/v1/searches", h.createSearch)
	mux.HandleFunc("GET /api/v1/searches/{id}", h.getSearch)
	mux.HandleFunc("PATCH /api/v1/searches/{id}", h.updateSearch)
	mux.HandleFunc("DELETE /api/v1/searches/{id}", h.deleteSearch)

	mux.HandleFunc("GET /api/v1/regions", h.listRegions)
	mux.HandleFunc("POST /api/v1/regions", h.createRegion)
	mux.HandleFunc("PATCH /api/v1/regions/{id}", h.updateRegion)

	mux.HandleFunc("GET /api/v1/listings", h.listListings)
	mux.HandleFunc("GET /api/v1/listings/{id}", h.getListing)

	mux.HandleFunc("GET /api/v1/jobs", h.listJobs)
	mux.HandleFunc("GET /api/v1/workers", h.listWorkers)
	mux.HandleFunc("GET /api/v1/sessions", h.listSessions)
	mux.HandleFunc("POST /api/v1/sessions/{source}/resume", h.resumeSession)

	mux.HandleFunc("GET /api/v1/stats", h.stats)
}

func internalErrorPublic(w http.ResponseWriter, log *slog.Logger, op string, err error) {
	log.Error(op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
```

- [ ] **Step 4: Implementar `searches.go`**

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"scrapperbook/backend/internal/catalog"
)

func (h *Public) listSearches(w http.ResponseWriter, r *http.Request) {
	list, err := catalog.ListSearches(r.Context(), h.Pool)
	if err != nil {
		internalErrorPublic(w, h.Log, "list searches", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) createSearch(w http.ResponseWriter, r *http.Request) {
	var s catalog.Search
	if !decodeJSON(w, r, &s) {
		return
	}
	id, err := catalog.CreateSearch(r.Context(), h.Pool, s)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *Public) getSearch(w http.ResponseWriter, r *http.Request) {
	s, err := catalog.GetSearch(r.Context(), h.Pool, r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "get search", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Public) updateSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          *string         `json:"name"`
		Keyword       *string         `json:"keyword"`
		Enabled       *bool           `json:"enabled"`
		Priority      *int16          `json:"priority"`
		FrequencySecs *int            `json:"frequency_seconds"`
		Filters       json.RawMessage `json:"filters"`
		Alert         json.RawMessage `json:"alert"`
		RegionIDs     *[]string       `json:"region_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	s, err := catalog.UpdateSearch(r.Context(), h.Pool, r.PathValue("id"), catalog.SearchPatch{
		Name: body.Name, Keyword: body.Keyword, Enabled: body.Enabled, Priority: body.Priority,
		FrequencySecs: body.FrequencySecs, Filters: body.Filters, Alert: body.Alert, RegionIDs: body.RegionIDs,
	})
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "update search", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Public) deleteSearch(w http.ResponseWriter, r *http.Request) {
	err := catalog.DeleteSearch(r.Context(), h.Pool, r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "delete search", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Implementar `regions.go`**

```go
package httpapi

import (
	"errors"
	"net/http"

	"scrapperbook/backend/internal/catalog"
)

func (h *Public) listRegions(w http.ResponseWriter, r *http.Request) {
	list, err := catalog.ListRegions(r.Context(), h.Pool)
	if err != nil {
		internalErrorPublic(w, h.Log, "list regions", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) createRegion(w http.ResponseWriter, r *http.Request) {
	var reg catalog.Region
	if !decodeJSON(w, r, &reg) {
		return
	}
	if err := catalog.CreateRegion(r.Context(), h.Pool, reg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Public) updateRegion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       *string  `json:"name"`
		Kind       *string  `json:"kind"`
		City       *string  `json:"city"`
		State      *string  `json:"state"`
		Lat        *float64 `json:"lat"`
		Lng        *float64 `json:"lng"`
		RadiusKM   *int     `json:"radius_km"`
		FBLocation *string  `json:"fb_location"`
		Enabled    *bool    `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	reg, err := catalog.UpdateRegion(r.Context(), h.Pool, r.PathValue("id"), catalog.RegionPatch{
		Name: body.Name, Kind: body.Kind, City: body.City, State: body.State,
		Lat: body.Lat, Lng: body.Lng, RadiusKM: body.RadiusKM, FBLocation: body.FBLocation, Enabled: body.Enabled,
	})
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "region not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "update region", err)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}
```

- [ ] **Step 6: Implementar `listings.go`**

```go
package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"scrapperbook/backend/internal/catalog"
)

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
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	list, err := catalog.ListListings(r.Context(), h.Pool, f)
	if err != nil {
		internalErrorPublic(w, h.Log, "list listings", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) getListing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown listing")
		return
	}
	listing, snaps, err := catalog.GetListing(r.Context(), h.Pool, id)
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "listing not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "get listing", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listing": listing, "snapshots": snaps})
}
```

- [ ] **Step 7: Implementar `ops.go`**

```go
package httpapi

import (
	"net/http"

	"scrapperbook/backend/internal/jobs"
)

func (h *Public) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := h.Queue.ListJobs(r.Context(), jobs.JobFilter{Status: q.Get("status"), ErrorKind: q.Get("error_kind")})
	if err != nil {
		internalErrorPublic(w, h.Log, "list jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) listWorkers(w http.ResponseWriter, r *http.Request) {
	list, err := h.Workers.List(r.Context())
	if err != nil {
		internalErrorPublic(w, h.Log, "list workers", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Queue.ListSessions(r.Context())
	if err != nil {
		internalErrorPublic(w, h.Log, "list sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) resumeSession(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	if err := h.Queue.ResumeSource(r.Context(), source); err != nil {
		internalErrorPublic(w, h.Log, "resume session", err)
		return
	}
	h.Log.Info("session resumed", "source", source)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 8: Implementar `stats.go`**

```go
package httpapi

import "net/http"

type statsResponse struct {
	TotalListings   int            `json:"total_listings"`
	ListingsByState map[string]int `json:"listings_by_state"`
	EnabledSearches int            `json:"enabled_searches"`
	JobsLast24h     map[string]int `json:"jobs_last_24h"`
	WorkersByStatus map[string]int `json:"workers_by_status"`
}

func (h *Public) stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := statsResponse{
		ListingsByState: map[string]int{}, JobsLast24h: map[string]int{}, WorkersByStatus: map[string]int{},
	}

	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM listings`).Scan(&resp.TotalListings); err != nil {
		internalErrorPublic(w, h.Log, "stats total listings", err)
		return
	}

	rows, err := h.Pool.Query(ctx, `SELECT COALESCE(state, ''), count(*) FROM listings GROUP BY state`)
	if err != nil {
		internalErrorPublic(w, h.Log, "stats listings by state", err)
		return
	}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			rows.Close()
			internalErrorPublic(w, h.Log, "stats listings by state scan", err)
			return
		}
		if state != "" {
			resp.ListingsByState[state] = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internalErrorPublic(w, h.Log, "stats listings by state iterate", err)
		return
	}

	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM search_definitions WHERE enabled`).Scan(&resp.EnabledSearches); err != nil {
		internalErrorPublic(w, h.Log, "stats enabled searches", err)
		return
	}

	jobRows, err := h.Pool.Query(ctx,
		`SELECT status, count(*) FROM jobs WHERE created_at > now() - interval '24 hours' GROUP BY status`)
	if err != nil {
		internalErrorPublic(w, h.Log, "stats jobs", err)
		return
	}
	for jobRows.Next() {
		var status string
		var n int
		if err := jobRows.Scan(&status, &n); err != nil {
			jobRows.Close()
			internalErrorPublic(w, h.Log, "stats jobs scan", err)
			return
		}
		resp.JobsLast24h[status] = n
	}
	jobRows.Close()
	if err := jobRows.Err(); err != nil {
		internalErrorPublic(w, h.Log, "stats jobs iterate", err)
		return
	}

	workerRows, err := h.Pool.Query(ctx, `SELECT status, count(*) FROM workers GROUP BY status`)
	if err != nil {
		internalErrorPublic(w, h.Log, "stats workers", err)
		return
	}
	for workerRows.Next() {
		var status string
		var n int
		if err := workerRows.Scan(&status, &n); err != nil {
			workerRows.Close()
			internalErrorPublic(w, h.Log, "stats workers scan", err)
			return
		}
		resp.WorkersByStatus[status] = n
	}
	workerRows.Close()
	if err := workerRows.Err(); err != nil {
		internalErrorPublic(w, h.Log, "stats workers iterate", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 9: Implementar `metrics.go`**

```go
package httpapi

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics registers GET /metrics using promhttp's handler against the
// default Prometheus registry — the same one the metrics package's
// promauto vars register themselves into.
func Metrics(mux *http.ServeMux) {
	mux.Handle("GET /metrics", promhttp.Handler())
}
```

- [ ] **Step 10: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/httpapi/ -v`
Expected: todos os testes do pacote PASS — os 5 novos (`TestRegionAndSearchLifecycle`, `TestListingsFilterByStateAndPrice`, `TestOpsEndpointsAndResume`, `TestStatsReturnsExpectedShape`, `TestMetricsEndpointServesPrometheusFormat`) e os 5 já existentes do plano 1 (Task 10).

- [ ] **Step 11: Commit**

```bash
git add backend/internal/httpapi/
git commit -m "feat(backend): add public /api/v1 and /metrics endpoints"
```

---

### Task 13: Wire notifications, sweeps and the public API into `main.go`

No new behavior to unit test here — this task wires already-tested packages (Tasks 2-12) into the process entrypoint. Verified by Task 15's Docker smoke test, not by a Go test file.

**Files:**
- Modify: `backend/cmd/server/main.go`
- Modify: `compose.yaml`
- Modify: `.env.example`

- [ ] **Step 1: Atualizar imports**

In `backend/cmd/server/main.go`, replace:

```go
	"scrapperbook/backend/internal/config"
	"scrapperbook/backend/internal/db"
	"scrapperbook/backend/internal/httpapi"
	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/source/facebook"
	"scrapperbook/backend/internal/workers"
)
```

With:

```go
	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/config"
	"scrapperbook/backend/internal/db"
	"scrapperbook/backend/internal/httpapi"
	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/notify"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/source/facebook"
	"scrapperbook/backend/internal/sweep"
	"scrapperbook/backend/internal/workers"
)
```

- [ ] **Step 2: Montar o notifier, o Public handler e registrar as novas rotas**

Replace:

```go
	queue := jobs.New(pool, cfg.Limits)
	sched := scheduler.New(pool, queue, []string{facebook.Source}, log)
	internal := &httpapi.Internal{
		Token:   cfg.WorkerToken,
		Pool:    pool,
		Queue:   queue,
		Workers: workers.New(pool),
		Ingest:  ingest.New(ingest.NewRegistry(facebook.Normalizer{}), log),
		Log:     log,
	}
	mux := http.NewServeMux()
	internal.Routes(mux)
	httpapi.Health(mux, pool)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		background(ctx, log, sched, queue)
	}()
	// Wait for background to actually exit before pool.Close() (deferred
	// above) runs: without this, a ticker firing right as ctx is canceled
	// could still be mid-query against pool when Close() is called.
	defer wg.Wait()
```

With:

```go
	queue := jobs.New(pool, cfg.Limits)
	sched := scheduler.New(pool, queue, []string{facebook.Source}, log)
	internal := &httpapi.Internal{
		Token:   cfg.WorkerToken,
		Pool:    pool,
		Queue:   queue,
		Workers: workers.New(pool),
		Ingest:  ingest.New(ingest.NewRegistry(facebook.Normalizer{}), log),
		Log:     log,
	}
	public := &httpapi.Public{Pool: pool, Queue: queue, Workers: workers.New(pool), Log: log}

	// A nil drainer disables the Discord drain step in background() below —
	// there's nothing to send to without a webhook URL, and that's a valid,
	// expected single-user setup (not an error).
	var drainer *notify.Drainer
	if cfg.DiscordWebhookURL != "" {
		drainer = notify.NewDrainer(pool, notify.NewDiscordNotifier(cfg.DiscordWebhookURL), log)
	} else {
		log.Info("discord notifications disabled: DISCORD_WEBHOOK_URL not set")
	}

	mux := http.NewServeMux()
	internal.Routes(mux)
	public.Routes(mux)
	httpapi.Metrics(mux)
	httpapi.Health(mux, pool)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		background(ctx, log, sched, queue, drainer, pool, cfg)
	}()
	go func() {
		defer wg.Done()
		backgroundRetention(ctx, log, pool, cfg)
	}()
	// Wait for both background loops to actually exit before pool.Close()
	// (deferred above) runs: without this, a ticker firing right as ctx is
	// canceled could still be mid-query against pool when Close() is called.
	defer wg.Wait()
```

- [ ] **Step 3: Estender `background`/`runTickSafely` e adicionar o loop de retenção**

Replace:

```go
// background runs the scheduler tick and lease reaper once a minute. A
// panic in either is recovered per-iteration so one bad tick can't crash
// the whole process (and take the HTTP server down with it) — it logs and
// the loop just tries again next minute.
func background(ctx context.Context, log *slog.Logger, sched *scheduler.Scheduler, queue *jobs.Queue) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		runTickSafely(ctx, log, sched, queue)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runTickSafely(ctx context.Context, log *slog.Logger, sched *scheduler.Scheduler, queue *jobs.Queue) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("background tick panicked", "panic", r)
		}
	}()
	if n, err := sched.Tick(ctx); err != nil {
		log.Error("scheduler tick", "err", err)
	} else if n > 0 {
		log.Info("jobs enqueued", "count", n)
	}
	if n, err := queue.ReapExpiredLeases(ctx); err != nil {
		log.Error("reap leases", "err", err)
	} else if n > 0 {
		log.Warn("leases expired", "count", n)
	}
}
```

With:

```go
// background runs the scheduler tick, lease reaper, Discord drain and
// unseen sweep once a minute. A panic in any one step is recovered
// per-iteration so one bad tick can't crash the whole process (and take
// the HTTP server down with it) — it logs and the loop just tries again
// next minute.
func background(
	ctx context.Context, log *slog.Logger, sched *scheduler.Scheduler, queue *jobs.Queue,
	drainer *notify.Drainer, pool *pgxpool.Pool, cfg config.Config,
) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		runTickSafely(ctx, log, sched, queue, drainer, pool, cfg)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runTickSafely(
	ctx context.Context, log *slog.Logger, sched *scheduler.Scheduler, queue *jobs.Queue,
	drainer *notify.Drainer, pool *pgxpool.Pool, cfg config.Config,
) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("background tick panicked", "panic", r)
		}
	}()
	if n, err := sched.Tick(ctx); err != nil {
		log.Error("scheduler tick", "err", err)
	} else if n > 0 {
		log.Info("jobs enqueued", "count", n)
	}
	if n, err := queue.ReapExpiredLeases(ctx); err != nil {
		log.Error("reap leases", "err", err)
	} else if n > 0 {
		log.Warn("leases expired", "count", n)
	}
	if drainer != nil {
		if n, err := drainer.DrainPending(ctx); err != nil {
			log.Error("drain notifications", "err", err)
		} else if n > 0 {
			log.Info("notifications sent", "count", n)
		}
	}
	if n, err := sweep.MarkUnseen(ctx, pool, time.Now(), cfg.ListingUnseenAfter); err != nil {
		log.Error("mark unseen", "err", err)
	} else if n > 0 {
		log.Info("listings marked unseen", "count", n)
	}
}

// backgroundRetention purges old raw_captures rows once an hour — a
// DELETE that can touch a lot of rows, so it runs far less often than the
// per-minute loop above.
func backgroundRetention(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, cfg config.Config) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		runRetentionSafely(ctx, log, pool, cfg)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runRetentionSafely(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, cfg config.Config) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("retention tick panicked", "panic", r)
		}
	}()
	if n, err := sweep.PurgeOldRawCaptures(ctx, pool, time.Now(), cfg.RawCapturesRetention); err != nil {
		log.Error("purge raw captures", "err", err)
	} else if n > 0 {
		log.Info("raw captures purged", "count", n)
	}
}
```

- [ ] **Step 4: Atualizar `compose.yaml`**

In the `api` service's `environment:` block, add two new vars (`DISCORD_WEBHOOK_URL` was already added to `.env.example` in plan 1 but never read into `compose.yaml`'s `api` environment — add it here too):

```yaml
    environment:
      DATABASE_URL: postgres://monitor:${POSTGRES_PASSWORD}@postgres:5432/monitor?sslmode=disable
      WORKER_TOKEN: ${WORKER_TOKEN:?set WORKER_TOKEN in .env}
      QUIET_HOURS: ${QUIET_HOURS:-23-7}
      FB_MAX_JOBS_PER_HOUR: ${FB_MAX_JOBS_PER_HOUR:-6}
      FB_MIN_GAP: ${FB_MIN_GAP:-4m}
      DISCORD_WEBHOOK_URL: ${DISCORD_WEBHOOK_URL:-}
      LISTING_UNSEEN_AFTER: ${LISTING_UNSEEN_AFTER:-168h}
      RAW_CAPTURES_RETENTION: ${RAW_CAPTURES_RETENTION:-336h}
```

- [ ] **Step 5: Atualizar `.env.example`**

Add below the existing `DISCORD_WEBHOOK_URL=` line:

```
# Formato aceito pelo Go time.ParseDuration (ex.: 168h). Default já é esse.
LISTING_UNSEEN_AFTER=168h
RAW_CAPTURES_RETENTION=336h
```

- [ ] **Step 6: Confirmar que compila**

Run: `cd backend && go build ./...`
Expected: sem erros.

- [ ] **Step 7: Commit**

```bash
git add backend/cmd/server/main.go compose.yaml .env.example
git commit -m "feat(backend): wire discord drain, sweeps and public API into server entrypoint"
```

---

### Task 14 (HUMAN ONLY — do not dispatch to a subagent): confirmar notificação real no Discord

Same reasoning as plan 2's human-only login task: this posts to an external, user-owned Discord webhook. No subagent, automated or otherwise, should hold or exercise that credential.

**What the human does:**

1. Create a Discord webhook in any channel you control (Server Settings → Integrations → Webhooks → New Webhook → Copy URL).
2. Set `DISCORD_WEBHOOK_URL` in `.env` to that URL.
3. `docker compose up -d --build api` to pick up the new env var and the Task 13 code.
4. Trigger a real notification — easiest path is a price-drop: pick a `listings` row currently `active` and run a manual `UPDATE listings SET price_cents = price_cents - 100 WHERE id = ...;` then manually insert a matching row into `notifications` (kind `price_drop`, referencing that listing), **or** just wait for the next real crawl tick to produce a new listing naturally (`kind = 'new'`).
5. Within a minute, confirm a message actually arrives in the Discord channel with the expected title/price/city.
6. Report back: did the message arrive, did the embed look right (title, price, city/state, thumbnail)?

This task is marked done only once the human confirms a real message was seen in their own Discord channel.

---

### Task 15: Verificação final

- [ ] **Step 1: Suíte completa**

Run: `cd backend && go test ./...`
Expected: all packages PASS, including the new `metrics`, `notify`, `sweep`, `catalog`, `workers`, `jobs` and `httpapi` tests added by this plan.

- [ ] **Step 2: Smoke test contra a stack rodando**

With `docker compose up -d` already running (postgres + api + crawler from plans 1-2):

```bash
curl -s http://localhost:8080/api/v1/regions | jq .
curl -s http://localhost:8080/api/v1/searches | jq .
curl -s http://localhost:8080/api/v1/listings?limit=5 | jq .
curl -s http://localhost:8080/api/v1/jobs?limit=5 | jq .
curl -s http://localhost:8080/api/v1/workers | jq .
curl -s http://localhost:8080/api/v1/sessions | jq .
curl -s http://localhost:8080/api/v1/stats | jq .
curl -s http://localhost:8080/metrics | grep jobs_total
```

Expected: all return `200` with real data reflecting the live Honda Civic EG/Curitiba search seeded in plan 2 — nonzero `total_listings` in `/stats`, nonzero `jobs_total` series in `/metrics`.

- [ ] **Step 3: Checklist de critérios de pronto (contra o spec, seção 11)**

- [ ] Notificações no Discord funcionam de ponta a ponta (Task 14 confirmado pelo humano).
- [ ] `/api/v1/searches` e `/api/v1/regions` permitem CRUD completo sem precisar editar o banco na mão.
- [ ] `/api/v1/listings` permite filtrar por estado/cidade/preço/status.
- [ ] `/api/v1/jobs`, `/workers`, `/sessions` dão visibilidade operacional sem acessar o Postgres direto.
- [ ] `/api/v1/sessions/{source}/resume` desbloqueia uma fonte pausada por `checkpoint`/`login_required` sem reiniciar o container.
- [ ] `/metrics` expõe contadores que um Prometheus/Grafana local poderia raspar.
- [ ] Listagens que somem do Marketplace são marcadas `unseen` depois de `LISTING_UNSEEN_AFTER` sem intervenção manual.
- [ ] `raw_captures` antigas são purgadas automaticamente depois de `RAW_CAPTURES_RETENTION`.

Com isso, o roadmap definido nos planos 1 e 2 (alertas + API pública) está completo. O próximo item do roadmap — dashboard Next.js — fica para o plano 4, fora do escopo deste.
