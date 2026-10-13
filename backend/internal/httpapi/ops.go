package httpapi

import (
	"errors"
	"net/http"

	"scrapperbook/backend/internal/jobs"
)

func (h *Public) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := h.Queue.ListJobs(r.Context(), jobs.JobFilter{Status: q.Get("status"), ErrorKind: q.Get("error_kind")})
	if err != nil {
		internalErrorPublic(w, h.Log, "list jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) listWorkers(w http.ResponseWriter, r *http.Request) {
	list, err := h.Workers.List(r.Context())
	if err != nil {
		internalErrorPublic(w, h.Log, "list workers", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Queue.ListSessions(r.Context())
	if err != nil {
		internalErrorPublic(w, h.Log, "list sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) resumeSession(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	err := h.Queue.ResumeSource(r.Context(), source)
	if errors.Is(err, jobs.ErrUnknownSource) {
		writeError(w, http.StatusNotFound, "unknown source")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "resume session", err)
		return
	}
	h.Log.Info("session resumed", "source", source)
	w.WriteHeader(http.StatusNoContent)
}
