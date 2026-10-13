package listing_test

import (
	"testing"

	"scrapperbook/backend/internal/listing"
)

func price(v int64) *int64 { return &v }

func TestNormalizeText(t *testing.T) {
	cases := map[string]string{
		"  Honda  CIVIC – EG! São ": "honda civic eg sao",
		"Câmbio Manual/1993":        "cambio manual 1993",
		"":                          "",
	}
	for in, want := range cases {
		if got := listing.NormalizeText(in); got != want {
			t.Errorf("NormalizeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFingerprintIgnoresCaseAccentsAndPunctuation(t *testing.T) {
	a := listing.Canonical{Title: "Honda Civic EG São Paulo", PriceCents: price(2350000),
		SellerExternalID: "555", City: "Curitiba", State: "PR"}
	b := listing.Canonical{Title: "honda civic eg sao paulo!", PriceCents: price(2350000),
		SellerExternalID: "555", City: "curitiba", State: "pr"}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("fingerprints differ for equivalent listings")
	}
}

func TestContentHashChangesWithPrice(t *testing.T) {
	a := listing.Canonical{Title: "Civic", PriceCents: price(2350000), Status: listing.StatusActive}
	b := a
	b.PriceCents = price(2200000)
	if a.ContentHash() == b.ContentHash() {
		t.Fatal("content hash did not change with price")
	}
}

func TestContentHashDistinguishesMissingPriceFromZero(t *testing.T) {
	a := listing.Canonical{Title: "Civic", Status: listing.StatusActive}
	b := a
	b.PriceCents = price(0)
	if a.ContentHash() == b.ContentHash() {
		t.Fatal("nil price and zero price produce the same hash")
	}
}
