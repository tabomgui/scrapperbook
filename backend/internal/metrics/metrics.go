// Package metrics exposes Prometheus counters and histograms describing
// pipeline activity. These are package-level vars, not fields on a struct
// passed around — the idiomatic promauto pattern, and it means callers
// anywhere in the backend can record a metric with one line, with no
// constructor or signature changes to the code being instrumented.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// JobsTotal counts every terminal job outcome. error_kind is empty for
	// a successful job.
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_total",
		Help: "Jobs processed, by source, type, status and error_kind.",
	}, []string{"source", "type", "status", "error_kind"})

	// JobDurationSeconds is observed only on success — a failed job's
	// "duration" isn't a meaningful latency signal the way a completed
	// crawl's is.
	JobDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "job_duration_seconds",
		Help: "Successful job duration in seconds, by source and type.",
	}, []string{"source", "type"})

	// ListingsIngestedTotal's result label is one of new|changed|seen|invalid|irrelevant.
	ListingsIngestedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "listings_ingested_total",
		Help: "Listing items processed by ingest, by source and result.",
	}, []string{"source", "result"})

	// SnapshotsTotal counts listing_snapshots rows written.
	SnapshotsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "snapshots_total",
		Help: "listing_snapshots rows written.",
	})

	// NotificationsTotal's status label is one of sent|failed.
	NotificationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "notifications_total",
		Help: "Notifications processed by the outbox drain, by kind and status.",
	}, []string{"kind", "status"})
)
