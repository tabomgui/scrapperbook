package metrics_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"scrapperbook/backend/internal/metrics"
)

func TestCountersAreIndependentByLabel(t *testing.T) {
	before := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("test-source", "search", "succeeded", ""))
	metrics.JobsTotal.WithLabelValues("test-source", "search", "succeeded", "").Inc()
	after := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("test-source", "search", "succeeded", ""))
	if after-before != 1 {
		t.Fatalf("delta = %v, want 1", after-before)
	}
	// A different error_kind label is a distinct series, untouched by the above.
	other := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("test-source", "search", "dead", "network"))
	if other != 0 {
		t.Fatalf("other label series = %v, want 0 (independent)", other)
	}
}

func TestHistogramAndPlainCounter(t *testing.T) {
	hist, ok := metrics.JobDurationSeconds.WithLabelValues("test-source", "search").(prometheus.Histogram)
	if !ok {
		t.Fatal("JobDurationSeconds observer does not implement prometheus.Histogram")
	}

	var beforeHist, afterHist dto.Metric
	if err := hist.Write(&beforeHist); err != nil {
		t.Fatalf("Write before observe: %v", err)
	}
	metrics.JobDurationSeconds.WithLabelValues("test-source", "search").Observe(1.5)
	if err := hist.Write(&afterHist); err != nil {
		t.Fatalf("Write after observe: %v", err)
	}
	gotDelta := afterHist.GetHistogram().GetSampleCount() - beforeHist.GetHistogram().GetSampleCount()
	if gotDelta != 1 {
		t.Fatalf("histogram sample count delta = %v, want 1", gotDelta)
	}

	before := testutil.ToFloat64(metrics.SnapshotsTotal)
	metrics.SnapshotsTotal.Inc()
	after := testutil.ToFloat64(metrics.SnapshotsTotal)
	if after-before != 1 {
		t.Fatalf("snapshots delta = %v, want 1", after-before)
	}
}
