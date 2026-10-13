package httpapi

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Health registers liveness (/healthz) and readiness (/readyz, pings Postgres).
func Health(mux *http.ServeMux, pool *pgxpool.Pool) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
