// Public serves the human-facing API under /api/v1 — read/write for
// searches and regions, read-only for listings/jobs/workers/sessions, plus
// aggregate stats. No auth: bound to 127.0.0.1 only (see compose.yaml),
// consistent with this being a single-user tool.
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/workers"
)

type Public struct {
	Pool    *pgxpool.Pool
	Queue   *jobs.Queue
	Workers *workers.Registry
	Log     *slog.Logger
}

func (h *Public) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/searches", h.listSearches)
	mux.HandleFunc("POST /api/v1/searches", h.createSearch)
	mux.HandleFunc("GET /api/v1/searches/{id}", h.getSearch)
	mux.HandleFunc("PATCH /api/v1/searches/{id}", h.updateSearch)
	mux.HandleFunc("DELETE /api/v1/searches/{id}", h.deleteSearch)

	mux.HandleFunc("GET /api/v1/regions", h.listRegions)
	mux.HandleFunc("POST /api/v1/regions", h.createRegion)
	mux.HandleFunc("PATCH /api/v1/regions/{id}", h.updateRegion)

	mux.HandleFunc("GET /api/v1/listings", h.listListings)
	mux.HandleFunc("GET /api/v1/listings/{id}", h.getListing)

	mux.HandleFunc("GET /api/v1/jobs", h.listJobs)
	mux.HandleFunc("GET /api/v1/workers", h.listWorkers)
	mux.HandleFunc("GET /api/v1/sessions", h.listSessions)
	mux.HandleFunc("POST /api/v1/sessions/{source}/resume", h.resumeSession)

	mux.HandleFunc("GET /api/v1/stats", h.stats)
}

func internalErrorPublic(w http.ResponseWriter, log *slog.Logger, op string, err error) {
	log.Error(op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
