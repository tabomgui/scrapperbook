// Package catalog owns read/write access to the data a human manages:
// search definitions, regions, and (read-only here) the listings they
// produce. This is distinct from jobs, which only owns the queue.
package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by any Get/Update/Delete for a missing row.
var ErrNotFound = errors.New("not found")

// ErrValidation wraps the error returned by the explicit pre-flight
// validation checks in Create/Update, so callers can distinguish "bad
// input" from infra failures (DB errors, timeouts, ...) that happen to
// return an error from the same function.
var ErrValidation = errors.New("validation failed")

type Region struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // city | point | state
	City       *string  `json:"city,omitempty"`
	State      *string  `json:"state,omitempty"`
	Lat        *float64 `json:"lat,omitempty"`
	Lng        *float64 `json:"lng,omitempty"`
	RadiusKM   *int     `json:"radius_km,omitempty"`
	FBLocation *string  `json:"fb_location,omitempty"`
	Enabled    bool     `json:"enabled"`
}

func CreateRegion(ctx context.Context, pool *pgxpool.Pool, r Region) error {
	if r.ID == "" || r.Name == "" || r.Kind == "" {
		return fmt.Errorf("%w: id, name and kind are required", ErrValidation)
	}
	_, err := pool.Exec(ctx, `INSERT INTO search_regions
		(id, name, kind, city, state, lat, lng, radius_km, fb_location, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		r.ID, r.Name, r.Kind, r.City, r.State, r.Lat, r.Lng, r.RadiusKM, r.FBLocation, r.Enabled)
	if err != nil {
		return fmt.Errorf("create region %s: %w", r.ID, err)
	}
	return nil
}

func GetRegion(ctx context.Context, pool *pgxpool.Pool, id string) (Region, error) {
	var r Region
	err := pool.QueryRow(ctx, `SELECT id, name, kind, city, state, lat, lng, radius_km, fb_location, enabled
		FROM search_regions WHERE id = $1`, id).Scan(
		&r.ID, &r.Name, &r.Kind, &r.City, &r.State, &r.Lat, &r.Lng, &r.RadiusKM, &r.FBLocation, &r.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Region{}, ErrNotFound
	}
	if err != nil {
		return Region{}, fmt.Errorf("get region %s: %w", id, err)
	}
	return r, nil
}

func ListRegions(ctx context.Context, pool *pgxpool.Pool) ([]Region, error) {
	rows, err := pool.Query(ctx, `SELECT id, name, kind, city, state, lat, lng, radius_km, fb_location, enabled
		FROM search_regions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	defer rows.Close()
	var out []Region
	for rows.Next() {
		var r Region
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.City, &r.State, &r.Lat, &r.Lng,
			&r.RadiusKM, &r.FBLocation, &r.Enabled); err != nil {
			return nil, fmt.Errorf("scan region: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegionPatch's fields are nil-means-unchanged — a PATCH, not a PUT.
type RegionPatch struct {
	Name       *string
	Kind       *string
	City       *string
	State      *string
	Lat        *float64
	Lng        *float64
	RadiusKM   *int
	FBLocation *string
	Enabled    *bool
}

func UpdateRegion(ctx context.Context, pool *pgxpool.Pool, id string, p RegionPatch) (Region, error) {
	tag, err := pool.Exec(ctx, `UPDATE search_regions SET
		name = COALESCE($2, name), kind = COALESCE($3, kind), city = COALESCE($4, city),
		state = COALESCE($5, state), lat = COALESCE($6, lat), lng = COALESCE($7, lng),
		radius_km = COALESCE($8, radius_km), fb_location = COALESCE($9, fb_location),
		enabled = COALESCE($10, enabled)
		WHERE id = $1`,
		id, p.Name, p.Kind, p.City, p.State, p.Lat, p.Lng, p.RadiusKM, p.FBLocation, p.Enabled)
	if err != nil {
		return Region{}, fmt.Errorf("update region %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return Region{}, ErrNotFound
	}
	return GetRegion(ctx, pool, id)
}
