package httpapi

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics registers GET /metrics using promhttp's handler against the
// default Prometheus registry — the same one the metrics package's
// promauto vars register themselves into.
func Metrics(mux *http.ServeMux) {
	mux.Handle("GET /metrics", promhttp.Handler())
}
