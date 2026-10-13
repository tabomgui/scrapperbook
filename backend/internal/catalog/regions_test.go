package catalog_test

import (
	"context"
	"errors"
	"testing"

	"scrapperbook/backend/internal/catalog"
	"scrapperbook/backend/internal/testdb"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }
func strPtr(v string) *string     { return &v }
func boolPtr(v bool) *bool        { return &v }

func TestCreateGetAndListRegions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	r := catalog.Region{
		ID: "curitiba-pr", Name: "Curitiba/PR", Kind: "city",
		City: strPtr("Curitiba"), State: strPtr("PR"),
		Lat: floatPtr(-25.4284), Lng: floatPtr(-49.2733), RadiusKM: intPtr(60),
		FBLocation: strPtr("curitiba"), Enabled: true,
	}
	if err := catalog.CreateRegion(ctx, pool, r); err != nil {
		t.Fatal(err)
	}

	got, err := catalog.GetRegion(ctx, pool, "curitiba-pr")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Curitiba/PR" || *got.FBLocation != "curitiba" || !got.Enabled {
		t.Fatalf("got = %+v", got)
	}

	list, err := catalog.ListRegions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "curitiba-pr" {
		t.Fatalf("list = %+v", list)
	}
}

func TestGetRegionNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, err := catalog.GetRegion(context.Background(), pool, "nowhere")
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateRegionPatchesOnlyGivenFields(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := catalog.CreateRegion(ctx, pool, catalog.Region{
		ID: "londrina-pr", Name: "Londrina/PR", Kind: "city", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	updated, err := catalog.UpdateRegion(ctx, pool, "londrina-pr", catalog.RegionPatch{
		FBLocation: strPtr("londrina"), Enabled: boolPtr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Londrina/PR" { // untouched
		t.Errorf("name = %q, want unchanged", updated.Name)
	}
	if updated.FBLocation == nil || *updated.FBLocation != "londrina" || updated.Enabled {
		t.Fatalf("updated = %+v", updated)
	}
}

func TestUpdateRegionNotFound(t *testing.T) {
	pool := testdb.New(t)
	_, err := catalog.UpdateRegion(context.Background(), pool, "nowhere", catalog.RegionPatch{Enabled: boolPtr(true)})
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
