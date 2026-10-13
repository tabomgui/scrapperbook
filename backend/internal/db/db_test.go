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
