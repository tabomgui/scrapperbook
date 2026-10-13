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
