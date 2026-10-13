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
