package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/workers"
)

// Internal serves the worker-facing API under /internal/v1.
type Internal struct {
	Token   string
	Pool    *pgxpool.Pool
	Queue   *jobs.Queue
	Workers *workers.Registry
	Ingest  *ingest.Service
	Log     *slog.Logger
}

func (h *Internal) Routes(mux *http.ServeMux) {
	mux.Handle("POST /internal/v1/workers/register", h.auth(h.register))
	mux.Handle("POST /internal/v1/workers/{id}/heartbeat", h.auth(h.heartbeat))
	mux.Handle("POST /internal/v1/jobs/claim", h.auth(h.claim))
	mux.Handle("POST /internal/v1/jobs/{id}/result", h.auth(h.result))
	mux.Handle("POST /internal/v1/jobs/{id}/fail", h.auth(h.fail))
}

func (h *Internal) auth(next http.HandlerFunc) http.Handler {
	want := []byte("Bearer " + h.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if h.Token == "" || subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

func (h *Internal) register(w http.ResponseWriter, r *http.Request) {
	var reg workers.Registration
	if !decodeJSON(w, r, &reg) {
		return
	}
	err := h.Workers.Register(r.Context(), reg)
	switch {
	case errors.Is(err, workers.ErrMissingWorkerID):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		h.internalError(w, "register", err, "worker_id", reg.ID)
	default:
		h.Log.Info("worker registered", "worker_id", reg.ID, "sources", reg.Sources)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Internal) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status       string  `json:"status"`
		CurrentJobID *string `json:"current_job_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.CurrentJobID != nil && !isUUID(*body.CurrentJobID) {
		writeError(w, http.StatusBadRequest, "current_job_id must be a uuid")
		return
	}
	err := h.Workers.Heartbeat(r.Context(), r.PathValue("id"), body.Status, body.CurrentJobID)
	switch {
	case errors.Is(err, workers.ErrUnknownWorker):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, workers.ErrInvalidStatus):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		h.internalError(w, "heartbeat", err, "worker_id", r.PathValue("id"))
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Internal) claim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkerID string   `json:"worker_id"`
		Sources  []string `json:"sources"`
		Types    []string `json:"types"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.WorkerID == "" || len(body.Sources) == 0 || len(body.Types) == 0 {
		writeError(w, http.StatusBadRequest, "worker_id, sources and types are required")
		return
	}
	job, err := h.Queue.Claim(r.Context(), body.WorkerID, body.Sources, body.Types)
	if err != nil {
		h.internalError(w, "claim", err, "worker_id", body.WorkerID)
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	msg := map[string]any{}
	if err := json.Unmarshal(job.Payload, &msg); err != nil {
		h.internalError(w, "decode payload", err, "job_id", job.ID, "worker_id", body.WorkerID)
		return
	}
	// job_id/attempt/type/source/stop_before are reserved: they always
	// overwrite any same-named key already in the stored payload. No current
	// job payload (search jobs carry search/region/limits) collides with
	// these, but a future job type must avoid reusing these names.
	msg["job_id"] = job.ID
	msg["attempt"] = job.Attempt
	msg["type"] = job.Type
	msg["source"] = job.Source
	msg["stop_before"] = job.StopBefore
	h.Log.Info("job claimed", "job_id", job.ID, "worker_id", body.WorkerID, "attempt", job.Attempt)
	writeJSON(w, http.StatusOK, msg)
}

type resultRequest struct {
	WorkerID       string          `json:"worker_id"`
	Attempt        int             `json:"attempt"`
	CrawlerVersion string          `json:"crawler_version"`
	ParserVersion  string          `json:"parser_version"`
	Items          []ingest.Item   `json:"items"`
	Stats          json.RawMessage `json:"stats"`
}

func (h *Internal) result(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	var req resultRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		h.internalError(w, "begin", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	defer tx.Rollback(ctx)

	done, err := h.Queue.Complete(ctx, tx, id, req.Attempt, req.WorkerID)
	if errors.Is(err, jobs.ErrStale) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		h.internalError(w, "complete", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	if done.SearchID == nil || done.RegionID == nil {
		writeError(w, http.StatusUnprocessableEntity, "job has no search context")
		return
	}
	stats, err := h.Ingest.Ingest(ctx, tx, ingest.Batch{
		JobID: id, Source: done.Source, SearchID: *done.SearchID, RegionID: *done.RegionID,
		CrawlerVersion: req.CrawlerVersion, ParserVersion: req.ParserVersion,
		SeenAt: done.FinishedAt, Items: req.Items,
	})
	if err != nil {
		h.internalError(w, "ingest", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	if err := h.Queue.RecordResult(ctx, tx, id, stats.New, stats.Changed, stats.Seen, stats.Invalid, stats.Irrelevant, stats.Alerts); err != nil {
		h.internalError(w, "record result", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.internalError(w, "commit", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	h.Log.Info("job result ingested", "job_id", id, "worker_id", req.WorkerID, "attempt", req.Attempt,
		"new", stats.New, "changed", stats.Changed, "seen", stats.Seen,
		"invalid", stats.Invalid, "irrelevant", stats.Irrelevant, "alerts", stats.Alerts, "crawler_stats", string(req.Stats))
	writeJSON(w, http.StatusOK, stats)
}

func (h *Internal) fail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	var req struct {
		WorkerID  string `json:"worker_id"`
		Attempt   int    `json:"attempt"`
		ErrorKind string `json:"error_kind"`
		Message   string `json:"message"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	kind, ok := jobs.ParseErrorKind(req.ErrorKind)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown error_kind: "+req.ErrorKind)
		return
	}
	out, err := h.Queue.Fail(r.Context(), id, req.Attempt, req.WorkerID, kind, req.Message)
	if errors.Is(err, jobs.ErrStale) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		h.internalError(w, "fail", err, "job_id", id, "worker_id", req.WorkerID)
		return
	}
	h.Log.Warn("job failed", "job_id", id, "worker_id", req.WorkerID, "attempt", req.Attempt,
		"error_kind", kind, "message", req.Message, "outcome", out.Status)
	writeJSON(w, http.StatusOK, out)
}

// internalError logs the failure with whatever identifying context the
// caller has (job_id, worker_id, ...) and tells the client nothing beyond
// "internal error" — the log line is the only debugging trail, so kv should
// carry everything the caller knows.
func (h *Internal) internalError(w http.ResponseWriter, op string, err error, kv ...any) {
	h.Log.Error(op, append([]any{"err", err}, kv...)...)
	writeError(w, http.StatusInternalServerError, "internal error")
}
