# Backend Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Servidor Go que armazena buscas, gera jobs, entrega jobs a workers via API HTTP interna, recebe resultados brutos e os transforma em anúncios deduplicados, com histórico e alertas pendentes em outbox.

**Architecture:** Um binário Go (`cmd/server`) com API interna, scheduler e reaper de leases. PostgreSQL é a fonte de verdade e também a fila (`FOR UPDATE SKIP LOCKED`). Toda lógica de tempo usa um relógio injetado (`Clock`), nunca `now()` do banco, para que os testes sejam determinísticos.

**Tech Stack:** Go 1.26, pgx v5, goose v3, testcontainers-go, `net/http` stdlib, `log/slog`, Docker Compose, PostgreSQL 17.

**Spec:** `docs/superpowers/specs/2026-10-09-marketplace-monitor-mvp-design.md`

---

## Planos do MVP

Este é o plano 1 de 4. Cada plano gera software funcional e testável:

1. **Backend core** (este): schema, fila, API interna, ingest, scheduler. Testável com `curl` simulando um worker.
2. **Crawler Python:** worker Playwright, login via noVNC, captura GraphQL, contratos em `docs/contracts/`.
3. **Notificação e API pública:** notifier Discord, `/api/v1`, `/metrics`, marcação `unseen`, retenção de `raw_captures`.
4. **Dashboard:** Next.js.

## Desvios da spec (intencionais)

| Spec | Plano | Motivo |
|---|---|---|
| sqlc | SQL escrito à mão com pgx | ~20 queries; evita um passo de geração de código. |
| gocron v2 | `time.Ticker` | Só existe um tick de 1 minuto. |
| `jobs.status` inclui `failed` | Sem `failed` | Retry volta o job para `queued`; esgotado vira `dead`. |
| `source_sessions.status` inclui `cooldown` | Só `ok` e `needs_attention` | Cooldown é representado por `cooldown_until`. |
| `UNIQUE (listing_id, search_id, kind, price_cents)` em `notifications` | Coluna `dedupe_key UNIQUE` | Colunas nulas (alertas de sistema, anúncio sem preço) quebrariam a unicidade composta. |
| Unique de job em voo `(search_id, region_id)` | `(source, search_id, region_id)` | Permite a mesma busca em várias fontes no futuro. |
| — | `search_definition_regions.last_success_at` | Necessário para calcular `stop_before`. |
| — | `raw_captures.external_id` | Permite re-normalizar item a item. |

## Estrutura de arquivos

```text
.gitignore
.env.example
README.md
compose.yaml
backend/
├── go.mod, go.sum
├── Dockerfile
├── cmd/server/main.go                 wiring, loops, shutdown
├── migrations/
│   ├── embed.go                       embed.FS das migrations
│   └── 00001_init.sql                 schema completo
├── seed/
│   ├── dev.sql                        região + busca de exemplo
│   └── smoke-result.json              corpo de result para smoke test
└── internal/
    ├── config/config.go               env → Config
    ├── db/
    │   ├── db.go                      Connect, Migrate
    │   └── dbtx.go                    interface DBTX (pool ou tx)
    ├── testdb/testdb.go               Postgres descartável para testes + seeds
    ├── listing/listing.go             Canonical, ContentHash, Fingerprint, NormalizeText
    ├── source/facebook/
    │   ├── normalizer.go              raw GraphQL node → Canonical
    │   └── testdata/listing_node.json fixture sintética
    ├── search/alert.go                AlertRule
    ├── ingest/
    │   ├── registry.go                Normalizer + Registry
    │   └── ingest.go                  upsert, snapshot, search_listings, outbox, baseline
    ├── jobs/
    │   ├── queue.go                   tipos, Enqueue, Claim, QuietHours
    │   └── lifecycle.go               Complete, Fail, ReapExpiredLeases, ResumeSource
    ├── workers/workers.go             Register, Heartbeat
    ├── scheduler/scheduler.go         Tick
    └── httpapi/
        ├── respond.go                 helpers JSON, isUUID
        ├── internal.go                /internal/v1/*
        └── health.go                  /healthz, /readyz
```

Pré-requisitos na máquina: Go 1.26+, Docker em execução (testcontainers sobe Postgres nos testes).

---

### Task 1: Scaffold do repositório

**Files:**
- Create: `.gitignore`, `.env.example`, `README.md`, `compose.yaml`, `backend/go.mod`

- [ ] **Step 1: Criar `.gitignore`**

```gitignore
.env
*.local
/backend/bin/
/crawler/.venv/
/crawler/profile/
/debug/
__pycache__/
node_modules/
.next/
```

- [ ] **Step 2: Criar `.env.example`**

```dotenv
# Use somente [A-Za-z0-9] na senha: ela entra numa URL de conexão.
POSTGRES_PASSWORD=change0me
WORKER_TOKEN=change0me
DISCORD_WEBHOOK_URL=
# "off" desliga as quiet hours (útil para smoke tests).
QUIET_HOURS=23-7
FB_MAX_JOBS_PER_HOUR=6
FB_MIN_GAP=4m
```

- [ ] **Step 3: Criar `compose.yaml` (só Postgres por enquanto)**

```yaml
services:
  postgres:
    image: postgres:17
    environment:
      POSTGRES_USER: monitor
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: monitor
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U monitor -d monitor"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  pgdata:
```

- [ ] **Step 4: Criar `README.md`**

```markdown
# scrapperbook

Monitor pessoal de anúncios do Facebook Marketplace.

Design: `docs/superpowers/specs/2026-10-09-marketplace-monitor-mvp-design.md`

## Rodar

    cp .env.example .env   # edite as senhas
    docker compose up -d --build

## Testes do backend

Requer Docker em execução.

    cd backend && go test ./...
```

- [ ] **Step 5: Inicializar o módulo Go**

Run: `cd backend && go mod init scrapperbook/backend && go mod edit -go=1.26`
Expected: cria `backend/go.mod` com `module scrapperbook/backend`.

- [ ] **Step 6: Validar o compose**

Run: `cp .env.example .env && docker compose config --quiet && echo OK`
Expected: `OK`

- [ ] **Step 7: Commit**

```bash
git add .gitignore .env.example README.md compose.yaml backend/go.mod
git commit -m "chore: scaffold repository and postgres compose service"
```

---

### Task 2: Schema, migrations e banco de teste

**Files:**
- Create: `backend/migrations/00001_init.sql`, `backend/migrations/embed.go`
- Create: `backend/internal/db/db.go`, `backend/internal/db/dbtx.go`
- Create: `backend/internal/testdb/testdb.go`
- Test: `backend/internal/db/db_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`backend/internal/db/db_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"scrapperbook/backend/internal/testdb"
)

func TestMigrateCreatesSchema(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	tables := []string{
		"search_regions", "search_definitions", "search_definition_regions",
		"jobs", "raw_captures", "listings", "listing_snapshots",
		"search_listings", "notifications", "workers", "source_sessions",
	}
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = ANY($1)`, tables).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(tables) {
		t.Fatalf("found %d of %d tables", n, len(tables))
	}

	var status string
	err = pool.QueryRow(ctx,
		`SELECT status FROM source_sessions WHERE source = 'facebook_marketplace'`).Scan(&status)
	if err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatalf("session status = %q, want ok", status)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/db/`
Expected: FAIL de compilação: `package scrapperbook/backend/internal/testdb is not in std`.

- [ ] **Step 3: Escrever a migration**

`backend/migrations/00001_init.sql`:

```sql
-- +goose Up
CREATE TABLE search_regions (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('city', 'point', 'state')),
    city        text,
    state       text,
    lat         double precision,
    lng         double precision,
    radius_km   integer,
    fb_location text,
    enabled     boolean NOT NULL DEFAULT true
);

CREATE TABLE search_definitions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    keyword     text NOT NULL,
    enabled     boolean NOT NULL DEFAULT true,
    priority    smallint NOT NULL DEFAULT 0,
    frequency   interval NOT NULL DEFAULT '1 hour',
    next_run_at timestamptz NOT NULL DEFAULT now(),
    filters     jsonb NOT NULL DEFAULT '{}',
    alert       jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX search_definitions_due ON search_definitions (next_run_at) WHERE enabled;

CREATE TABLE search_definition_regions (
    search_id       uuid NOT NULL REFERENCES search_definitions (id) ON DELETE CASCADE,
    region_id       text NOT NULL REFERENCES search_regions (id),
    baseline_done   boolean NOT NULL DEFAULT false,
    last_success_at timestamptz,
    PRIMARY KEY (search_id, region_id)
);

CREATE TABLE jobs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type          text NOT NULL CHECK (type IN ('search', 'enrich')),
    source        text NOT NULL,
    search_id     uuid REFERENCES search_definitions (id) ON DELETE CASCADE,
    region_id     text REFERENCES search_regions (id),
    payload       jsonb NOT NULL,
    status        text NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued', 'running', 'succeeded', 'dead')),
    priority      smallint NOT NULL DEFAULT 0,
    attempts      integer NOT NULL DEFAULT 0,
    max_attempts  integer NOT NULL DEFAULT 4,
    run_after     timestamptz NOT NULL,
    lease_until   timestamptz,
    worker_id     text,
    error_kind    text,
    error_message text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);
CREATE UNIQUE INDEX jobs_one_in_flight ON jobs (source, search_id, region_id)
    WHERE status IN ('queued', 'running');
CREATE INDEX jobs_claim ON jobs (status, source, priority DESC, run_after);
CREATE INDEX jobs_started ON jobs (source, started_at);

CREATE TABLE raw_captures (
    id              bigserial PRIMARY KEY,
    job_id          uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    source          text NOT NULL,
    external_id     text NOT NULL,
    crawler_version text NOT NULL,
    parser_version  text NOT NULL,
    payload         jsonb NOT NULL,
    captured_at     timestamptz NOT NULL
);
CREATE INDEX raw_captures_captured_at ON raw_captures (captured_at);

CREATE TABLE listings (
    id                 bigserial PRIMARY KEY,
    source             text NOT NULL,
    external_id        text NOT NULL,
    url                text NOT NULL,
    title              text NOT NULL,
    description        text,
    price_cents        bigint,
    currency           char(3),
    location_text      text,
    city               text,
    state              text,
    lat                double precision,
    lng                double precision,
    image_url          text,
    seller_external_id text,
    seller_name        text,
    status             text NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'pending', 'sold', 'unseen')),
    fingerprint        text NOT NULL,
    content_hash       text NOT NULL,
    first_seen_at      timestamptz NOT NULL,
    last_seen_at       timestamptz NOT NULL,
    last_changed_at    timestamptz NOT NULL,
    raw_data           jsonb NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);
CREATE INDEX listings_first_seen ON listings (first_seen_at);
CREATE INDEX listings_last_seen ON listings (last_seen_at);
CREATE INDEX listings_price ON listings (price_cents);
CREATE INDEX listings_location ON listings (state, city);
CREATE INDEX listings_seller ON listings (seller_external_id);
CREATE INDEX listings_fingerprint ON listings (fingerprint);

CREATE TABLE listing_snapshots (
    id          bigserial PRIMARY KEY,
    listing_id  bigint NOT NULL REFERENCES listings (id) ON DELETE CASCADE,
    price_cents bigint,
    title       text NOT NULL,
    description text,
    status      text NOT NULL,
    job_id      uuid REFERENCES jobs (id) ON DELETE SET NULL,
    captured_at timestamptz NOT NULL,
    raw_data    jsonb NOT NULL
);
CREATE INDEX listing_snapshots_listing ON listing_snapshots (listing_id, captured_at);

CREATE TABLE search_listings (
    search_id     uuid NOT NULL REFERENCES search_definitions (id) ON DELETE CASCADE,
    listing_id    bigint NOT NULL REFERENCES listings (id) ON DELETE CASCADE,
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    PRIMARY KEY (search_id, listing_id)
);

CREATE TABLE notifications (
    id              bigserial PRIMARY KEY,
    dedupe_key      text NOT NULL UNIQUE,
    kind            text NOT NULL CHECK (kind IN ('new', 'price_drop', 'system')),
    listing_id      bigint REFERENCES listings (id) ON DELETE CASCADE,
    search_id       uuid REFERENCES search_definitions (id) ON DELETE CASCADE,
    price_cents     bigint,
    old_price_cents bigint,
    message         text,
    channel         text NOT NULL DEFAULT 'discord',
    attempts        integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz
);
CREATE INDEX notifications_pending ON notifications (created_at) WHERE sent_at IS NULL;

CREATE TABLE workers (
    id                text PRIMARY KEY,
    hostname          text NOT NULL,
    status            text NOT NULL CHECK (status IN
                      ('starting', 'healthy', 'busy', 'idle', 'error', 'cooldown', 'stopped')),
    crawler_version   text NOT NULL,
    sources           text[] NOT NULL DEFAULT '{}',
    current_job_id    uuid,
    jobs_ok           integer NOT NULL DEFAULT 0,
    jobs_failed       integer NOT NULL DEFAULT 0,
    cooldown_until    timestamptz,
    registered_at     timestamptz NOT NULL,
    last_heartbeat_at timestamptz NOT NULL
);

CREATE TABLE source_sessions (
    source             text PRIMARY KEY,
    status             text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'needs_attention')),
    last_checkpoint_at timestamptz,
    cooldown_until     timestamptz,
    note               text,
    updated_at         timestamptz NOT NULL DEFAULT now()
);
INSERT INTO source_sessions (source) VALUES ('facebook_marketplace');

-- +goose Down
DROP TABLE source_sessions;
DROP TABLE workers;
DROP TABLE notifications;
DROP TABLE search_listings;
DROP TABLE listing_snapshots;
DROP TABLE listings;
DROP TABLE raw_captures;
DROP TABLE jobs;
DROP TABLE search_definition_regions;
DROP TABLE search_definitions;
DROP TABLE search_regions;
```

- [ ] **Step 4: Embutir as migrations**

`backend/migrations/embed.go`:

```go
// Package migrations embeds the goose SQL migrations.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 5: Escrever o pacote `db`**

`backend/internal/db/dbtx.go`:

```go
package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is satisfied by *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
```

`backend/internal/db/db.go`:

```go
// Package db opens the Postgres pool and applies migrations.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"scrapperbook/backend/migrations"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// Closing this *sql.DB does not close the pool.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(database.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: Escrever o helper de teste**

`backend/internal/testdb/testdb.go`:

```go
// Package testdb provides a disposable, migrated Postgres for tests.
// One container is shared per test binary; New truncates all data.
package testdb

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"scrapperbook/backend/internal/db"
)

var (
	once     sync.Once
	baseURL  string
	startErr error
)

func start(ctx context.Context) (string, error) {
	ctr, err := postgres.Run(ctx, "postgres:17",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		return "", err
	}
	return ctr.ConnectionString(ctx, "sslmode=disable")
}

// New returns a pool to an empty, fully migrated database.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	once.Do(func() { baseURL, startErr = start(ctx) })
	if startErr != nil {
		t.Fatalf("start postgres: %v", startErr)
	}
	pool, err := db.Connect(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE notifications, listing_snapshots, search_listings,
		raw_captures, listings, jobs, search_definition_regions, search_definitions,
		search_regions, workers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE source_sessions
		SET status = 'ok', cooldown_until = NULL, last_checkpoint_at = NULL, note = NULL`); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Seeded holds the IDs created by Seed.
