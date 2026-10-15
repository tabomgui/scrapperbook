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
		"items": []any{item("1", "Honda Civic EG 1993", "23500"), item("2", "Honda Civic EG5 1994", "21000")},
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
