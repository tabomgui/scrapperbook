package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"scrapperbook/backend/internal/scheduler"
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
	queue := jobs.New(pool, jobs.Limits{MaxPerHour: 100, Lease: 10 * time.Minute})
	sched := scheduler.New(pool, queue, []string{"facebook_marketplace"}, log)
	pub := &httpapi.Public{
		Pool: pool, Queue: queue, Workers: workers.New(pool), Scheduler: sched, Log: log,
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
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "1" {
		t.Fatalf("body = %+v", body)
	}
}

func TestHideAndUnhideListing(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	var id int64
	if err := e.pool.QueryRow(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', 'fp1', 'ch1',
		        now(), now(), now(), '{}')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	resp, _ := doJSON(t, http.MethodPost, fmt.Sprintf("%s/api/v1/listings/%d/hide", e.srv.URL, id), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hide status = %d", resp.StatusCode)
	}
	var hidden bool
	if err := e.pool.QueryRow(ctx, `SELECT hidden FROM listings WHERE id = $1`, id).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if !hidden {
		t.Fatal("hidden = false after /hide")
	}

	resp, _ = doJSON(t, http.MethodPost, fmt.Sprintf("%s/api/v1/listings/%d/unhide", e.srv.URL, id), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unhide status = %d", resp.StatusCode)
	}
	if err := e.pool.QueryRow(ctx, `SELECT hidden FROM listings WHERE id = $1`, id).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if hidden {
		t.Fatal("hidden = true after /unhide")
	}
}

func TestHideUnknownListingReturnsNotFound(t *testing.T) {
	e := setupPublic(t)
	resp, _ := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/listings/999999/hide", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestListingsExcludesHiddenByDefaultAndIncludesWithHiddenAll(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, hidden, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', false, 'fp1', 'ch1',
		        now(), now(), now(), '{}'),
		       ('facebook_marketplace', '2', 'https://x/2', 'Civic', 'active', true, 'fp2', 'ch2',
		        now(), now(), now(), '{}')`); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/listings")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if body.Total != 1 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "1" {
		t.Fatalf("default body = %+v, want only non-hidden", body)
	}

	resp, err = http.Get(e.srv.URL + "/api/v1/listings?hidden=all")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var allBody struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&allBody); err != nil {
		t.Fatal(err)
	}
	if allBody.Total != 2 || len(allBody.Listings) != 2 {
		t.Fatalf("hidden=all body = %+v, want both", allBody)
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

func TestRunSearchEnqueuesJobs(t *testing.T) {
	e := setupPublic(t)
	seed := testdb.Seed(t, e.pool)

	resp, body := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/searches/"+seed.SearchID+"/run", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["jobs_enqueued"] != float64(1) {
		t.Fatalf("jobs_enqueued = %v", body["jobs_enqueued"])
	}
	var count int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM jobs WHERE search_id = $1`, seed.SearchID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("jobs in db = %d, want 1", count)
	}
}

func TestListingsFiltersByFirstSeenDateRange(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', 'fp1', 'ch1',
		        '2026-10-01', '2026-10-01', '2026-10-01', '{}'),
		       ('facebook_marketplace', '2', 'https://x/2', 'Civic', 'active', 'fp2', 'ch2',
		        '2026-10-10', '2026-10-10', '2026-10-10', '{}')`); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/listings?first_seen_after=2026-10-05&first_seen_before=2026-10-15")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "2" {
		t.Fatalf("body = %+v, want only external_id=2", body)
	}
}

func TestListingsOffsetAndLimitPaginate(t *testing.T) {
	e := setupPublic(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO listings
		(source, external_id, url, title, status, fingerprint, content_hash,
		 first_seen_at, last_seen_at, last_changed_at, raw_data)
		VALUES ('facebook_marketplace', '1', 'https://x/1', 'Civic', 'active', 'fp1', 'ch1', now() - interval '2 hours', now(), now(), '{}'),
		       ('facebook_marketplace', '2', 'https://x/2', 'Civic', 'active', 'fp2', 'ch2', now() - interval '1 hour', now(), now(), '{}'),
		       ('facebook_marketplace', '3', 'https://x/3', 'Civic', 'active', 'fp3', 'ch3', now(), now(), now(), '{}')`); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/listings?limit=2&offset=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Listings []map[string]any `json:"listings"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 3 || len(body.Listings) != 1 || body.Listings[0]["external_id"] != "1" {
		t.Fatalf("body = %+v, want total=3 and the oldest row (external_id=1) as the only row past offset 2", body)
	}
}

func TestRunSearchUnknownIDReturnsNotFound(t *testing.T) {
	e := setupPublic(t)
	resp, _ := doJSON(t, http.MethodPost, e.srv.URL+"/api/v1/searches/00000000-0000-0000-0000-000000000000/run", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