type Seeded struct {
	SearchID string
	RegionID string
}

// Seed inserts region curitiba-pr and one due search ("Honda Civic EG",
// every 30 minutes, alerts on, max price R$ 30.000) linked to it.
func Seed(t *testing.T, pool *pgxpool.Pool) Seeded {
	t.Helper()
	ctx := context.Background()
	AddRegion(t, pool, "curitiba-pr", "curitiba")
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO search_definitions
		(name, keyword, frequency, next_run_at, filters, alert)
		VALUES ('Civic EG', 'Honda Civic EG', '30 minutes', '2000-01-01T00:00:00Z',
		        '{"max_price": 30000}',
		        '{"enabled": true, "on_new": true, "on_price_drop": true, "max_price": 30000}')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	LinkRegion(t, pool, id, "curitiba-pr")
	return Seeded{SearchID: id, RegionID: "curitiba-pr"}
}

// AddRegion inserts a city region. Pass fbLocation "" to leave it NULL.
func AddRegion(t *testing.T, pool *pgxpool.Pool, id, fbLocation string) {
	t.Helper()
	var loc *string
	if fbLocation != "" {
		loc = &fbLocation
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO search_regions
		(id, name, kind, city, state, lat, lng, radius_km, fb_location)
		VALUES ($1, $1, 'city', $1, 'PR', -25.43, -49.27, 60, $2)`, id, loc)
	if err != nil {
		t.Fatal(err)
	}
}

// LinkRegion attaches a region to a search.
func LinkRegion(t *testing.T, pool *pgxpool.Pool, searchID, regionID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO search_definition_regions (search_id, region_id) VALUES ($1, $2)`,
		searchID, regionID)
	if err != nil {
		t.Fatal(err)
	}
}

// InsertRunningJob inserts a running search job for the seeded pair and returns its ID.
func InsertRunningJob(t *testing.T, pool *pgxpool.Pool, s Seeded) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `INSERT INTO jobs
		(type, source, search_id, region_id, payload, status, attempts, run_after, started_at)
		VALUES ('search', 'facebook_marketplace', $1, $2, '{}', 'running', 1,
		        '2000-01-01T00:00:00Z', '2000-01-01T00:00:00Z')
		RETURNING id`, s.SearchID, s.RegionID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
```

- [ ] **Step 7: Baixar dependências**

Run:
```bash
cd backend
go get github.com/jackc/pgx/v5@latest github.com/pressly/goose/v3@latest \
  github.com/testcontainers/testcontainers-go@latest \
  github.com/testcontainers/testcontainers-go/modules/postgres@latest
go mod tidy
```
Expected: `go.mod` lista pgx v5, goose v3 e testcontainers.

- [ ] **Step 8: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/db/ -v`
Expected: `--- PASS: TestMigrateCreatesSchema`

- [ ] **Step 9: Commit**

```bash
git add backend/
git commit -m "feat(backend): add schema migration, db package and test database helper"
```

---

### Task 3: Modelo canônico de anúncio

**Files:**
- Create: `backend/internal/listing/listing.go`
- Test: `backend/internal/listing/listing_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/listing/listing_test.go`:

```go
package listing_test

import (
	"testing"

	"scrapperbook/backend/internal/listing"
)

func price(v int64) *int64 { return &v }

func TestNormalizeText(t *testing.T) {
	cases := map[string]string{
		"  Honda  CIVIC – EG! São ": "honda civic eg sao",
		"Câmbio Manual/1993":        "cambio manual 1993",
		"":                          "",
	}
	for in, want := range cases {
		if got := listing.NormalizeText(in); got != want {
			t.Errorf("NormalizeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFingerprintIgnoresCaseAccentsAndPunctuation(t *testing.T) {
	a := listing.Canonical{Title: "Honda Civic EG São Paulo", PriceCents: price(2350000),
		SellerExternalID: "555", City: "Curitiba", State: "PR"}
	b := listing.Canonical{Title: "honda civic eg sao paulo!", PriceCents: price(2350000),
		SellerExternalID: "555", City: "curitiba", State: "pr"}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("fingerprints differ for equivalent listings")
	}
}

func TestContentHashChangesWithPrice(t *testing.T) {
	a := listing.Canonical{Title: "Civic", PriceCents: price(2350000), Status: listing.StatusActive}
	b := a
	b.PriceCents = price(2200000)
	if a.ContentHash() == b.ContentHash() {
		t.Fatal("content hash did not change with price")
	}
}

func TestContentHashDistinguishesMissingPriceFromZero(t *testing.T) {
	a := listing.Canonical{Title: "Civic", Status: listing.StatusActive}
	b := a
	b.PriceCents = price(0)
	if a.ContentHash() == b.ContentHash() {
		t.Fatal("nil price and zero price produce the same hash")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/listing/`
Expected: FAIL de compilação (pacote `listing` não existe).

- [ ] **Step 3: Implementar**

`backend/internal/listing/listing.go`:

```go
// Package listing defines the source-independent listing model.
package listing

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

type Status string

const (
	StatusActive  Status = "active"
	StatusPending Status = "pending"
	StatusSold    Status = "sold"
	StatusUnseen  Status = "unseen"
)

// Canonical is the shape every source normalizer produces.
type Canonical struct {
	Source           string
	ExternalID       string
	URL              string
	Title            string
	Description      string
	PriceCents       *int64
	Currency         string
	LocationText     string
	City             string
	State            string
	Lat              *float64
	Lng              *float64
	ImageURL         string
	SellerExternalID string
	SellerName       string
	Status           Status
}

// ContentHash changes whenever a field worth a snapshot changes.
func (c Canonical) ContentHash() string {
	return hashParts(c.Title, priceKey(c.PriceCents), string(c.Status), c.Description)
}

// Fingerprint identifies the same item across external IDs (reposts, other sources).
func (c Canonical) Fingerprint() string {
	return hashParts(
		NormalizeText(c.Title),
		priceKey(c.PriceCents),
		NormalizeText(c.SellerExternalID),
		NormalizeText(c.City),
		NormalizeText(c.State),
	)
}

// NormalizeText lowercases, strips accents and collapses everything that is
// not a letter or digit into single spaces.
func NormalizeText(s string) string {
	// transform.Chain is stateful, so build one per call.
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err != nil {
		folded = s
	}
	var b strings.Builder
	pendingSpace := false
	for _, r := range strings.ToLower(folded) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
			continue
		}
		pendingSpace = true
	}
	return b.String()
}

func priceKey(p *int64) string {
	if p == nil {
		return "none"
	}
	return strconv.FormatInt(*p, 10)
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0x1f})
	}
	return hex.EncodeToString(h.Sum(nil))
}
```

- [ ] **Step 4: Baixar dependência e rodar**

Run: `cd backend && go get golang.org/x/text@latest && go test ./internal/listing/ -v`
Expected: 4 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/
git commit -m "feat(backend): add canonical listing model with content hash and fingerprint"
```

---

### Task 4: Normalizer do Facebook

O formato abaixo segue o nó `listing` documentado por projetos open source (`marketplace_search.feed_units.edges[].node.listing`). A fixture é **sintética**. O plano 2 substitui por uma captura real sanitizada e ajusta o mapeamento se necessário.

**Files:**
- Create: `backend/internal/source/facebook/normalizer.go`
- Create: `backend/internal/source/facebook/testdata/listing_node.json`
- Test: `backend/internal/source/facebook/normalizer_test.go`

- [ ] **Step 1: Criar a fixture**

`backend/internal/source/facebook/testdata/listing_node.json`:

```json
{
  "id": "1029384756",
  "marketplace_listing_title": "Honda Civic EG 1993 hatch",
  "listing_price": {"amount": "23500.00", "formatted_amount": "R$ 23.500", "currency": "BRL"},
  "primary_listing_photo": {"image": {"uri": "https://scontent.example/civic.jpg"}},
  "location": {
    "reverse_geocode": {
      "city": "Curitiba",
      "state": "PR",
      "city_page": {"display_name": "Curitiba, PR"}
    }
  },
  "marketplace_listing_seller": {"id": "555", "name": "Vendedor Teste"},
  "is_sold": false,
  "is_pending": false
}
```

- [ ] **Step 2: Escrever os testes que falham**

`backend/internal/source/facebook/normalizer_test.go`:

```go
package facebook_test

import (
	"encoding/json"
	"os"
	"testing"

	"scrapperbook/backend/internal/listing"
	"scrapperbook/backend/internal/source/facebook"
)

