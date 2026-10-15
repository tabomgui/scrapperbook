package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"scrapperbook/backend/internal/catalog"
)

func (h *Public) listListings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListingFilter{Sort: q.Get("sort")}
	if v := q.Get("search_id"); v != "" {
		f.SearchID = &v
	}
	if v := q.Get("state"); v != "" {
		f.State = &v
	}
	if v := q.Get("city"); v != "" {
		f.City = &v
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
	switch q.Get("hidden") {
	case "true":
		v := true
		f.Hidden = &v
	case "all":
		f.Hidden = nil
	default:
		v := false
		f.Hidden = &v
	}
	if v := q.Get("min_price"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.MinPrice = &n
		}
	}
	if v := q.Get("max_price"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.MaxPrice = &n
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	list, err := catalog.ListListings(r.Context(), h.Pool, f)
	if err != nil {
		internalErrorPublic(w, h.Log, "list listings", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) getListing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown listing")
		return
	}
	listing, snaps, err := catalog.GetListing(r.Context(), h.Pool, id)
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "listing not found")
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "get listing", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listing": listing, "snapshots": snaps})
}

func (h *Public) hideListing(w http.ResponseWriter, r *http.Request) {
	h.setListingHidden(w, r, true)
}

func (h *Public) unhideListing(w http.ResponseWriter, r *http.Request) {
	h.setListingHidden(w, r, false)
}

func (h *Public) setListingHidden(w http.ResponseWriter, r *http.Request, hidden bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown listing")
		return
	}
	if err := catalog.SetListingHidden(r.Context(), h.Pool, id, hidden); err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			writeError(w, http.StatusNotFound, "listing not found")
			return
		}
		internalErrorPublic(w, h.Log, "set listing hidden", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
