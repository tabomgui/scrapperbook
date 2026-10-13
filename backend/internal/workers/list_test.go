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
