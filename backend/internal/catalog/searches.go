package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Search struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Keyword       string          `json:"keyword"`
	Enabled       bool            `json:"enabled"`
	Priority      int16           `json:"priority"`
	FrequencySecs int             `json:"frequency_seconds"`
	Filters       json.RawMessage `json:"filters"`
	Alert         json.RawMessage `json:"alert"`
	RegionIDs     []string        `json:"region_ids"`
}

func CreateSearch(ctx context.Context, pool *pgxpool.Pool, s Search) (string, error) {
	if s.Keyword == "" {
		return "", fmt.Errorf("%w: keyword is required", ErrValidation)
	}
	if s.FrequencySecs <= 0 {
		return "", fmt.Errorf("%w: frequency_seconds must be positive", ErrValidation)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `INSERT INTO search_definitions
		(name, keyword, enabled, priority, frequency, filters, alert)
		VALUES ($1, $2, $3, $4, make_interval(secs => $5), $6, $7)
		RETURNING id`,
		s.Name, s.Keyword, s.Enabled, s.Priority, s.FrequencySecs, orEmptyJSON(s.Filters), orEmptyJSON(s.Alert)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create search: %w", err)
	}
	if err := linkRegions(ctx, tx, id, s.RegionIDs); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func GetSearch(ctx context.Context, pool *pgxpool.Pool, id string) (Search, error) {
	var s Search
	var freqSecs float64
	err := pool.QueryRow(ctx, `SELECT sd.id, sd.name, sd.keyword, sd.enabled, sd.priority,
		       EXTRACT(EPOCH FROM sd.frequency), sd.filters, sd.alert,
		       COALESCE(array_agg(sdr.region_id) FILTER (WHERE sdr.region_id IS NOT NULL), '{}')
		FROM search_definitions sd
		LEFT JOIN search_definition_regions sdr ON sdr.search_id = sd.id
		WHERE sd.id = $1
		GROUP BY sd.id`, id).Scan(
		&s.ID, &s.Name, &s.Keyword, &s.Enabled, &s.Priority, &freqSecs, &s.Filters, &s.Alert, &s.RegionIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return Search{}, ErrNotFound
	}
	if err != nil {
		return Search{}, fmt.Errorf("get search %s: %w", id, err)
	}
	s.FrequencySecs = int(freqSecs)
	return s, nil
}

func ListSearches(ctx context.Context, pool *pgxpool.Pool) ([]Search, error) {
	rows, err := pool.Query(ctx, `SELECT sd.id, sd.name, sd.keyword, sd.enabled, sd.priority,
		       EXTRACT(EPOCH FROM sd.frequency), sd.filters, sd.alert,
		       COALESCE(array_agg(sdr.region_id) FILTER (WHERE sdr.region_id IS NOT NULL), '{}')
		FROM search_definitions sd
		LEFT JOIN search_definition_regions sdr ON sdr.search_id = sd.id
		GROUP BY sd.id
		ORDER BY sd.created_at`)
	if err != nil {
		return nil, fmt.Errorf("list searches: %w", err)
	}
	defer rows.Close()
	var out []Search
	for rows.Next() {
		var s Search
		var freqSecs float64
		if err := rows.Scan(&s.ID, &s.Name, &s.Keyword, &s.Enabled, &s.Priority,
			&freqSecs, &s.Filters, &s.Alert, &s.RegionIDs); err != nil {
			return nil, fmt.Errorf("scan search: %w", err)
		}
		s.FrequencySecs = int(freqSecs)
		out = append(out, s)
	}
	return out, rows.Err()
}

// SearchPatch's fields are nil-means-unchanged. RegionIDs is a pointer to a
// slice: nil means unchanged, a non-nil slice (even empty) fully replaces
// the current set — this is a PATCH of the whole relationship, not an
// add/remove delta, kept simple on purpose.
type SearchPatch struct {
	Name          *string
	Keyword       *string
	Enabled       *bool
	Priority      *int16
	FrequencySecs *int
	Filters       json.RawMessage
	Alert         json.RawMessage
	RegionIDs     *[]string
}

func UpdateSearch(ctx context.Context, pool *pgxpool.Pool, id string, p SearchPatch) (Search, error) {
	if p.Keyword != nil && *p.Keyword == "" {
		return Search{}, fmt.Errorf("%w: keyword is required", ErrValidation)
	}
	if p.FrequencySecs != nil && *p.FrequencySecs <= 0 {
		return Search{}, fmt.Errorf("%w: frequency_seconds must be positive", ErrValidation)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Search{}, err
	}
	defer tx.Rollback(ctx)

	var freqInterval any
	if p.FrequencySecs != nil {
		freqInterval = fmt.Sprintf("%d seconds", *p.FrequencySecs)
	}
	tag, err := tx.Exec(ctx, `UPDATE search_definitions SET
		name = COALESCE($2, name), keyword = COALESCE($3, keyword), enabled = COALESCE($4, enabled),
		priority = COALESCE($5, priority), frequency = COALESCE($6::interval, frequency),
		filters = COALESCE($7, filters), alert = COALESCE($8, alert), updated_at = now()
		WHERE id = $1`,
		id, p.Name, p.Keyword, p.Enabled, p.Priority, freqInterval, nullableJSON(p.Filters), nullableJSON(p.Alert))
	if err != nil {
		return Search{}, fmt.Errorf("update search %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return Search{}, ErrNotFound
	}
	if p.RegionIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM search_definition_regions WHERE search_id = $1`, id); err != nil {
			return Search{}, fmt.Errorf("clear regions for %s: %w", id, err)
		}
		if err := linkRegions(ctx, tx, id, *p.RegionIDs); err != nil {
			return Search{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Search{}, err
	}
	return GetSearch(ctx, pool, id)
}

func DeleteSearch(ctx context.Context, pool *pgxpool.Pool, id string) error {
	tag, err := pool.Exec(ctx, `DELETE FROM search_definitions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete search %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func linkRegions(ctx context.Context, tx pgx.Tx, searchID string, regionIDs []string) error {
	for _, rid := range regionIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO search_definition_regions (search_id, region_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, searchID, rid); err != nil {
			return fmt.Errorf("link region %s: %w", rid, err)
		}
	}
	return nil
}

func orEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
