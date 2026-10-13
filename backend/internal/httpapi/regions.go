package httpapi

import (
	"errors"
	"net/http"

	"scrapperbook/backend/internal/catalog"
)

func (h *Public) listRegions(w http.ResponseWriter, r *http.Request) {
	list, err := catalog.ListRegions(r.Context(), h.Pool)
	if err != nil {
		internalErrorPublic(w, h.Log, "list regions", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Public) createRegion(w http.ResponseWriter, r *http.Request) {
	var reg catalog.Region
	if !decodeJSON(w, r, &reg) {
		return
	}
	err := catalog.CreateRegion(r.Context(), h.Pool, reg)
	if errors.Is(err, catalog.ErrValidation) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "create region", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Public) updateRegion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       *string  `json:"name"`
		Kind       *string  `json:"kind"`
		City       *string  `json:"city"`
		State      *string  `json:"state"`
		Lat        *float64 `json:"lat"`
		Lng        *float64 `json:"lng"`
		RadiusKM   *int     `json:"radius_km"`
		FBLocation *string  `json:"fb_location"`
		Enabled    *bool    `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	reg, err := catalog.UpdateRegion(r.Context(), h.Pool, r.PathValue("id"), catalog.RegionPatch{
		Name: body.Name, Kind: body.Kind, City: body.City, State: body.State,
		Lat: body.Lat, Lng: body.Lng, RadiusKM: body.RadiusKM, FBLocation: body.FBLocation, Enabled: body.Enabled,
	})
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "region not found")
		return
	}
	if errors.Is(err, catalog.ErrValidation) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		internalErrorPublic(w, h.Log, "update region", err)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}
