package jobs_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/metrics"
	"scrapperbook/backend/internal/testdb"
)

func TestCompleteIncrementsJobsAndDurationMetrics(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	before := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "succeeded", ""))

	hist, ok := metrics.JobDurationSeconds.WithLabelValues(j.Source, "search").(prometheus.Histogram)
	if !ok {
		t.Fatal("JobDurationSeconds observer does not implement prometheus.Histogram")
	}
	var beforeHist, afterHist dto.Metric
	if err := hist.Write(&beforeHist); err != nil {
		t.Fatalf("Write before complete: %v", err)
	}

	if _, err := complete(t, pool, q, j, 1); err != nil {
		t.Fatal(err)
	}

	if delta := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "succeeded", "")) - before; delta != 1 {
		t.Fatalf("jobs_total delta = %v, want 1", delta)
	}

	if err := hist.Write(&afterHist); err != nil {
		t.Fatalf("Write after complete: %v", err)
	}
	if gotDelta := afterHist.GetHistogram().GetSampleCount() - beforeHist.GetHistogram().GetSampleCount(); gotDelta != 1 {
		t.Fatalf("job_duration_seconds sample count delta = %v, want 1", gotDelta)
	}
}

func TestFailIncrementsJobsMetricWithErrorKind(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Seed(t, pool)
	q, _ := newQueue(t, pool, openLimits())
	enqueue(t, q, pool, s.SearchID, s.RegionID, 0)
	j := claim(t, q)

	before := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "queued", "network"))
	if _, err := q.Fail(context.Background(), j.ID, j.Attempt, "w1", jobs.KindNetwork, "boom"); err != nil {
		t.Fatal(err)
	}
	if delta := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("facebook_marketplace", "search", "queued", "network")) - before; delta != 1 {
		t.Fatalf("jobs_total delta = %v, want 1", delta)
	}
}