func fixture(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/listing_node.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNormalizeFullNode(t *testing.T) {
	c, err := facebook.Normalizer{}.Normalize("1029384756", fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "facebook_marketplace" {
		t.Errorf("source = %q", c.Source)
	}
	if c.URL != "https://www.facebook.com/marketplace/item/1029384756/" {
		t.Errorf("url = %q", c.URL)
	}
	if c.Title != "Honda Civic EG 1993 hatch" {
		t.Errorf("title = %q", c.Title)
	}
	if c.PriceCents == nil || *c.PriceCents != 2350000 {
		t.Errorf("price = %v", c.PriceCents)
	}
	if c.Currency != "BRL" || c.City != "Curitiba" || c.State != "PR" {
		t.Errorf("currency/city/state = %q/%q/%q", c.Currency, c.City, c.State)
	}
	if c.LocationText != "Curitiba, PR" {
		t.Errorf("location_text = %q", c.LocationText)
	}
	if c.ImageURL != "https://scontent.example/civic.jpg" {
		t.Errorf("image = %q", c.ImageURL)
	}
	if c.SellerExternalID != "555" || c.SellerName != "Vendedor Teste" {
		t.Errorf("seller = %q/%q", c.SellerExternalID, c.SellerName)
	}
	if c.Status != listing.StatusActive {
		t.Errorf("status = %q", c.Status)
	}
}

func TestNormalizeStatusAndNumericPrice(t *testing.T) {
	raw := json.RawMessage(`{"id":"1","marketplace_listing_title":"Civic",
		"listing_price":{"amount":19900},"is_sold":true}`)
	c, err := facebook.Normalizer{}.Normalize("1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != listing.StatusSold {
		t.Errorf("status = %q, want sold", c.Status)
	}
	if c.PriceCents == nil || *c.PriceCents != 1990000 {
		t.Errorf("price = %v, want 1990000", c.PriceCents)
	}
	if c.Currency != "BRL" {
		t.Errorf("currency default = %q, want BRL", c.Currency)
	}
}

func TestNormalizePendingWithoutPrice(t *testing.T) {
	raw := json.RawMessage(`{"id":"2","marketplace_listing_title":"Civic","is_pending":true}`)
	c, err := facebook.Normalizer{}.Normalize("2", raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != listing.StatusPending {
		t.Errorf("status = %q, want pending", c.Status)
	}
	if c.PriceCents != nil {
		t.Errorf("price = %v, want nil", *c.PriceCents)
	}
}

func TestNormalizeRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		id  string
		raw string
	}{
		"id mismatch":    {"9", `{"id":"1","marketplace_listing_title":"Civic"}`},
		"missing title":  {"1", `{"id":"1"}`},
		"bad json":       {"1", `{`},
		"exponent price": {"1", `{"id":"1","marketplace_listing_title":"C","listing_price":{"amount":"2e4"}}`},
	}
	for name, tc := range cases {
		if _, err := (facebook.Normalizer{}).Normalize(tc.id, json.RawMessage(tc.raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
```

- [ ] **Step 3: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/source/facebook/`
Expected: FAIL de compilação (pacote `facebook` não existe).

- [ ] **Step 4: Implementar**

`backend/internal/source/facebook/normalizer.go`:

```go
// Package facebook maps Facebook Marketplace raw listing nodes to the canonical model.
package facebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"scrapperbook/backend/internal/listing"
)

const (
	Source        = "facebook_marketplace"
	itemURLPrefix = "https://www.facebook.com/marketplace/item/"
	// Marketplace searches here are Brazil-only; the node rarely carries a currency.
	defaultCurrency = "BRL"
)

type node struct {
	ID    string `json:"id"`
	Title string `json:"marketplace_listing_title"`
	Price *struct {
		Amount   json.Number `json:"amount"`
		Currency string      `json:"currency"`
	} `json:"listing_price"`
	Photo *struct {
		Image struct {
			URI string `json:"uri"`
		} `json:"image"`
	} `json:"primary_listing_photo"`
	Location *struct {
		ReverseGeocode struct {
			City     string `json:"city"`
			State    string `json:"state"`
			CityPage struct {
				DisplayName string `json:"display_name"`
			} `json:"city_page"`
		} `json:"reverse_geocode"`
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	} `json:"location"`
	Seller *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"marketplace_listing_seller"`
	Description *struct {
		Text string `json:"text"`
	} `json:"redacted_description"`
	IsSold    bool `json:"is_sold"`
	IsPending bool `json:"is_pending"`
}

type Normalizer struct{}

func (Normalizer) Source() string { return Source }

func (Normalizer) Normalize(externalID string, raw json.RawMessage) (listing.Canonical, error) {
	var n node
	if err := json.Unmarshal(raw, &n); err != nil {
		return listing.Canonical{}, fmt.Errorf("decode facebook node: %w", err)
	}
	if externalID == "" {
		return listing.Canonical{}, errors.New("empty external_id")
	}
	if n.ID != "" && n.ID != externalID {
		return listing.Canonical{}, fmt.Errorf("external_id %q does not match node id %q", externalID, n.ID)
	}
	title := strings.TrimSpace(n.Title)
	if title == "" {
		return listing.Canonical{}, errors.New("missing title")
	}

	c := listing.Canonical{
		Source:     Source,
		ExternalID: externalID,
		URL:        itemURLPrefix + externalID + "/",
		Title:      title,
		Currency:   defaultCurrency,
		Status:     listing.StatusActive,
	}
	if n.Price != nil && n.Price.Amount != "" {
		cents, err := amountToCents(n.Price.Amount.String())
		if err != nil {
			return listing.Canonical{}, err
		}
		c.PriceCents = &cents
		if n.Price.Currency != "" {
			c.Currency = n.Price.Currency
		}
	}
	if n.Photo != nil {
		c.ImageURL = n.Photo.Image.URI
	}
	if n.Location != nil {
		g := n.Location.ReverseGeocode
		c.City, c.State = g.City, g.State
		c.LocationText = g.CityPage.DisplayName
		if c.LocationText == "" && c.City != "" {
			c.LocationText = strings.TrimSuffix(c.City+", "+c.State, ", ")
		}
		c.Lat, c.Lng = n.Location.Latitude, n.Location.Longitude
	}
	if n.Seller != nil {
		c.SellerExternalID, c.SellerName = n.Seller.ID, n.Seller.Name
	}
	if n.Description != nil {
		c.Description = n.Description.Text
	}
	switch {
	case n.IsSold:
		c.Status = listing.StatusSold
	case n.IsPending:
		c.Status = listing.StatusPending
	}
	return c, nil
}

// amountToCents parses "23500", "23500.5" or "23500.00" without floats.
func amountToCents(s string) (int64, error) {
	if strings.ContainsAny(s, "eE-") {
		return 0, fmt.Errorf("unsupported amount %q", s)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", s, err)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", s, err)
	}
	return w*100 + f, nil
}
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/source/facebook/ -v`
Expected: 4 testes PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/source/
git commit -m "feat(backend): add facebook marketplace normalizer"
```

---

### Task 5: Regra de alerta

**Files:**
- Create: `backend/internal/search/alert.go`
- Test: `backend/internal/search/alert_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/search/alert_test.go`:

```go
package search_test

import (
	"testing"

	"scrapperbook/backend/internal/listing"
	"scrapperbook/backend/internal/search"
)

func cents(v int64) *int64 { return &v }

func TestParseAlertRule(t *testing.T) {
	r, err := search.ParseAlertRule([]byte(
		`{"enabled":true,"max_price":30000,"states":["PR","SC"],"on_new":true,"min_drop_pct":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Enabled || !r.OnNew || r.OnPriceDrop || *r.MaxPrice != 30000 || len(r.States) != 2 || r.MinDropPct != 5 {
		t.Fatalf("unexpected rule %+v", r)
	}
	empty, err := search.ParseAlertRule(nil)
	if err != nil || empty.Enabled {
		t.Fatalf("empty rule = %+v, %v", empty, err)
	}
}

func TestMatches(t *testing.T) {
	max := int64(30000)
	rule := search.AlertRule{Enabled: true, MaxPrice: &max, States: []string{"PR"}}
	cases := []struct {
		name string
		c    listing.Canonical
		want bool
	}{
		{"under max in PR", listing.Canonical{PriceCents: cents(2500000), State: "PR"}, true},
		{"exactly max", listing.Canonical{PriceCents: cents(3000000), State: "pr"}, true},
		{"over max", listing.Canonical{PriceCents: cents(3000001), State: "PR"}, false},
		{"no price with max set", listing.Canonical{State: "PR"}, false},
		{"other state", listing.Canonical{PriceCents: cents(100), State: "SP"}, false},
	}
	for _, tc := range cases {
		if got := rule.Matches(tc.c); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
	disabled := search.AlertRule{}
	if disabled.Matches(listing.Canonical{}) {
		t.Error("disabled rule matched")
	}
}

func TestWantsNewAndPriceDrop(t *testing.T) {
	c := listing.Canonical{PriceCents: cents(2000000)}
	r := search.AlertRule{Enabled: true, OnNew: true, OnPriceDrop: true, MinDropPct: 10}
	if !r.WantsNew(c) {
		t.Error("WantsNew = false")
	}
	if !r.WantsPriceDrop(2300000, 2000000, c) {
		t.Error("13% drop should alert with min 10%")
	}
	if r.WantsPriceDrop(2100000, 2000000, c) {
		t.Error("4.7% drop should not alert with min 10%")
	}
	if r.WantsPriceDrop(2000000, 2100000, c) {
		t.Error("price increase should not alert")
	}
	r.OnNew = false
	if r.WantsNew(c) {
		t.Error("WantsNew with on_new=false")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/search/`
Expected: FAIL de compilação (pacote `search` não existe).

- [ ] **Step 3: Implementar**

`backend/internal/search/alert.go`:

```go
// Package search holds search definition rules.
package search

import (
	"encoding/json"
	"fmt"
	"strings"

	"scrapperbook/backend/internal/listing"
)

// AlertRule is stored as JSON in search_definitions.alert.
type AlertRule struct {
	Enabled     bool     `json:"enabled"`
	MaxPrice    *int64   `json:"max_price"` // whole currency units (reais), not cents
	States      []string `json:"states"`
	OnNew       bool     `json:"on_new"`
	OnPriceDrop bool     `json:"on_price_drop"`
	MinDropPct  float64  `json:"min_drop_pct"`
}

func ParseAlertRule(raw []byte) (AlertRule, error) {
	var r AlertRule
	if len(raw) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return AlertRule{}, fmt.Errorf("parse alert rule: %w", err)
	}
	return r, nil
}

// Matches reports whether the listing passes the rule's filters.
func (r AlertRule) Matches(c listing.Canonical) bool {
	if !r.Enabled {
		return false
	}
	if r.MaxPrice != nil && (c.PriceCents == nil || *c.PriceCents > *r.MaxPrice*100) {
		return false
	}
	if len(r.States) > 0 {
		for _, s := range r.States {
			if strings.EqualFold(s, c.State) {
				return true
			}
		}
		return false
	}
	return true
}

func (r AlertRule) WantsNew(c listing.Canonical) bool {
	return r.OnNew && r.Matches(c)
}

func (r AlertRule) WantsPriceDrop(oldCents, newCents int64, c listing.Canonical) bool {
	if !r.OnPriceDrop || oldCents <= 0 || newCents >= oldCents {
		return false
	}
	dropPct := float64(oldCents-newCents) / float64(oldCents) * 100
	return dropPct >= r.MinDropPct && r.Matches(c)
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/search/ -v`
Expected: 3 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/search/
git commit -m "feat(backend): add alert rule matching"
```

---

### Task 6: Ingest (dedup, snapshots, baseline, outbox)

**Files:**
- Create: `backend/internal/ingest/registry.go`, `backend/internal/ingest/ingest.go`
- Test: `backend/internal/ingest/ingest_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/ingest/ingest_test.go`:

```go
package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/source/facebook"
	"scrapperbook/backend/internal/testdb"
)

var seenAt = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

func node(id, title, amount string) ingest.Item {
	raw := fmt.Sprintf(`{"id":%q,"marketplace_listing_title":%q,
		"listing_price":{"amount":%q},
		"location":{"reverse_geocode":{"city":"Curitiba","state":"PR"}}}`, id, title, amount)
	return ingest.Item{ExternalID: id, Raw: json.RawMessage(raw)}
}

type env struct {
	pool  *pgxpool.Pool
	svc   *ingest.Service
	seed  testdb.Seeded
	jobID string
}

func setup(t *testing.T) env {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	svc := ingest.New(ingest.NewRegistry(facebook.Normalizer{}),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return env{pool: pool, svc: svc, seed: seed, jobID: testdb.InsertRunningJob(t, pool, seed)}
}

func (e env) run(t *testing.T, at time.Time, items ...ingest.Item) ingest.Stats {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	st, err := e.svc.Ingest(ctx, tx, ingest.Batch{
		JobID: e.jobID, Source: facebook.Source,
		SearchID: e.seed.SearchID, RegionID: e.seed.RegionID,
		CrawlerVersion: "test", ParserVersion: "test", SeenAt: at, Items: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func (e env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBaselineStoresListingsWithoutAlerts(t *testing.T) {
	e := setup(t)
	st := e.run(t, seenAt, node("1", "Civic EG", "23500"), node("2", "Civic EG5", "21000"))
	if st.New != 2 || st.Alerts != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM listings`); n != 2 {
		t.Fatalf("listings = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM listing_snapshots`); n != 2 {
		t.Fatalf("snapshots = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications`); n != 0 {
		t.Fatalf("notifications = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM raw_captures`); n != 2 {
		t.Fatalf("raw_captures = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM search_definition_regions WHERE baseline_done`); n != 1 {
		t.Fatal("baseline_done not set")
	}
}

func TestIngestIsIdempotent(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Civic EG", "23500"))
	if st.New != 0 || st.Seen != 1 || st.Changed != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM listings`); n != 1 {
		t.Fatalf("listings = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM listing_snapshots`); n != 1 {
		t.Fatalf("snapshots = %d", n)
	}
	var lastSeen time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT last_seen_at FROM listings`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if !lastSeen.Equal(seenAt.Add(time.Hour)) {
		t.Fatalf("last_seen_at = %v", lastSeen)
	}
}

func TestNewListingAfterBaselineAlertsOnce(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Civic EG", "23500"), node("2", "Civic EG5", "21000"))
	if st.New != 1 || st.Alerts != 1 {
		t.Fatalf("stats = %+v", st)
	}
	again := e.run(t, seenAt.Add(2*time.Hour), node("2", "Civic EG5", "21000"))
	if again.Alerts != 0 {
		t.Fatalf("second sighting alerted: %+v", again)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications WHERE kind = 'new'`); n != 1 {
		t.Fatalf("new notifications = %d", n)
	}
}

func TestPriceDropCreatesSnapshotAndAlert(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("1", "Civic EG", "21000"))
	if st.Changed != 1 || st.Alerts != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM listing_snapshots`); n != 2 {
		t.Fatalf("snapshots = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications
		WHERE kind = 'price_drop' AND price_cents = 2100000 AND old_price_cents = 2350000`); n != 1 {
		t.Fatal("price_drop notification missing")
	}
}

func TestAlertRespectsMaxPrice(t *testing.T) {
	e := setup(t)
	e.run(t, seenAt, node("1", "Civic EG", "23500"))
	st := e.run(t, seenAt.Add(time.Hour), node("2", "Civic Si", "45000"))
	if st.New != 1 || st.Alerts != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestInvalidItemsAreSkipped(t *testing.T) {
	e := setup(t)
	bad := ingest.Item{ExternalID: "3", Raw: json.RawMessage(`{"id":"3"}`)}
	notJSON := ingest.Item{ExternalID: "4", Raw: json.RawMessage(`{`)}
	st := e.run(t, seenAt, node("1", "Civic EG", "23500"), bad, notJSON)
	if st.New != 1 || st.Invalid != 2 {
		t.Fatalf("stats = %+v", st)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/ingest/`
Expected: FAIL de compilação (pacote `ingest` não existe).

- [ ] **Step 3: Implementar o registry**

`backend/internal/ingest/registry.go`:

```go
package ingest

import (
	"encoding/json"

	"scrapperbook/backend/internal/listing"
)

// Normalizer maps one raw item from a source to the canonical model.
type Normalizer interface {
	Source() string
	Normalize(externalID string, raw json.RawMessage) (listing.Canonical, error)
}

// Registry maps source name to normalizer.
type Registry map[string]Normalizer

func NewRegistry(ns ...Normalizer) Registry {
	r := Registry{}
	for _, n := range ns {
		r[n.Source()] = n
	}
	return r
}
```

- [ ] **Step 4: Implementar o serviço**

`backend/internal/ingest/ingest.go`:

```go
// Package ingest turns raw crawler items into deduplicated listings,
// snapshots and pending notifications.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"scrapperbook/backend/internal/listing"
	"scrapperbook/backend/internal/search"
)

var ErrUnknownSource = errors.New("unknown source")

type Item struct {
	ExternalID string          `json:"external_id"`
	Raw        json.RawMessage `json:"raw"`
}

type Batch struct {
	JobID          string
	Source         string
	SearchID       string
	RegionID       string
	CrawlerVersion string
	ParserVersion  string
	SeenAt         time.Time
	Items          []Item
}

type Stats struct {
	New     int `json:"new"`
	Changed int `json:"changed"`
	Seen    int `json:"seen"`
	Invalid int `json:"invalid"`
	Alerts  int `json:"alerts"`
}

type Service struct {
	registry Registry
	log      *slog.Logger
}

func New(registry Registry, log *slog.Logger) *Service {
	return &Service{registry: registry, log: log}
}

// Ingest processes a batch inside the caller's transaction.
func (s *Service) Ingest(ctx context.Context, tx pgx.Tx, b Batch) (Stats, error) {
	norm, ok := s.registry[b.Source]
	if !ok {
		return Stats{}, fmt.Errorf("%w: %s", ErrUnknownSource, b.Source)
	}
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
	rule, err := search.ParseAlertRule(alertRaw)
	if err != nil {
		return Stats{}, err
	}

	var st Stats
	for _, it := range b.Items {
		if !json.Valid(it.Raw) {
			st.Invalid++
			s.log.Warn("invalid raw json", "job_id", b.JobID, "external_id", it.ExternalID)
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO raw_captures
			(job_id, source, external_id, crawler_version, parser_version, payload, captured_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			b.JobID, b.Source, it.ExternalID, b.CrawlerVersion, b.ParserVersion, it.Raw, b.SeenAt); err != nil {
			return Stats{}, fmt.Errorf("insert raw capture: %w", err)
		}
		c, err := norm.Normalize(it.ExternalID, it.Raw)
		if err != nil {
			st.Invalid++
			s.log.Warn("normalize failed", "job_id", b.JobID, "external_id", it.ExternalID, "err", err)
			continue
		}
		res, err := upsertListing(ctx, tx, c, it.Raw, b)
		if err != nil {
			return Stats{}, err
		}
		switch res.kind {
		case kindNew:
			st.New++
		case kindChanged:
			st.Changed++
		case kindSeen:
			st.Seen++
		}
		newToSearch, err := touchSearchListing(ctx, tx, b.SearchID, res.listingID, b.SeenAt)
		if err != nil {
			return Stats{}, err
		}
		if !baselineDone {
			continue
		}
		if newToSearch && rule.WantsNew(c) {
			added, err := enqueueNotification(ctx, tx, notification{
				key:  fmt.Sprintf("new:%s:%d", b.SearchID, res.listingID),
				kind: "new", listingID: res.listingID, searchID: b.SearchID, price: c.PriceCents,
			})
			if err != nil {
				return Stats{}, err
			}
			if added {
				st.Alerts++
			}
		}
		if res.oldPrice != nil && c.PriceCents != nil && rule.WantsPriceDrop(*res.oldPrice, *c.PriceCents, c) {
			added, err := enqueueNotification(ctx, tx, notification{
				key:  fmt.Sprintf("price_drop:%s:%d:%d", b.SearchID, res.listingID, *c.PriceCents),
				kind: "price_drop", listingID: res.listingID, searchID: b.SearchID,
				price: c.PriceCents, oldPrice: res.oldPrice,
			})
			if err != nil {
				return Stats{}, err
			}
			if added {
				st.Alerts++
			}
		}
	}

	if !baselineDone {
		if _, err := tx.Exec(ctx, `UPDATE search_definition_regions SET baseline_done = true
			WHERE search_id = $1 AND region_id = $2`, b.SearchID, b.RegionID); err != nil {
			return Stats{}, fmt.Errorf("mark baseline: %w", err)
		}
	}
	return st, nil
}

type upsertKind int

const (
	kindNew upsertKind = iota
	kindChanged
	kindSeen
)

type upsertResult struct {
	listingID int64
	kind      upsertKind
	oldPrice  *int64 // set only when kind == kindChanged
}

func upsertListing(ctx context.Context, tx pgx.Tx, c listing.Canonical, raw json.RawMessage, b Batch) (upsertResult, error) {
	hash := c.ContentHash()
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO listings
		(source, external_id, url, title, description, price_cents, currency,
		 location_text, city, state, lat, lng, image_url, seller_external_id, seller_name,
		 status, fingerprint, content_hash, first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
		        $16, $17, $18, $19, $19, $19, $20)
		ON CONFLICT (source, external_id) DO NOTHING
		RETURNING id`,
		c.Source, c.ExternalID, c.URL, c.Title, nullable(c.Description), c.PriceCents, nullable(c.Currency),
		nullable(c.LocationText), nullable(c.City), nullable(c.State), c.Lat, c.Lng, nullable(c.ImageURL),
		nullable(c.SellerExternalID), nullable(c.SellerName),
		string(c.Status), c.Fingerprint(), hash, b.SeenAt, raw).Scan(&id)
	if err == nil {
		if err := insertSnapshot(ctx, tx, id, c, raw, b); err != nil {
			return upsertResult{}, err
		}
		return upsertResult{listingID: id, kind: kindNew}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return upsertResult{}, fmt.Errorf("insert listing: %w", err)
	}

	var oldHash string
	var oldPrice *int64
	if err := tx.QueryRow(ctx, `SELECT id, content_hash, price_cents FROM listings
		WHERE source = $1 AND external_id = $2 FOR UPDATE`,
		c.Source, c.ExternalID).Scan(&id, &oldHash, &oldPrice); err != nil {
		return upsertResult{}, fmt.Errorf("load listing: %w", err)
	}

	if oldHash == hash {
		// Status is part of the hash, so this only revives listings marked 'unseen'.
		if _, err := tx.Exec(ctx, `UPDATE listings
			SET last_seen_at = GREATEST(last_seen_at, $2), status = $3, updated_at = now()
			WHERE id = $1`, id, b.SeenAt, string(c.Status)); err != nil {
			return upsertResult{}, fmt.Errorf("touch listing: %w", err)
		}
		return upsertResult{listingID: id, kind: kindSeen}, nil
	}

	if _, err := tx.Exec(ctx, `UPDATE listings SET
		url = $2, title = $3, description = $4, price_cents = $5, currency = $6,
		location_text = $7, city = $8, state = $9, lat = $10, lng = $11, image_url = $12,
		seller_external_id = $13, seller_name = $14, status = $15, fingerprint = $16,
		content_hash = $17, last_seen_at = GREATEST(last_seen_at, $18), last_changed_at = $18,
		raw_data = $19, updated_at = now()
		WHERE id = $1`,
		id, c.URL, c.Title, nullable(c.Description), c.PriceCents, nullable(c.Currency),
		nullable(c.LocationText), nullable(c.City), nullable(c.State), c.Lat, c.Lng, nullable(c.ImageURL),
		nullable(c.SellerExternalID), nullable(c.SellerName), string(c.Status), c.Fingerprint(),
		hash, b.SeenAt, raw); err != nil {
		return upsertResult{}, fmt.Errorf("update listing: %w", err)
	}
	if err := insertSnapshot(ctx, tx, id, c, raw, b); err != nil {
		return upsertResult{}, err
	}
	return upsertResult{listingID: id, kind: kindChanged, oldPrice: oldPrice}, nil
}

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

// touchSearchListing records the sighting and reports whether it is the
// first time this search has seen the listing.
func touchSearchListing(ctx context.Context, tx pgx.Tx, searchID string, listingID int64, at time.Time) (bool, error) {
	var inserted bool
	err := tx.QueryRow(ctx, `INSERT INTO search_listings (search_id, listing_id, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (search_id, listing_id)
		DO UPDATE SET last_seen_at = GREATEST(search_listings.last_seen_at, EXCLUDED.last_seen_at)
		RETURNING (xmax = 0)`, searchID, listingID, at).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("upsert search listing: %w", err)
	}
	return inserted, nil
}

type notification struct {
	key       string
	kind      string
	listingID int64
	searchID  string
	price     *int64
	oldPrice  *int64
}

func enqueueNotification(ctx context.Context, tx pgx.Tx, n notification) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO notifications
		(dedupe_key, kind, listing_id, search_id, price_cents, old_price_cents)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		n.key, n.kind, n.listingID, n.searchID, n.price, n.oldPrice)
	if err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/ingest/ -v`
Expected: 6 testes PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/ingest/
git commit -m "feat(backend): ingest raw items with dedup, snapshots, baseline and alert outbox"
```

---

### Task 7: Fila de jobs — Enqueue e Claim

Regras do claim (spec §6):
- quiet hours bloqueiam;
- a sessão da fonte precisa estar `ok` e fora de cooldown;
- no máximo `MaxPerHour` jobs iniciados na última hora;
- intervalo mínimo `MinGap` desde o último início.

Um `pg_advisory_xact_lock` serializa claims para que essas regras valham mesmo com claims concorrentes.

**Files:**
- Create: `backend/internal/jobs/queue.go`
- Test: `backend/internal/jobs/queue_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/jobs/queue_test.go`:

```go
package jobs_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/testdb"
)

var (
	sources = []string{"facebook_marketplace"}
	types   = []string{"search"}
)

func saoPaulo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// clock is a controllable time source shared with the queue.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func openLimits() jobs.Limits {
	return jobs.Limits{MaxPerHour: 100, MinGap: 0, Lease: 10 * time.Minute}
}

func newQueue(t *testing.T, pool *pgxpool.Pool, limits jobs.Limits) (*jobs.Queue, *clock) {
	t.Helper()
	c := &clock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, saoPaulo(t))}
	q := jobs.New(pool, limits)
	q.Clock = c.Now
	return q, c
}

func enqueue(t *testing.T, q *jobs.Queue, pool *pgxpool.Pool, searchID, regionID string, priority int) {
	t.Helper()
	ok, err := q.Enqueue(context.Background(), pool, jobs.NewJob{
		Type: "search", Source: "facebook_marketplace", SearchID: searchID, RegionID: regionID,
		Priority: priority, Payload: json.RawMessage(`{"search":{"keyword":"civic"}}`),
	})
	if err != nil || !ok {
		t.Fatalf("enqueue = %v, %v", ok, err)
	}
}

func claim(t *testing.T, q *jobs.Queue) *jobs.Job {
	t.Helper()
	j, err := q.Claim(context.Background(), "w1", sources, types)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestEnqueueSkipsDuplicateInFlight(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ok, err := q.Enqueue(context.Background(), pool, jobs.NewJob{
		Type: "search", Source: "facebook_marketplace", SearchID: s.SearchID, RegionID: s.RegionID,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil || ok {
		t.Fatalf("duplicate enqueue = %v, %v; want false, nil", ok, err)
	}
}

func TestClaimReturnsJobAndMarksRunning(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)

	j := claim(t, q)
	if j == nil {
		t.Fatal("no job claimed")
	}
	if j.Attempt != 1 || j.Source != "facebook_marketplace" || *j.SearchID != s.SearchID || *j.RegionID != s.RegionID {
		t.Fatalf("job = %+v", j)
	}
	if !j.StartedAt.Equal(c.now) || j.StopBefore != nil {
		t.Fatalf("started_at = %v, stop_before = %v", j.StartedAt, j.StopBefore)
	}
	var status, worker string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, worker_id FROM jobs WHERE id = $1`, j.ID).Scan(&status, &worker); err != nil {
		t.Fatal(err)
	}
	if status != "running" || worker != "w1" {
		t.Fatalf("status/worker = %s/%s", status, worker)
	}
	if again := claim(t, q); again != nil {
		t.Fatal("claimed a running job twice")
	}
}

func TestClaimPrefersHigherPriority(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, s.SearchID, "londrina-pr")
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	enqueue(t, q, pool, s.SearchID, "londrina-pr", 5)

	if j := claim(t, q); j == nil || *j.RegionID != "londrina-pr" {
		t.Fatalf("claimed %+v, want londrina-pr", j)
	}
}

func TestClaimGates(t *testing.T) {
	cases := []struct {
		name   string
		limits func(jobs.Limits) jobs.Limits
		prep   func(t *testing.T, pool *pgxpool.Pool, c *clock)
	}{
		{"quiet hours", func(l jobs.Limits) jobs.Limits {
			l.Quiet = jobs.QuietHours{Enabled: true, StartHour: 11, EndHour: 13, Location: saoPaulo(t)}
			return l
		}, nil},
		{"hourly budget", func(l jobs.Limits) jobs.Limits { l.MaxPerHour = 1; return l },
			func(t *testing.T, pool *pgxpool.Pool, c *clock) { recentStart(t, pool, c.now.Add(-30*time.Minute)) }},
		{"min gap", func(l jobs.Limits) jobs.Limits { l.MinGap = 4 * time.Minute; return l },
			func(t *testing.T, pool *pgxpool.Pool, c *clock) { recentStart(t, pool, c.now.Add(-time.Minute)) }},
		{"session needs attention", nil, func(t *testing.T, pool *pgxpool.Pool, c *clock) {
			exec(t, pool, `UPDATE source_sessions SET status = 'needs_attention'`)
		}},
		{"source cooldown", nil, func(t *testing.T, pool *pgxpool.Pool, c *clock) {
			exec(t, pool, `UPDATE source_sessions SET cooldown_until = $1`, c.now.Add(time.Minute))
		}},
		{"run_after in future", nil, func(t *testing.T, pool *pgxpool.Pool, c *clock) {
			exec(t, pool, `UPDATE jobs SET run_after = $1 WHERE status = 'queued'`, c.now.Add(time.Minute))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testdb.New(t)
			s := testdb.Seed(t, pool)
			limits := openLimits()
			if tc.limits != nil {
				limits = tc.limits(limits)
			}
			q, c := newQueue(t, pool, limits)
			enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
			if tc.prep != nil {
				tc.prep(t, pool, c)
			}
			if j := claim(t, q); j != nil {
				t.Fatalf("claimed %s despite gate", j.ID)
			}
		})
	}
}

func TestConcurrentClaimsNeverShareJob(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, s.SearchID, "londrina-pr")
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	enqueue(t, q, pool, s.SearchID, "londrina-pr", 0)

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := q.Claim(context.Background(), "w", sources, types)
			if err != nil {
				t.Error(err)
				return
			}
			if j != nil {
				mu.Lock()
				seen[j.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 2 {
		t.Fatalf("claimed %d distinct jobs, want 2", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %s claimed %d times", id, n)
		}
	}
}

// recentStart inserts a finished job that started at the given time.
func recentStart(t *testing.T, pool *pgxpool.Pool, at time.Time) {
	t.Helper()
	exec(t, pool, `INSERT INTO jobs (type, source, payload, status, run_after, started_at, finished_at)
		VALUES ('search', 'facebook_marketplace', '{}', 'succeeded', $1, $1, $1)`, at)
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/jobs/`
Expected: FAIL de compilação (pacote `jobs` não existe).

- [ ] **Step 3: Implementar**

`backend/internal/jobs/queue.go`:

```go
// Package jobs implements the Postgres-backed job queue.
// All time comparisons use Queue.Clock, never the database clock.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/db"
)

const defaultMaxAttempts = 4

type NewJob struct {
	Type        string
	Source      string
	SearchID    string
	RegionID    string
	Priority    int
	Payload     json.RawMessage
	MaxAttempts int // 0 means defaultMaxAttempts
}

type Job struct {
	ID         string
	Type       string
	Source     string
	SearchID   *string
	RegionID   *string
	Attempt    int
	Payload    json.RawMessage
	StartedAt  time.Time
	StopBefore *time.Time // start of the last successful run for the same search and region
}

type QuietHours struct {
	Enabled   bool
	StartHour int // inclusive, 0-23
	EndHour   int // exclusive, 0-23
	Location  *time.Location
}

// Contains reports whether t falls in the quiet window. Windows may wrap midnight.
func (q QuietHours) Contains(t time.Time) bool {
	if !q.Enabled {
		return false
	}
	h := t.In(q.Location).Hour()
	if q.StartHour <= q.EndHour {
		return h >= q.StartHour && h < q.EndHour
	}
	return h >= q.StartHour || h < q.EndHour
}

type Limits struct {
	MaxPerHour int
	MinGap     time.Duration
	Lease      time.Duration
	Quiet      QuietHours
}

type Queue struct {
	pool   *pgxpool.Pool
	limits Limits
	Clock  func() time.Time
}

func New(pool *pgxpool.Pool, limits Limits) *Queue {
	return &Queue{pool: pool, limits: limits, Clock: time.Now}
}

// Enqueue inserts a job unless the same source/search/region is already in flight.
func (q *Queue) Enqueue(ctx context.Context, d db.DBTX, j NewJob) (bool, error) {
	maxAttempts := j.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	tag, err := d.Exec(ctx, `INSERT INTO jobs
		(type, source, search_id, region_id, priority, payload, max_attempts, run_after)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT DO NOTHING`,
		j.Type, j.Source, j.SearchID, j.RegionID, j.Priority, j.Payload, maxAttempts, q.Clock())
	if err != nil {
		return false, fmt.Errorf("enqueue: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const pickSQL = `
WITH open_sources AS (
    SELECT s.source
    FROM source_sessions s
    WHERE s.source = ANY($1)
      AND s.status = 'ok'
      AND (s.cooldown_until IS NULL OR s.cooldown_until <= $3)
      AND (SELECT count(*) FROM jobs j WHERE j.source = s.source AND j.started_at > $4) < $5
      AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.source = s.source AND j.started_at > $6)
)
SELECT id FROM jobs
WHERE status = 'queued'
  AND run_after <= $3
  AND type = ANY($2)
  AND source IN (SELECT source FROM open_sources)
ORDER BY priority DESC, run_after
LIMIT 1
FOR UPDATE SKIP LOCKED`

// Claim leases the next eligible job, or returns nil when none may run now.
func (q *Queue) Claim(ctx context.Context, workerID string, sources, types []string) (*Job, error) {
	now := q.Clock()
	if q.limits.Quiet.Contains(now) {
		return nil, nil
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('jobs.claim'))`); err != nil {
		return nil, fmt.Errorf("claim lock: %w", err)
	}
	var id string
	err = tx.QueryRow(ctx, pickSQL, sources, types, now,
		now.Add(-time.Hour), q.limits.MaxPerHour, now.Add(-q.limits.MinGap)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pick job: %w", err)
	}

	var j Job
	err = tx.QueryRow(ctx, `UPDATE jobs
		SET status = 'running', attempts = attempts + 1, started_at = $2, lease_until = $3, worker_id = $4
		WHERE id = $1
		RETURNING id, type, source, search_id, region_id, attempts, payload, started_at`,
		id, now, now.Add(q.limits.Lease), workerID).Scan(
		&j.ID, &j.Type, &j.Source, &j.SearchID, &j.RegionID, &j.Attempt, &j.Payload, &j.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("lease job: %w", err)
	}
	if j.SearchID != nil && j.RegionID != nil {
		err = tx.QueryRow(ctx, `SELECT last_success_at FROM search_definition_regions
			WHERE search_id = $1 AND region_id = $2`, *j.SearchID, *j.RegionID).Scan(&j.StopBefore)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("load stop_before: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workers
		SET status = 'busy', current_job_id = $2, last_heartbeat_at = $3 WHERE id = $1`,
		workerID, j.ID, now); err != nil {
		return nil, fmt.Errorf("mark worker busy: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &j, nil
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/jobs/ -v`
Expected: PASS em `TestEnqueueSkipsDuplicateInFlight`, `TestClaimReturnsJobAndMarksRunning`, `TestClaimPrefersHigherPriority`, os 6 subtestes de `TestClaimGates` e `TestConcurrentClaimsNeverShareJob`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/jobs/
git commit -m "feat(backend): add postgres job queue with gated claim"
```

---

### Task 8: Ciclo de vida do job — Complete, Fail, Reap, ResumeSource

Reações por `error_kind` (spec §4.4):

| `error_kind` | Job | Fonte |
|---|---|---|
| `network`, `timeout` (e desconhecidos) | backoff 1 min, 5 min, 25 min; após `max_attempts` (4) vira `dead` | — |
| `rate_limited` | volta para a fila após 60 min (ou `dead` se esgotado) | `cooldown_until = now + 60 min` |
| `checkpoint`, `login_required` | volta para a fila sem consumir tentativa | `needs_attention` + alerta de sistema |
| `layout_changed` | uma nova tentativa após 5 min; depois `dead` | alerta de sistema (no máximo 1 por dia) |

`Complete` e `Fail` só aceitam o job se ele estiver `running` com o mesmo `attempt`. Caso contrário retornam `ErrStale`. Isso torna o envio de resultado idempotente.

**Files:**
- Create: `backend/internal/jobs/lifecycle.go`
- Test: `backend/internal/jobs/lifecycle_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/jobs/lifecycle_test.go`:

```go
package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/testdb"
)

func complete(t *testing.T, pool *pgxpool.Pool, q *jobs.Queue, j *jobs.Job, attempt int) (jobs.Completed, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	done, err := q.Complete(ctx, tx, j.ID, attempt, "w1")
	if err != nil {
		return done, err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return done, nil
}

func jobStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM jobs WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCompleteMarksSucceededAndRecordsLastSuccess(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	if _, err := complete(t, pool, q, j, 2); !errors.Is(err, jobs.ErrStale) {
		t.Fatalf("wrong attempt: err = %v, want ErrStale", err)
	}
	done, err := complete(t, pool, q, j, 1)
	if err != nil {
		t.Fatal(err)
	}
	if done.Source != "facebook_marketplace" || *done.SearchID != s.SearchID || !done.StartedAt.Equal(c.now) {
		t.Fatalf("completed = %+v", done)
	}
	if st := jobStatus(t, pool, j.ID); st != "succeeded" {
		t.Fatalf("status = %s", st)
	}
	if _, err := complete(t, pool, q, j, 1); !errors.Is(err, jobs.ErrStale) {
		t.Fatalf("second complete: err = %v, want ErrStale", err)
	}

	// The next run of the same pair gets stop_before = previous start.
	c.Advance(time.Hour)
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	next := claim(t, q)
	if next.StopBefore == nil || !next.StopBefore.Equal(j.StartedAt) {
		t.Fatalf("stop_before = %v, want %v", next.StopBefore, j.StartedAt)
	}
}

func TestFailNetworkBacksOffThenDies(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ctx := context.Background()

	for i, wait := range []time.Duration{time.Minute, 5 * time.Minute, 25 * time.Minute} {
		j := claim(t, q)
		if j == nil || j.Attempt != i+1 {
			t.Fatalf("round %d: job = %+v", i, j)
		}
		out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindNetwork, "boom")
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != "queued" || !out.RunAfter.Equal(c.now.Add(wait)) {
			t.Fatalf("round %d: outcome = %+v", i, out)
		}
		c.Advance(wait)
	}
	j := claim(t, q)
	out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindTimeout, "boom")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "dead" || jobStatus(t, pool, j.ID) != "dead" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestFailCheckpointPausesSourceUntilResumed(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ctx := context.Background()

	j := claim(t, q)
	out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindCheckpoint, "checkpoint page")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "queued" {
		t.Fatalf("outcome = %+v", out)
	}
	var session string
	if err := pool.QueryRow(ctx, `SELECT status FROM source_sessions
		WHERE source = 'facebook_marketplace'`).Scan(&session); err != nil {
		t.Fatal(err)
	}
	if session != "needs_attention" {
		t.Fatalf("session = %s", session)
	}
	var alerts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE kind = 'system'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("system alerts = %d", alerts)
	}

	c.Advance(24 * time.Hour)
	if claim(t, q) != nil {
		t.Fatal("claimed while session needs attention")
	}
	if err := q.ResumeSource(ctx, "facebook_marketplace"); err != nil {
		t.Fatal(err)
	}
	again := claim(t, q)
	if again == nil || again.Attempt != 1 {
		t.Fatalf("after resume: job = %+v, want attempt 1", again)
	}
}

func TestFailRateLimitedCoolsDownSource(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)

	j := claim(t, q)
	if _, err := q.Fail(context.Background(), j.ID, j.Attempt, "w1", jobs.KindRateLimited, "slow down"); err != nil {
		t.Fatal(err)
	}
	c.Advance(59 * time.Minute)
	if claim(t, q) != nil {
		t.Fatal("claimed during cooldown")
	}
	c.Advance(2 * time.Minute)
	if claim(t, q) == nil {
		t.Fatal("no job after cooldown")
	}
}

func TestFailLayoutChangedRetriesOnceThenDies(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	ctx := context.Background()

	j := claim(t, q)
	out, err := q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindLayoutChanged, "no results captured")
	if err != nil || out.Status != "queued" {
		t.Fatalf("first: %+v, %v", out, err)
	}
	c.Advance(5 * time.Minute)
	j = claim(t, q)
	out, err = q.Fail(ctx, j.ID, j.Attempt, "w1", jobs.KindLayoutChanged, "no results captured")
	if err != nil || out.Status != "dead" {
		t.Fatalf("second: %+v, %v", out, err)
	}
	var alerts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE kind = 'system'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("system alerts = %d, want 1 per day", alerts)
	}
}

func TestFailRejectsStaleAttempt(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)
	if _, err := q.Fail(context.Background(), j.ID, 9, "w1", jobs.KindNetwork, "x"); !errors.Is(err, jobs.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
}

func TestReapExpiredLeases(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, c := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	c.Advance(5 * time.Minute)
	if n, err := q.ReapExpiredLeases(context.Background()); err != nil || n != 0 {
		t.Fatalf("early reap = %d, %v", n, err)
	}
	c.Advance(6 * time.Minute)
	if n, err := q.ReapExpiredLeases(context.Background()); err != nil || n != 1 {
		t.Fatalf("reap = %d, %v", n, err)
	}
	if st := jobStatus(t, pool, j.ID); st != "queued" {
		t.Fatalf("status = %s", st)
	}
	if next := claim(t, q); next == nil || next.Attempt != 2 {
		t.Fatalf("reclaimed = %+v, want attempt 2", next)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/jobs/`
Expected: FAIL de compilação: `undefined: jobs.Completed` (e demais símbolos novos).

- [ ] **Step 3: Implementar**

`backend/internal/jobs/lifecycle.go`:

```go
package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrStale means the job is not running with the given attempt
// (already finished, reaped, or a duplicate submission).
var ErrStale = errors.New("job is not running with this attempt")

type ErrorKind string

const (
	KindNetwork       ErrorKind = "network"
	KindTimeout       ErrorKind = "timeout"
	KindRateLimited   ErrorKind = "rate_limited"
	KindCheckpoint    ErrorKind = "checkpoint"
	KindLoginRequired ErrorKind = "login_required"
	KindLayoutChanged ErrorKind = "layout_changed"
)

func ParseErrorKind(s string) (ErrorKind, bool) {
	switch k := ErrorKind(s); k {
	case KindNetwork, KindTimeout, KindRateLimited, KindCheckpoint, KindLoginRequired, KindLayoutChanged:
		return k, true
	}
	return "", false
}

const (
	rateLimitCooldown        = time.Hour
	layoutChangedRetryDelay  = 5 * time.Minute
	layoutChangedMaxAttempts = 2
)

var backoff = []time.Duration{time.Minute, 5 * time.Minute, 25 * time.Minute}

func backoffFor(attempt int) time.Duration {
	i := min(max(attempt-1, 0), len(backoff)-1)
	return backoff[i]
}

type Completed struct {
	Source     string
	SearchID   *string
	RegionID   *string
	StartedAt  time.Time
	FinishedAt time.Time
}

type Outcome struct {
	Status   string     `json:"status"`
	RunAfter *time.Time `json:"run_after,omitempty"`
}

// Complete marks a running job as succeeded inside the caller's transaction,
// so ingest and completion commit together.
func (q *Queue) Complete(ctx context.Context, tx pgx.Tx, jobID string, attempt int, workerID string) (Completed, error) {
	now := q.Clock()
	c := Completed{FinishedAt: now}
	err := tx.QueryRow(ctx, `UPDATE jobs
		SET status = 'succeeded', finished_at = $3, lease_until = NULL, error_kind = NULL, error_message = NULL
		WHERE id = $1 AND attempts = $2 AND status = 'running'
		RETURNING source, search_id, region_id, started_at`,
		jobID, attempt, now).Scan(&c.Source, &c.SearchID, &c.RegionID, &c.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Completed{}, ErrStale
	}
	if err != nil {
		return Completed{}, fmt.Errorf("complete job: %w", err)
	}
	if c.SearchID != nil && c.RegionID != nil {
		if _, err := tx.Exec(ctx, `UPDATE search_definition_regions SET last_success_at = $3
			WHERE search_id = $1 AND region_id = $2`, *c.SearchID, *c.RegionID, c.StartedAt); err != nil {
			return Completed{}, fmt.Errorf("record last success: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workers
		SET status = 'idle', current_job_id = NULL, jobs_ok = jobs_ok + 1, last_heartbeat_at = $2
		WHERE id = $1`, workerID, now); err != nil {
		return Completed{}, fmt.Errorf("update worker: %w", err)
	}
	return c, nil
}

// Fail records a classified failure and decides retry, death and source state.
func (q *Queue) Fail(ctx context.Context, jobID string, attempt int, workerID string, kind ErrorKind, message string) (Outcome, error) {
	now := q.Clock()
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback(ctx)

	var source string
	var attempts, maxAttempts int
	err = tx.QueryRow(ctx, `SELECT source, attempts, max_attempts FROM jobs
		WHERE id = $1 AND attempts = $2 AND status = 'running' FOR UPDATE`,
		jobID, attempt).Scan(&source, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, ErrStale
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("load job: %w", err)
	}

	var out Outcome
	requeue := func(at time.Time, consumeAttempt bool) error {
		refund := 0
		if !consumeAttempt {
			refund = 1
		}
		out = Outcome{Status: "queued", RunAfter: &at}
		_, err := tx.Exec(ctx, `UPDATE jobs
			SET status = 'queued', run_after = $2, lease_until = NULL, worker_id = NULL,
			    attempts = attempts - $3, error_kind = $4, error_message = $5
			WHERE id = $1`, jobID, at, refund, string(kind), message)
		return err
	}
	bury := func() error {
		out = Outcome{Status: "dead"}
		_, err := tx.Exec(ctx, `UPDATE jobs
			SET status = 'dead', finished_at = $2, lease_until = NULL, error_kind = $3, error_message = $4
			WHERE id = $1`, jobID, now, string(kind), message)
		return err
	}

	switch kind {
	case KindCheckpoint, KindLoginRequired:
		err = requeue(now, false)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE source_sessions
				SET status = 'needs_attention', last_checkpoint_at = $2, note = $3, updated_at = $2
				WHERE source = $1`, source, now, message)
		}
		if err == nil {
			err = systemAlert(ctx, tx,
				fmt.Sprintf("system:%s:%s:%d", kind, source, now.Unix()),
				fmt.Sprintf("%s: sessão precisa de atenção (%s). Refaça o login e libere a fonte.", source, kind))
		}
	case KindRateLimited:
		at := now.Add(rateLimitCooldown)
		if attempts < maxAttempts {
			err = requeue(at, true)
		} else {
			err = bury()
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE source_sessions SET cooldown_until = $2, updated_at = $3
				WHERE source = $1`, source, at, now)
		}
	case KindLayoutChanged:
		if attempts < layoutChangedMaxAttempts && attempts < maxAttempts {
			err = requeue(now.Add(layoutChangedRetryDelay), true)
		} else {
			err = bury()
		}
		if err == nil {
			err = systemAlert(ctx, tx,
				fmt.Sprintf("system:layout_changed:%s:%s", source, now.Format("2006-01-02")),
				fmt.Sprintf("%s: layout mudou, nenhum anúncio capturado (%s).", source, message))
		}
	default: // network, timeout
		if attempts < maxAttempts {
			err = requeue(now.Add(backoffFor(attempts)), true)
		} else {
			err = bury()
		}
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("record failure: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workers
		SET status = 'idle', current_job_id = NULL, jobs_failed = jobs_failed + 1, last_heartbeat_at = $2
		WHERE id = $1`, workerID, now); err != nil {
		return Outcome{}, fmt.Errorf("update worker: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

func systemAlert(ctx context.Context, tx pgx.Tx, key, message string) error {
	_, err := tx.Exec(ctx, `INSERT INTO notifications (dedupe_key, kind, message)
		VALUES ($1, 'system', $2) ON CONFLICT (dedupe_key) DO NOTHING`, key, message)
	return err
}

// ReapExpiredLeases returns jobs whose worker vanished to the queue (or buries them).
func (q *Queue) ReapExpiredLeases(ctx context.Context) (int64, error) {
	now := q.Clock()
	tag, err := q.pool.Exec(ctx, `UPDATE jobs SET
		status      = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'queued' END,
		finished_at = CASE WHEN attempts >= max_attempts THEN $1::timestamptz END,
		run_after   = $1, lease_until = NULL, worker_id = NULL, error_kind = 'lease_expired'
		WHERE status = 'running' AND lease_until < $1`, now)
	if err != nil {
		return 0, fmt.Errorf("reap leases: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ResumeSource clears a paused or cooling-down source after a manual re-login.
func (q *Queue) ResumeSource(ctx context.Context, source string) error {
	_, err := q.pool.Exec(ctx, `UPDATE source_sessions
		SET status = 'ok', cooldown_until = NULL, note = NULL, updated_at = $2
		WHERE source = $1`, source, q.Clock())
	return err
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/jobs/ -v`
Expected: todos os testes do pacote PASS, incluindo os 7 novos.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/jobs/
git commit -m "feat(backend): add job completion, classified failures and lease reaping"
```

---

### Task 9: Registro de workers

**Files:**
- Create: `backend/internal/workers/workers.go`
- Test: `backend/internal/workers/workers_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/workers/workers_test.go`:

```go
package workers_test

import (
	"context"
	"errors"
	"testing"

	"scrapperbook/backend/internal/testdb"
	"scrapperbook/backend/internal/workers"
)

func TestRegisterAndHeartbeat(t *testing.T) {
	pool := testdb.New(t)
	r := workers.New(pool)
	ctx := context.Background()

	reg := workers.Registration{ID: "w1", Hostname: "box", CrawlerVersion: "0.1.0"}
	if err := r.Register(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(ctx, reg); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if err := r.Heartbeat(ctx, "w1", "idle", nil); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM workers WHERE id = 'w1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "idle" {
		t.Fatalf("status = %s", status)
	}
}

func TestHeartbeatErrors(t *testing.T) {
	pool := testdb.New(t)
	r := workers.New(pool)
	ctx := context.Background()
	if err := r.Heartbeat(ctx, "ghost", "idle", nil); !errors.Is(err, workers.ErrUnknownWorker) {
		t.Fatalf("err = %v, want ErrUnknownWorker", err)
	}
	if err := r.Register(ctx, workers.Registration{ID: "w1", Hostname: "box", CrawlerVersion: "0"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Heartbeat(ctx, "w1", "dancing", nil); !errors.Is(err, workers.ErrInvalidStatus) {
		t.Fatalf("err = %v, want ErrInvalidStatus", err)
	}
	if err := r.Register(ctx, workers.Registration{}); err == nil {
		t.Fatal("register without id succeeded")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/workers/`
Expected: FAIL de compilação (pacote `workers` não existe).

- [ ] **Step 3: Implementar**

`backend/internal/workers/workers.go`:

```go
// Package workers tracks crawler worker registration and health.
package workers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUnknownWorker = errors.New("unknown worker")
	ErrInvalidStatus = errors.New("invalid worker status")
)

var validStatuses = map[string]bool{
	"starting": true, "healthy": true, "busy": true, "idle": true,
	"error": true, "cooldown": true, "stopped": true,
}

type Registration struct {
	ID             string   `json:"worker_id"`
	Hostname       string   `json:"hostname"`
	CrawlerVersion string   `json:"crawler_version"`
	Sources        []string `json:"sources"`
}

type Registry struct {
	pool  *pgxpool.Pool
	Clock func() time.Time
}

func New(pool *pgxpool.Pool) *Registry {
	return &Registry{pool: pool, Clock: time.Now}
}

func (r *Registry) Register(ctx context.Context, reg Registration) error {
	if reg.ID == "" {
		return errors.New("worker_id is required")
	}
	if reg.Sources == nil {
		reg.Sources = []string{}
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO workers
		(id, hostname, status, crawler_version, sources, registered_at, last_heartbeat_at)
		VALUES ($1, $2, 'healthy', $3, $4, $5, $5)
		ON CONFLICT (id) DO UPDATE SET
			hostname = EXCLUDED.hostname, status = 'healthy',
			crawler_version = EXCLUDED.crawler_version, sources = EXCLUDED.sources,
			current_job_id = NULL, last_heartbeat_at = EXCLUDED.last_heartbeat_at`,
		reg.ID, reg.Hostname, reg.CrawlerVersion, reg.Sources, r.Clock())
	if err != nil {
		return fmt.Errorf("register worker: %w", err)
	}
	return nil
}

func (r *Registry) Heartbeat(ctx context.Context, id, status string, currentJobID *string) error {
	if !validStatuses[status] {
		return ErrInvalidStatus
	}
	tag, err := r.pool.Exec(ctx, `UPDATE workers
		SET status = $2, current_job_id = $3, last_heartbeat_at = $4 WHERE id = $1`,
		id, status, currentJobID, r.Clock())
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnknownWorker
	}
	return nil
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/workers/ -v`
Expected: 2 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/workers/
git commit -m "feat(backend): add worker registry with heartbeat"
```

---

### Task 10: API HTTP interna

Endpoints (spec §4), todos com `Authorization: Bearer $WORKER_TOKEN`:

| Rota | Sucesso | Erros |
|---|---|---|
| `POST /internal/v1/workers/register` | 204 | 400 |
| `POST /internal/v1/workers/{id}/heartbeat` | 204 | 400, 404 |
| `POST /internal/v1/jobs/claim` | 200 job, 204 vazio | 400 |
| `POST /internal/v1/jobs/{id}/result` | 200 `Stats` | 400, 404, 409 stale |
| `POST /internal/v1/jobs/{id}/fail` | 200 `Outcome` | 400, 404, 409 stale |

**Files:**
- Create: `backend/internal/httpapi/respond.go`, `backend/internal/httpapi/internal.go`, `backend/internal/httpapi/health.go`
- Test: `backend/internal/httpapi/internal_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/httpapi/internal_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/httpapi"
	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/source/facebook"
	"scrapperbook/backend/internal/testdb"
	"scrapperbook/backend/internal/workers"
)

const token = "secret"

type env struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
	seed testdb.Seeded
	q    *jobs.Queue
}

func setup(t *testing.T) env {
	t.Helper()
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	now := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	q := jobs.New(pool, jobs.Limits{MaxPerHour: 100, Lease: 10 * time.Minute})
	q.Clock = func() time.Time { return now }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := &httpapi.Internal{
		Token:   token,
		Pool:    pool,
		Queue:   q,
		Workers: workers.New(pool),
		Ingest:  ingest.New(ingest.NewRegistry(facebook.Normalizer{}), log),
		Log:     log,
	}
	mux := http.NewServeMux()
	api.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return env{srv: srv, pool: pool, seed: seed, q: q}
}

func (e env) enqueue(t *testing.T) {
	t.Helper()
	payload := `{"search":{"id":"` + e.seed.SearchID + `","keyword":"Honda Civic EG","filters":{}},
		"region":{"id":"curitiba-pr","fb_location":"curitiba"},"limits":{"max_pages":3,"max_items":120}}`
	ok, err := e.q.Enqueue(context.Background(), e.pool, jobs.NewJob{
		Type: "search", Source: facebook.Source, SearchID: e.seed.SearchID, RegionID: e.seed.RegionID,
		Payload: json.RawMessage(payload),
	})
	if err != nil || !ok {
		t.Fatalf("enqueue = %v, %v", ok, err)
	}
}

func (e env) post(t *testing.T, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return resp, out
}

func claimBody() map[string]any {
	return map[string]any{"worker_id": "w1", "sources": []string{"facebook_marketplace"}, "types": []string{"search"}}
}

func item(id, title, amount string) map[string]any {
	return map[string]any{"external_id": id, "raw": map[string]any{
		"id": id, "marketplace_listing_title": title, "listing_price": map[string]any{"amount": amount},
	}}
}

func TestRejectsMissingToken(t *testing.T) {
	e := setup(t)
	resp, err := http.Post(e.srv.URL+"/internal/v1/jobs/claim", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestClaimReturns204WhenEmpty(t *testing.T) {
	e := setup(t)
	resp, _ := e.post(t, "/internal/v1/jobs/claim", claimBody())
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestFullJobFlow(t *testing.T) {
	e := setup(t)
	resp, _ := e.post(t, "/internal/v1/workers/register", map[string]any{
		"worker_id": "w1", "hostname": "box", "crawler_version": "0.1.0", "sources": []string{"facebook_marketplace"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	e.enqueue(t)

	resp, job := e.post(t, "/internal/v1/jobs/claim", claimBody())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim status = %d", resp.StatusCode)
	}
	jobID, _ := job["job_id"].(string)
	if jobID == "" || job["attempt"] != float64(1) || job["source"] != "facebook_marketplace" || job["stop_before"] != nil {
		t.Fatalf("claim body = %v", job)
	}
	if kw := job["search"].(map[string]any)["keyword"]; kw != "Honda Civic EG" {
		t.Fatalf("keyword = %v", kw)
	}

	result := map[string]any{
		"worker_id": "w1", "attempt": 1, "crawler_version": "0.1.0", "parser_version": "fb-1",
		"items": []any{item("1", "Civic EG", "23500"), item("2", "Civic EG5", "21000")},
		"stats": map[string]any{"pages": 1, "duration_ms": 1200},
	}
	resp, stats := e.post(t, "/internal/v1/jobs/"+jobID+"/result", result)
	if resp.StatusCode != http.StatusOK || stats["new"] != float64(2) {
		t.Fatalf("result status = %d, body = %v", resp.StatusCode, stats)
	}

	resp, _ = e.post(t, "/internal/v1/jobs/"+jobID+"/result", result)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate result status = %d, want 409", resp.StatusCode)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM listings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("listings = %d", n)
	}
}

func TestFailEndpoint(t *testing.T) {
	e := setup(t)
	e.enqueue(t)
	_, job := e.post(t, "/internal/v1/jobs/claim", claimBody())
	jobID := job["job_id"].(string)

	resp, _ := e.post(t, "/internal/v1/jobs/"+jobID+"/fail",
		map[string]any{"worker_id": "w1", "attempt": 1, "error_kind": "bogus", "message": "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus kind status = %d", resp.StatusCode)
	}
	resp, out := e.post(t, "/internal/v1/jobs/"+jobID+"/fail",
		map[string]any{"worker_id": "w1", "attempt": 1, "error_kind": "network", "message": "reset"})
	if resp.StatusCode != http.StatusOK || out["status"] != "queued" {
		t.Fatalf("fail status = %d, body = %v", resp.StatusCode, out)
	}
	resp, _ = e.post(t, "/internal/v1/jobs/not-a-uuid/fail",
		map[string]any{"worker_id": "w1", "attempt": 1, "error_kind": "network"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad id status = %d", resp.StatusCode)
	}
}

func TestHeartbeatUnknownWorker(t *testing.T) {
	e := setup(t)
	resp, _ := e.post(t, "/internal/v1/workers/ghost/heartbeat", map[string]any{"status": "idle"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/httpapi/`
Expected: FAIL de compilação (pacote `httpapi` não existe).

- [ ] **Step 3: Implementar os helpers**

`backend/internal/httpapi/respond.go`:

```go
// Package httpapi exposes the HTTP endpoints.
package httpapi

import (
	"encoding/json"
	"net/http"
)

const maxBodyBytes = 8 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// isUUID checks the canonical 8-4-4-4-12 hex form so bad IDs never reach Postgres.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !('0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F') {
				return false
			}
		}
	}
	return true
}
```

- [ ] **Step 4: Implementar os handlers**

`backend/internal/httpapi/internal.go`:

```go
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/workers"
)

// Internal serves the worker-facing API under /internal/v1.
type Internal struct {
	Token   string
	Pool    *pgxpool.Pool
	Queue   *jobs.Queue
	Workers *workers.Registry
	Ingest  *ingest.Service
	Log     *slog.Logger
}

func (h *Internal) Routes(mux *http.ServeMux) {
	mux.Handle("POST /internal/v1/workers/register", h.auth(h.register))
	mux.Handle("POST /internal/v1/workers/{id}/heartbeat", h.auth(h.heartbeat))
	mux.Handle("POST /internal/v1/jobs/claim", h.auth(h.claim))
	mux.Handle("POST /internal/v1/jobs/{id}/result", h.auth(h.result))
	mux.Handle("POST /internal/v1/jobs/{id}/fail", h.auth(h.fail))
}

func (h *Internal) auth(next http.HandlerFunc) http.Handler {
	want := []byte("Bearer " + h.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if h.Token == "" || subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

func (h *Internal) register(w http.ResponseWriter, r *http.Request) {
	var reg workers.Registration
	if !decodeJSON(w, r, &reg) {
		return
	}
	if err := h.Workers.Register(r.Context(), reg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Internal) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status       string  `json:"status"`
		CurrentJobID *string `json:"current_job_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.CurrentJobID != nil && !isUUID(*body.CurrentJobID) {
		writeError(w, http.StatusBadRequest, "current_job_id must be a uuid")
		return
	}
	err := h.Workers.Heartbeat(r.Context(), r.PathValue("id"), body.Status, body.CurrentJobID)
	switch {
	case errors.Is(err, workers.ErrUnknownWorker):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, workers.ErrInvalidStatus):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		h.internalError(w, "heartbeat", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Internal) claim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkerID string   `json:"worker_id"`
		Sources  []string `json:"sources"`
		Types    []string `json:"types"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.WorkerID == "" || len(body.Sources) == 0 || len(body.Types) == 0 {
		writeError(w, http.StatusBadRequest, "worker_id, sources and types are required")
		return
	}
	job, err := h.Queue.Claim(r.Context(), body.WorkerID, body.Sources, body.Types)
	if err != nil {
		h.internalError(w, "claim", err)
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	msg := map[string]any{}
	if err := json.Unmarshal(job.Payload, &msg); err != nil {
		h.internalError(w, "decode payload", err)
		return
	}
	msg["job_id"] = job.ID
	msg["attempt"] = job.Attempt
	msg["type"] = job.Type
	msg["source"] = job.Source
	msg["stop_before"] = job.StopBefore
	h.Log.Info("job claimed", "job_id", job.ID, "worker_id", body.WorkerID, "attempt", job.Attempt)
	writeJSON(w, http.StatusOK, msg)
}

type resultRequest struct {
	WorkerID       string          `json:"worker_id"`
	Attempt        int             `json:"attempt"`
	CrawlerVersion string          `json:"crawler_version"`
	ParserVersion  string          `json:"parser_version"`
	Items          []ingest.Item   `json:"items"`
	Stats          json.RawMessage `json:"stats"`
}

func (h *Internal) result(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	var req resultRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		h.internalError(w, "begin", err)
		return
	}
	defer tx.Rollback(ctx)

	done, err := h.Queue.Complete(ctx, tx, id, req.Attempt, req.WorkerID)
	if errors.Is(err, jobs.ErrStale) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		h.internalError(w, "complete", err)
		return
	}
	if done.SearchID == nil || done.RegionID == nil {
		writeError(w, http.StatusUnprocessableEntity, "job has no search context")
		return
	}
	stats, err := h.Ingest.Ingest(ctx, tx, ingest.Batch{
		JobID: id, Source: done.Source, SearchID: *done.SearchID, RegionID: *done.RegionID,
		CrawlerVersion: req.CrawlerVersion, ParserVersion: req.ParserVersion,
		SeenAt: done.FinishedAt, Items: req.Items,
	})
	if err != nil {
		h.internalError(w, "ingest", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.internalError(w, "commit", err)
		return
	}
	h.Log.Info("job result ingested", "job_id", id, "attempt", req.Attempt,
		"new", stats.New, "changed", stats.Changed, "seen", stats.Seen,
		"invalid", stats.Invalid, "alerts", stats.Alerts, "crawler_stats", string(req.Stats))
	writeJSON(w, http.StatusOK, stats)
}

func (h *Internal) fail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	var req struct {
		WorkerID  string `json:"worker_id"`
		Attempt   int    `json:"attempt"`
		ErrorKind string `json:"error_kind"`
		Message   string `json:"message"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	kind, ok := jobs.ParseErrorKind(req.ErrorKind)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown error_kind: "+req.ErrorKind)
		return
	}
	out, err := h.Queue.Fail(r.Context(), id, req.Attempt, req.WorkerID, kind, req.Message)
	if errors.Is(err, jobs.ErrStale) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		h.internalError(w, "fail", err)
		return
	}
	h.Log.Warn("job failed", "job_id", id, "attempt", req.Attempt, "error_kind", kind,
		"message", req.Message, "outcome", out.Status)
	writeJSON(w, http.StatusOK, out)
}

func (h *Internal) internalError(w http.ResponseWriter, op string, err error) {
	h.Log.Error(op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
```

- [ ] **Step 5: Implementar health checks**

`backend/internal/httpapi/health.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Health registers liveness (/healthz) and readiness (/readyz, pings Postgres).
func Health(mux *http.ServeMux, pool *pgxpool.Pool) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
```

- [ ] **Step 6: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/httpapi/ -v`
Expected: 5 testes PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/httpapi/
git commit -m "feat(backend): add internal worker API and health endpoints"
```

---

### Task 11: Scheduler

A cada tick, cada busca habilitada e vencida gera um job por região com `fb_location` definida. Em seguida, `next_run_at` avança `frequency` com jitter de ±10%. O jitter é calculado no SQL para evitar converter `interval` em Go.

**Files:**
- Create: `backend/internal/scheduler/scheduler.go`
- Test: `backend/internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/scheduler/scheduler_test.go`:

```go
package scheduler_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/testdb"
)

var now = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

func newScheduler(pool *pgxpool.Pool) *scheduler.Scheduler {
	q := jobs.New(pool, jobs.Limits{})
	q.Clock = func() time.Time { return now }
	s := scheduler.New(pool, q, []string{"facebook_marketplace"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Clock = func() time.Time { return now }
	return s
}

func tick(t *testing.T, s *scheduler.Scheduler) int {
	t.Helper()
	n, err := s.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTickEnqueuesOneJobPerRegionAndReschedules(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	testdb.LinkRegion(t, pool, seed.SearchID, "londrina-pr")
	s := newScheduler(pool)

	if n := tick(t, s); n != 2 {
		t.Fatalf("enqueued %d, want 2", n)
	}
	var payload map[string]any
	if err := pool.QueryRow(context.Background(), `SELECT payload FROM jobs
		WHERE region_id = 'curitiba-pr'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	region := payload["region"].(map[string]any)
	search := payload["search"].(map[string]any)
	if region["fb_location"] != "curitiba" || search["keyword"] != "Honda Civic EG" {
		t.Fatalf("payload = %v", payload)
	}
	if limits := payload["limits"].(map[string]any); limits["max_pages"] != float64(3) {
		t.Fatalf("limits = %v", limits)
	}

	var next time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT next_run_at FROM search_definitions`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next.Before(now.Add(27*time.Minute)) || next.After(now.Add(33*time.Minute)) {
		t.Fatalf("next_run_at = %v, want now+30m±10%%", next)
	}
	if n := tick(t, s); n != 0 {
		t.Fatalf("second tick enqueued %d, want 0 (not due)", n)
	}
}

func TestTickDoesNotDuplicateInFlightJobs(t *testing.T) {
	pool := testdb.New(t)
	testdb.Seed(t, pool)
	s := newScheduler(pool)
	tick(t, s)
	if _, err := pool.Exec(context.Background(),
		`UPDATE search_definitions SET next_run_at = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if n := tick(t, s); n != 0 {
		t.Fatalf("enqueued %d while previous job still queued", n)
	}
}

func TestTickSkipsDisabledSearchesAndRegionsWithoutLocation(t *testing.T) {
	pool := testdb.New(t)
	seed := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "nowhere", "")
	testdb.LinkRegion(t, pool, seed.SearchID, "nowhere")
	s := newScheduler(pool)
	if n := tick(t, s); n != 1 {
		t.Fatalf("enqueued %d, want 1 (region without fb_location skipped)", n)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE search_definitions SET enabled = false, next_run_at = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM jobs`); err != nil {
		t.Fatal(err)
	}
	if n := tick(t, s); n != 0 {
		t.Fatalf("disabled search enqueued %d", n)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/scheduler/`
Expected: FAIL de compilação (pacote `scheduler` não existe).

- [ ] **Step 3: Implementar**

`backend/internal/scheduler/scheduler.go`:

```go
// Package scheduler turns due search definitions into jobs.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
)

type PageLimits struct {
	MaxPages int `json:"max_pages"`
	MaxItems int `json:"max_items"`
}

type Scheduler struct {
	pool    *pgxpool.Pool
	queue   *jobs.Queue
	sources []string
	limits  PageLimits
	log     *slog.Logger
	Clock   func() time.Time
}

func New(pool *pgxpool.Pool, queue *jobs.Queue, sources []string, log *slog.Logger) *Scheduler {
	return &Scheduler{
		pool: pool, queue: queue, sources: sources, log: log,
		limits: PageLimits{MaxPages: 3, MaxItems: 120},
		Clock:  time.Now,
	}
}

type searchPayload struct {
	ID      string          `json:"id"`
	Keyword string          `json:"keyword"`
	Filters json.RawMessage `json:"filters"`
}

// regionPayload carries fb_location because Facebook is the only MVP source.
type regionPayload struct {
	ID         string   `json:"id"`
	FBLocation string   `json:"fb_location"`
	Lat        *float64 `json:"lat"`
	Lng        *float64 `json:"lng"`
	RadiusKM   *int     `json:"radius_km"`
}

type payload struct {
	Search searchPayload `json:"search"`
	Region regionPayload `json:"region"`
	Limits PageLimits    `json:"limits"`
}

type dueSearch struct {
	search   searchPayload
	priority int
}

// Tick enqueues jobs for every due search and returns how many were created.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	now := s.Clock()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT id, keyword, filters, priority FROM search_definitions
		WHERE enabled AND next_run_at <= $1
		ORDER BY priority DESC
		FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return 0, fmt.Errorf("select due searches: %w", err)
	}
	var due []dueSearch
	for rows.Next() {
		var d dueSearch
		if err := rows.Scan(&d.search.ID, &d.search.Keyword, &d.search.Filters, &d.priority); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	created := 0
	for _, d := range due {
		regions, err := regionsFor(ctx, tx, d.search.ID)
		if err != nil {
			return 0, err
		}
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
		if _, err := tx.Exec(ctx, `UPDATE search_definitions
			SET next_run_at = $2::timestamptz + frequency * (0.9 + random() * 0.2), updated_at = $2
			WHERE id = $1`, d.search.ID, now); err != nil {
			return 0, fmt.Errorf("reschedule: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return created, nil
}

func regionsFor(ctx context.Context, tx pgx.Tx, searchID string) ([]regionPayload, error) {
	rows, err := tx.Query(ctx, `SELECT r.id, r.fb_location, r.lat, r.lng, r.radius_km
		FROM search_definition_regions sdr
		JOIN search_regions r ON r.id = sdr.region_id
		WHERE sdr.search_id = $1 AND r.enabled AND r.fb_location IS NOT NULL
		ORDER BY r.id`, searchID)
	if err != nil {
		return nil, fmt.Errorf("select regions: %w", err)
	}
	defer rows.Close()
	var out []regionPayload
	for rows.Next() {
		var rg regionPayload
		if err := rows.Scan(&rg.ID, &rg.FBLocation, &rg.Lat, &rg.Lng, &rg.RadiusKM); err != nil {
			return nil, err
		}
		out = append(out, rg)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/scheduler/ -v`
Expected: 3 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/scheduler/
git commit -m "feat(backend): add scheduler that expands due searches into region jobs"
```

---

### Task 12: Configuração

**Files:**
- Create: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`backend/internal/config/config_test.go`:

```go
package config_test

import (
	"testing"
	"time"

	"scrapperbook/backend/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.Limits.MaxPerHour != 6 || c.Limits.MinGap != 4*time.Minute ||
		c.Limits.Lease != 10*time.Minute {
		t.Fatalf("config = %+v", c)
	}
	q := c.Limits.Quiet
	if !q.Enabled || q.StartHour != 23 || q.EndHour != 7 || q.Location.String() != "America/Sao_Paulo" {
		t.Fatalf("quiet = %+v", q)
	}
}

func TestQuietHoursOffAndOverrides(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t",
		"QUIET_HOURS": "off", "FB_MAX_JOBS_PER_HOUR": "12", "FB_MIN_GAP": "90s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.Quiet.Enabled || c.Limits.MaxPerHour != 12 || c.Limits.MinGap != 90*time.Second {
		t.Fatalf("config = %+v", c)
	}
}

func TestInvalidConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}
	cases := map[string]map[string]string{
		"missing db":     {"WORKER_TOKEN": "t"},
		"missing token":  {"DATABASE_URL": "postgres://x"},
		"bad quiet":      {"QUIET_HOURS": "25-7"},
		"bad quiet form": {"QUIET_HOURS": "night"},
		"bad gap":        {"FB_MIN_GAP": "soon"},
		"bad max":        {"FB_MAX_JOBS_PER_HOUR": "many"},
	}
	for name, override := range cases {
		m := map[string]string{}
		if name != "missing db" && name != "missing token" {
			for k, v := range base {
				m[k] = v
			}
		}
		for k, v := range override {
			m[k] = v
		}
		if _, err := config.Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd backend && go test ./internal/config/`
Expected: FAIL de compilação (pacote `config` não existe).

- [ ] **Step 3: Implementar**

`backend/internal/config/config.go`:

```go
// Package config reads server settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"scrapperbook/backend/internal/jobs"
)

type Config struct {
	DatabaseURL string
	WorkerToken string
	HTTPAddr    string
	Limits      jobs.Limits
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL: getenv("DATABASE_URL"),
		WorkerToken: getenv("WORKER_TOKEN"),
		HTTPAddr:    or(getenv("HTTP_ADDR"), ":8080"),
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if c.WorkerToken == "" {
		return Config{}, errors.New("WORKER_TOKEN is required")
	}
	loc, err := time.LoadLocation(or(getenv("TIMEZONE"), "America/Sao_Paulo"))
	if err != nil {
		return Config{}, fmt.Errorf("TIMEZONE: %w", err)
	}
	maxPerHour, err := strconv.Atoi(or(getenv("FB_MAX_JOBS_PER_HOUR"), "6"))
	if err != nil {
		return Config{}, fmt.Errorf("FB_MAX_JOBS_PER_HOUR: %w", err)
	}
	minGap, err := time.ParseDuration(or(getenv("FB_MIN_GAP"), "4m"))
	if err != nil {
		return Config{}, fmt.Errorf("FB_MIN_GAP: %w", err)
	}
	lease, err := time.ParseDuration(or(getenv("JOB_LEASE"), "10m"))
	if err != nil {
		return Config{}, fmt.Errorf("JOB_LEASE: %w", err)
	}
	quiet, err := parseQuietHours(or(getenv("QUIET_HOURS"), "23-7"), loc)
	if err != nil {
		return Config{}, err
	}
	c.Limits = jobs.Limits{MaxPerHour: maxPerHour, MinGap: minGap, Lease: lease, Quiet: quiet}
	return c, nil
}

// parseQuietHours accepts "off" or "START-END" in whole hours, e.g. "23-7".
func parseQuietHours(s string, loc *time.Location) (jobs.QuietHours, error) {
	if s == "off" {
		return jobs.QuietHours{Location: loc}, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return jobs.QuietHours{}, fmt.Errorf("QUIET_HOURS %q: want off or START-END", s)
	}
	start, err1 := strconv.Atoi(a)
	end, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || start < 0 || start > 23 || end < 0 || end > 23 {
		return jobs.QuietHours{}, fmt.Errorf("QUIET_HOURS %q: hours must be 0-23", s)
	}
	return jobs.QuietHours{Enabled: true, StartHour: start, EndHour: end, Location: loc}, nil
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd backend && go test ./internal/config/ -v`
Expected: 3 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/
git commit -m "feat(backend): load server config from environment"
```

---

### Task 13: Servidor, Docker e smoke test

**Files:**
- Create: `backend/cmd/server/main.go`
- Create: `backend/Dockerfile`, `backend/.dockerignore`
- Create: `backend/seed/dev.sql`, `backend/seed/smoke-result.json`
- Modify: `compose.yaml` (adicionar serviço `api`)

- [ ] **Step 1: Escrever o `main`**

`backend/cmd/server/main.go`:

```go
// Command server runs the API, the scheduler and the lease reaper.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo

	"scrapperbook/backend/internal/config"
	"scrapperbook/backend/internal/db"
	"scrapperbook/backend/internal/httpapi"
	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/source/facebook"
	"scrapperbook/backend/internal/workers"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

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

	go background(ctx, log, sched, queue)

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("server listening", "addr", cfg.HTTPAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// background runs the scheduler tick and lease reaper once a minute.
func background(ctx context.Context, log *slog.Logger, sched *scheduler.Scheduler, queue *jobs.Queue) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
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
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
```

- [ ] **Step 2: Compilar**

Run: `cd backend && go build ./... && go vet ./...`
Expected: sem saída (sucesso).

- [ ] **Step 3: Criar `backend/Dockerfile` e `backend/.dockerignore`**

`backend/Dockerfile`:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/server /server
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/server"]
```

`backend/.dockerignore`:

```text
bin/
**/*_test.go
**/testdata/
seed/
```

- [ ] **Step 4: Adicionar o serviço `api` ao `compose.yaml`**

Inserir abaixo do serviço `postgres` (antes de `volumes:`):

```yaml
  api:
    build: ./backend
    environment:
      DATABASE_URL: postgres://monitor:${POSTGRES_PASSWORD}@postgres:5432/monitor?sslmode=disable
      WORKER_TOKEN: ${WORKER_TOKEN:?set WORKER_TOKEN in .env}
      QUIET_HOURS: ${QUIET_HOURS:-23-7}
      FB_MAX_JOBS_PER_HOUR: ${FB_MAX_JOBS_PER_HOUR:-6}
      FB_MIN_GAP: ${FB_MIN_GAP:-4m}
    ports:
      - "127.0.0.1:8080:8080"
    depends_on:
      postgres:
        condition: service_healthy
    restart: unless-stopped
```

- [ ] **Step 5: Criar o seed de desenvolvimento**

`backend/seed/dev.sql`:

```sql
-- Exemplo do MVP: "Honda Civic EG" em Curitiba/PR a cada 30 minutos.
-- fb_location 'curitiba' é provisório; o plano 2 confirma o slug real do Facebook.
INSERT INTO search_regions (id, name, kind, city, state, lat, lng, radius_km, fb_location)
VALUES ('curitiba-pr', 'Curitiba/PR', 'city', 'Curitiba', 'PR', -25.4284, -49.2733, 60, 'curitiba')
ON CONFLICT (id) DO NOTHING;

INSERT INTO search_definitions (id, name, keyword, frequency, filters, alert)
VALUES ('00000000-0000-0000-0000-00000000c1c1', 'Civic EG', 'Honda Civic EG', '30 minutes',
        '{"max_price": 30000}',
        '{"enabled": true, "on_new": true, "on_price_drop": true, "max_price": 30000}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO search_definition_regions (search_id, region_id)
VALUES ('00000000-0000-0000-0000-00000000c1c1', 'curitiba-pr')
ON CONFLICT DO NOTHING;
```

`backend/seed/smoke-result.json`:

```json
{
  "worker_id": "smoke",
  "attempt": 1,
  "crawler_version": "smoke",
  "parser_version": "smoke",
  "items": [
    {
      "external_id": "1029384756",
      "raw": {
        "id": "1029384756",
        "marketplace_listing_title": "Honda Civic EG 1993 hatch",
        "listing_price": {"amount": "23500.00"},
        "location": {"reverse_geocode": {"city": "Curitiba", "state": "PR"}}
      }
    }
  ],
  "stats": {"pages": 1, "duration_ms": 1000}
}
```

- [ ] **Step 6: Subir e rodar o smoke test**

Run:
```bash
QUIET_HOURS=off docker compose up -d --build postgres api
docker compose exec -T postgres psql -U monitor -d monitor < backend/seed/dev.sql
curl -fsS localhost:8080/readyz && echo READY
```
Expected: `READY`.

O scheduler roda a cada minuto. Aguarde até 60 s e então:

```bash
TOKEN=$(grep '^WORKER_TOKEN=' .env | cut -d= -f2)
AUTH="Authorization: Bearer $TOKEN"
curl -fsS -X POST localhost:8080/internal/v1/workers/register -H "$AUTH" \
  -d '{"worker_id":"smoke","hostname":"local","crawler_version":"smoke","sources":["facebook_marketplace"]}'
JOB=$(curl -fsS -X POST localhost:8080/internal/v1/jobs/claim -H "$AUTH" \
  -d '{"worker_id":"smoke","sources":["facebook_marketplace"],"types":["search"]}')
echo "$JOB"
JOB_ID=$(echo "$JOB" | python3 -c 'import json,sys; print(json.load(sys.stdin)["job_id"])')
curl -fsS -X POST "localhost:8080/internal/v1/jobs/$JOB_ID/result" -H "$AUTH" -d @backend/seed/smoke-result.json
docker compose exec -T postgres psql -U monitor -d monitor -c \
  "SELECT external_id, title, price_cents, status FROM listings;"
```

Expected:
- o claim imprime JSON com `job_id`, `"attempt":1`, `"stop_before":null` e `"keyword":"Honda Civic EG"`;
- o result responde `{"new":1,"changed":0,"seen":0,"invalid":0,"alerts":0}`;
- a consulta mostra `1029384756 | Honda Civic EG 1993 hatch | 2350000 | active`.

Se o claim responder vazio (HTTP 204), o scheduler ainda não rodou: aguarde mais um minuto e repita o claim.

- [ ] **Step 7: Derrubar**

Run: `docker compose down`
Expected: containers removidos (o volume `pgdata` permanece).

- [ ] **Step 8: Commit**

```bash
git add backend/cmd backend/Dockerfile backend/.dockerignore backend/seed compose.yaml
git commit -m "feat(backend): add server entrypoint, docker image and dev seed"
```

---

### Task 14: Verificação final

- [ ] **Step 1: Suíte completa**

Run: `cd backend && go vet ./... && go test ./... -count=1`
Expected: todos os pacotes `ok`.

- [ ] **Step 2: Conferir critérios deste plano**

- [ ] Job executado duas vezes não cria anúncio duplicado (`TestIngestIsIdempotent`, `TestFullJobFlow`).
- [ ] Mudança de preço gera snapshot e alerta pendente (`TestPriceDropCreatesSnapshotAndAlert`).
- [ ] Primeira execução não alerta (`TestBaselineStoresListingsWithoutAlerts`).
- [ ] Checkpoint pausa a fonte e gera alerta de sistema (`TestFailCheckpointPausesSourceUntilResumed`).
- [ ] Rate budget, intervalo mínimo e quiet hours bloqueiam o claim (`TestClaimGates`).
- [ ] Smoke test da Task 13 passou.

## Próximo plano

Plano 2 (crawler Python) consome esta API. Ele também:
- confirma o slug real de `fb_location` para Curitiba;
- substitui a fixture sintética por uma captura real sanitizada;
- publica os JSON Schemas de job e result em `docs/contracts/`.
