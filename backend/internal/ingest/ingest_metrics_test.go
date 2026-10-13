package ingest_test

import (
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/metrics"
)

func TestIngestIncrementsListingsAndSnapshotMetrics(t *testing.T) {
	e := setup(t)
	beforeNew := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "new"))
	beforeSnap := testutil.ToFloat64(metrics.SnapshotsTotal)

	e.run(t, seenAt, node("1", "Honda Civic EG 1993", "23500"))

	if delta := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "new")) - beforeNew; delta != 1 {
		t.Fatalf("new metric delta = %v, want 1", delta)
	}
	if delta := testutil.ToFloat64(metrics.SnapshotsTotal) - beforeSnap; delta != 1 {
		t.Fatalf("snapshots metric delta = %v, want 1", delta)
	}
}

func TestIngestIncrementsInvalidMetric(t *testing.T) {
	e := setup(t)
	before := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "invalid"))

	bad := ingest.Item{ExternalID: "x", Raw: json.RawMessage(`{`)}
	e.run(t, seenAt, bad)

	if delta := testutil.ToFloat64(metrics.ListingsIngestedTotal.WithLabelValues("facebook_marketplace", "invalid")) - before; delta != 1 {
		t.Fatalf("invalid metric delta = %v, want 1", delta)
	}
}
