package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"scrapperbook/backend/internal/catalog"
)

func (h *Public) listSearches(w http.ResponseWriter, r *http.Request) {
	list, err := catalog.ListSearches(r.Context(), h.Pool)
	if err != nil {
		internalErrorPublic(w, h.Log, "list searches", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) createSearch(w http.ResponseWriter, r *http.Request) {
	var s catalog.Search
	if !decodeJSON(w, r, &s) {
		return
	}
	id, err := catalog.CreateSearch(r.Context(), h.Pool, s)
	if errors.Is(err, catalog.ErrValidation) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "create search", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *Public) getSearch(w http.ResponseWriter, r *http.Request) {
	if !isUUID(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	s, err := catalog.GetSearch(r.Context(), h.Pool, r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "get search", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Public) updateSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          *string         `json:"name"`
		Keyword       *string         `json:"keyword"`
		Enabled       *bool           `json:"enabled"`
		Priority      *int16          `json:"priority"`
		FrequencySecs *int            `json:"frequency_seconds"`
		Filters       json.RawMessage `json:"filters"`
		Alert         json.RawMessage `json:"alert"`
		RegionIDs     *[]string       `json:"region_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !isUUID(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	s, err := catalog.UpdateSearch(r.Context(), h.Pool, r.PathValue("id"), catalog.SearchPatch{
		Name: body.Name, Keyword: body.Keyword, Enabled: body.Enabled, Priority: body.Priority,
		FrequencySecs: body.FrequencySecs, Filters: body.Filters, Alert: body.Alert, RegionIDs: body.RegionIDs,
	})
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	if errors.Is(err, catalog.ErrValidation) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "update search", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Public) deleteSearch(w http.ResponseWriter, r *http.Request) {
	if !isUUID(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	err := catalog.DeleteSearch(r.Context(), h.Pool, r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "search not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "delete search", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
