package facebook_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"scrapperbook/backend/internal/listing"
	"scrapperbook/backend/internal/source/facebook"
)

func fixture(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/listing_node.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// This fixture is a real, unredacted Facebook Marketplace capture (plan 2,
// Task 13) — not synthetic. It includes a real third party's name, ID and
// photo URL; keep that in mind before copying it elsewhere.
func TestNormalizeFullNode(t *testing.T) {
	c, err := facebook.Normalizer{}.Normalize("29168236012774043", fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "facebook_marketplace" {
		t.Errorf("source = %q", c.Source)
	}
	if c.URL != "https://www.facebook.com/marketplace/item/29168236012774043/" {
		t.Errorf("url = %q", c.URL)
	}
	if c.Title != "2000 Honda Civic" {
		t.Errorf("title = %q", c.Title)
	}
	if c.PriceCents == nil || *c.PriceCents != 2600000 {
		t.Errorf("price = %v", c.PriceCents)
	}
	if c.Currency != "BRL" || c.City != "Itaquaquecetuba" || c.State != "SP" {
		t.Errorf("currency/city/state = %q/%q/%q", c.Currency, c.City, c.State)
	}
	if c.LocationText != "Itaquaquecetuba" {
		t.Errorf("location_text = %q", c.LocationText)
	}
	if !strings.HasPrefix(c.ImageURL, "https://scontent.fldb4-1.fna.fbcdn.net/") {
		t.Errorf("image = %q", c.ImageURL)
	}
	if c.SellerExternalID != "100000901394133" || c.SellerName != "Wagner Moreira" {
		t.Errorf("seller = %q/%q", c.SellerExternalID, c.SellerName)
	}
	if c.Status != listing.StatusActive {
		t.Errorf("status = %q", c.Status)
	}
}

func TestNormalizeStatusAndNumericPrice(t *testing.T) {
	raw := json.RawMessage(`{"id":"1","marketplace_listing_title":"Civic",
		"listing_price":{"amount":19900},"is_sold":true}`)
	c, err := facebook.Normalizer{}.Normalize("1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != listing.StatusSold {
		t.Errorf("status = %q, want sold", c.Status)
	}
	if c.PriceCents == nil || *c.PriceCents != 1990000 {
		t.Errorf("price = %v, want 1990000", c.PriceCents)
	}
	if c.Currency != "BRL" {
		t.Errorf("currency default = %q, want BRL", c.Currency)
	}
}

func TestNormalizePendingWithoutPrice(t *testing.T) {
	raw := json.RawMessage(`{"id":"2","marketplace_listing_title":"Civic","is_pending":true}`)
	c, err := facebook.Normalizer{}.Normalize("2", raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != listing.StatusPending {
		t.Errorf("status = %q, want pending", c.Status)
	}
	if c.PriceCents != nil {
		t.Errorf("price = %v, want nil", *c.PriceCents)
	}
}

func TestNormalizeRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		id  string
		raw string
	}{
		"id mismatch":    {"9", `{"id":"1","marketplace_listing_title":"Civic"}`},
		"missing title":  {"1", `{"id":"1"}`},
		"bad json":       {"1", `{`},
		"exponent price": {"1", `{"id":"1","marketplace_listing_title":"C","listing_price":{"amount":"2e4"}}`},
	}
	for name, tc := range cases {
		if _, err := (facebook.Normalizer{}).Normalize(tc.id, json.RawMessage(tc.raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
