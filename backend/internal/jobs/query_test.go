package jobs_test

import (
	"context"
	"testing"
	"time"

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

func TestListJobsOrdersByCreatedAtDesc(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	testdb.AddRegion(t, pool, "sao-paulo-sp", "sao_paulo")
	testdb.LinkRegion(t, pool, s.SearchID, "sao-paulo-sp")
	q, _ := newQueue(t, pool, openLimits())

	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	older := claim(t, q)
	enqueue(t, q, pool, s.SearchID, "sao-paulo-sp", 0)
	newer := claim(t, q)

	// Force explicit, well-separated created_at values (independent of
	// insertion/wall-clock order) so the assertion below can only pass if
	// ListJobs actually orders by created_at DESC.
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE jobs SET created_at = $1 WHERE id = $2`,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), older.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET created_at = $1 WHERE id = $2`,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), newer.ID); err != nil {
		t.Fatal(err)
	}

	list, err := q.ListJobs(ctx, jobs.JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("list = %+v, want [%s, %s]", list, newer.ID, older.ID)
	}
}

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

func TestRecordResultErrorsForUnknownJobID(t *testing.T) {
	pool := testdb.New(t)
	q, _ := newQueue(t, pool, openLimits())

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := q.RecordResult(ctx, tx, "00000000-0000-0000-0000-000000000000", 1, 0, 0, 0, 0, 0); err == nil {
		t.Fatal("want error for unknown job id, got nil")
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
