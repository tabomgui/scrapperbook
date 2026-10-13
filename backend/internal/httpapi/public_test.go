package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		Pool: pool, Queue: jobs.New(pool, jobs.Limits{MaxPerHour: 100, Lease: 10 * time.Minute}),
		Workers: workers.New(pool), Log: log,
	}
	mux := http.NewServeMux()
	pub.Routes(mux)
	httpapi.Metrics(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return publicEnv{srv: srv, pool: pool}
}

func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
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

func TestGetSearchMalformedUUIDReturnsNotFound(t *testing.T) {
	e := setupPublic(t)
	resp, _ := doJSON(t, http.MethodGet, e.srv.URL+"/api/v1/searches/not-a-uuid", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get search with malformed id status = %d, want 404", resp.StatusCode)
	}
}

func TestCreateSearchValidationFailureReturnsBadRequest(t *testing.T) {
	e := setupPublic(t)
	resp, body := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/searches", map[string]any{
		"name": "No keyword", "enabled": true, "frequency_seconds": 1800,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create search without keyword status = %d, want 400", resp.StatusCode)
	}
	if msg, _ := body["error"].(string); msg != "validation failed: keyword is required" {
		t.Fatalf("create search error message = %q", msg)
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

	resp2, err := http.Get(e.srv.URL + "/api/v1/listings?state=PR&max_price=3000000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
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

func TestResumeUnknownSourceReturnsNotFound(t *testing.T) {
	e := setupPublic(t)
	resp, _ := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/sessions/does-not-exist/resume", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("resume unknown source status = %d, want 404", resp.StatusCode)
	}
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
	if !strings.Contains(string(body), "jobs_total") {
		t.Fatalf("expected jobs_total metric name in output, got %d bytes", len(body))
	}
}
