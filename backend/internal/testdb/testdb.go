// Package testdb provides a disposable, migrated Postgres for tests.
// One container is shared per test binary; New truncates all data.
//
// Tests using this package must not call t.Parallel(): New truncates the
// shared container's tables on every call, so parallel tests would wipe
// each other's data mid-test.
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
