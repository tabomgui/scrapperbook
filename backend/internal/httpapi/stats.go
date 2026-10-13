package httpapi

import "net/http"

type statsResponse struct {
	TotalListings   int            `json:"total_listings"`
	ListingsByState map[string]int `json:"listings_by_state"`
	EnabledSearches int            `json:"enabled_searches"`
	JobsLast24h     map[string]int `json:"jobs_last_24h"`
	WorkersByStatus map[string]int `json:"workers_by_status"`
}

func (h *Public) stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := statsResponse{
		ListingsByState: map[string]int{}, JobsLast24h: map[string]int{}, WorkersByStatus: map[string]int{},
	}

	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM listings`).Scan(&resp.TotalListings); err != nil {
		internalErrorPublic(w, h.Log, "stats total listings", err)
		return
	}

	rows, err := h.Pool.Query(ctx, `SELECT COALESCE(state, ''), count(*) FROM listings GROUP BY state`)
	if err != nil {
		internalErrorPublic(w, h.Log, "stats listings by state", err)
		return
	}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			rows.Close()
			internalErrorPublic(w, h.Log, "stats listings by state scan", err)
			return
		}
		if state != "" {
			resp.ListingsByState[state] = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internalErrorPublic(w, h.Log, "stats listings by state iterate", err)
		return
	}

	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM search_definitions WHERE enabled`).Scan(&resp.EnabledSearches); err != nil {
		internalErrorPublic(w, h.Log, "stats enabled searches", err)
		return
	}

	jobRows, err := h.Pool.Query(ctx,
		`SELECT status, count(*) FROM jobs WHERE created_at > now() - interval '24 hours' GROUP BY status`)
	if err != nil {
		internalErrorPublic(w, h.Log, "stats jobs", err)
		return
	}
	for jobRows.Next() {
		var status string
		var n int
		if err := jobRows.Scan(&status, &n); err != nil {
			jobRows.Close()
			internalErrorPublic(w, h.Log, "stats jobs scan", err)
			return
		}
		resp.JobsLast24h[status] = n
	}
	jobRows.Close()
	if err := jobRows.Err(); err != nil {
		internalErrorPublic(w, h.Log, "stats jobs iterate", err)
		return
	}

	workerRows, err := h.Pool.Query(ctx, `SELECT status, count(*) FROM workers GROUP BY status`)
	if err != nil {
		internalErrorPublic(w, h.Log, "stats workers", err)
		return
	}
	for workerRows.Next() {
		var status string
		var n int
		if err := workerRows.Scan(&status, &n); err != nil {
			workerRows.Close()
			internalErrorPublic(w, h.Log, "stats workers scan", err)
			return
		}
		resp.WorkersByStatus[status] = n
	}
	workerRows.Close()
	if err := workerRows.Err(); err != nil {
		internalErrorPublic(w, h.Log, "stats workers iterate", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
