package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"scrapperbook/backend/internal/catalog"
	"scrapperbook/backend/internal/testdb"
)

func TestCreateGetAndListSearches(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	testdb.AddRegion(t, pool, "curitiba-pr", "curitiba")

	id, err := catalog.CreateSearch(ctx, pool, catalog.Search{
		Name: "Civic EG", Keyword: "Honda Civic EG", Enabled: true, Priority: 1,
		FrequencySecs: 1800,
		Filters:       json.RawMessage(`{"max_price": 30000}`),
		Alert:         json.RawMessage(`{"enabled": true, "on_new": true}`),
		RegionIDs:     []string{"curitiba-pr"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected a generated id")
	}

	got, err := catalog.GetSearch(ctx, pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Keyword != "Honda Civic EG" || got.FrequencySecs != 1800 || !got.Enabled {
		t.Fatalf("got = %+v", got)
	}
	if len(got.RegionIDs) != 1 || got.RegionIDs[0] != "curitiba-pr" {
		t.Fatalf("region_ids = %v", got.RegionIDs)
	}
	var filters map[string]any
	if err := json.Unmarshal(got.Filters, &filters); err != nil || filters["max_price"] != float64(30000) {
		t.Fatalf("filters = %s, err = %v", got.Filters, err)
	}

	list, err := catalog.ListSearches(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v", list)
	}
}

func TestCreateSearchRejectsMissingKeywordOrFrequency(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := catalog.CreateSearch(ctx, pool, catalog.Search{Name: "x", FrequencySecs: 60}); err == nil {
		t.Error("missing keyword: expected error")
	}
	if _, err := catalog.CreateSearch(ctx, pool, catalog.Search{Name: "x", Keyword: "k"}); err == nil {
		t.Error("missing frequency: expected error")
	}
}

func TestGetSearchNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, err := catalog.GetSearch(context.Background(), pool, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateSearchPatchesFieldsAndReplacesRegions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	testdb.AddRegion(t, pool, "curitiba-pr", "curitiba")
	testdb.AddRegion(t, pool, "londrina-pr", "londrina")
	id, err := catalog.CreateSearch(ctx, pool, catalog.Search{
		Name: "Civic", Keyword: "Civic", Enabled: true, FrequencySecs: 1800, RegionIDs: []string{"curitiba-pr"},
	})
	if err != nil {
		t.Fatal(err)
	}

	newRegions := []string{"londrina-pr"}
	updated, err := catalog.UpdateSearch(ctx, pool, id, catalog.SearchPatch{
		Keyword: strPtr("Honda Civic"), RegionIDs: &newRegions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Keyword != "Honda Civic" || updated.Name != "Civic" { // name untouched
		t.Errorf("updated = %+v", updated)
	}
	if len(updated.RegionIDs) != 1 || updated.RegionIDs[0] != "londrina-pr" {
		t.Fatalf("region_ids after replace = %v, want only londrina-pr", updated.RegionIDs)
	}
}

func TestUpdateSearchRejectsEmptyKeywordOrNonPositiveFrequency(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	id, err := catalog.CreateSearch(ctx, pool, catalog.Search{
		Name: "Civic", Keyword: "Civic", Enabled: true, FrequencySecs: 1800,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := catalog.UpdateSearch(ctx, pool, id, catalog.SearchPatch{Keyword: strPtr("")}); err == nil {
		t.Error("empty keyword patch: expected error")
	}
	if _, err := catalog.UpdateSearch(ctx, pool, id, catalog.SearchPatch{FrequencySecs: intPtr(0)}); err == nil {
		t.Error("zero frequency patch: expected error")
	}
	if _, err := catalog.UpdateSearch(ctx, pool, id, catalog.SearchPatch{FrequencySecs: intPtr(-5)}); err == nil {
		t.Error("negative frequency patch: expected error")
	}

	// Confirm the rejected patches did not mutate the row.
	got, err := catalog.GetSearch(ctx, pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Keyword != "Civic" || got.FrequencySecs != 1800 {
		t.Fatalf("got = %+v, want unchanged Keyword=Civic FrequencySecs=1800", got)
	}
}

func TestDeleteSearch(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	id, err := catalog.CreateSearch(ctx, pool, catalog.Search{Name: "x", Keyword: "k", FrequencySecs: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.DeleteSearch(ctx, pool, id); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.GetSearch(ctx, pool, id); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err after delete = %v, want ErrNotFound", err)
	}
	if err := catalog.DeleteSearch(ctx, pool, id); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}
