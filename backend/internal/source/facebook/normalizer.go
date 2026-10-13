// Package facebook maps Facebook Marketplace raw listing nodes to the canonical model.
package facebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"scrapperbook/backend/internal/listing"
)

const (
	Source        = "facebook_marketplace"
	itemURLPrefix = "https://www.facebook.com/marketplace/item/"
	// Marketplace searches here are Brazil-only; the node rarely carries a currency.
	defaultCurrency = "BRL"
)

type node struct {
	ID    string `json:"id"`
	Title string `json:"marketplace_listing_title"`
	Price *struct {
		Amount   json.Number `json:"amount"`
		Currency string      `json:"currency"`
	} `json:"listing_price"`
	Photo *struct {
		Image struct {
			URI string `json:"uri"`
		} `json:"image"`
	} `json:"primary_listing_photo"`
	Location *struct {
		ReverseGeocode struct {
			City     string `json:"city"`
			State    string `json:"state"`
			CityPage struct {
				DisplayName string `json:"display_name"`
			} `json:"city_page"`
		} `json:"reverse_geocode"`
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	} `json:"location"`
	Seller *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"marketplace_listing_seller"`
	Description *struct {
		Text string `json:"text"`
	} `json:"redacted_description"`
	IsSold    bool `json:"is_sold"`
	IsPending bool `json:"is_pending"`
}

type Normalizer struct{}

func (Normalizer) Source() string { return Source }

func (Normalizer) Normalize(externalID string, raw json.RawMessage) (listing.Canonical, error) {
	var n node
	if err := json.Unmarshal(raw, &n); err != nil {
		return listing.Canonical{}, fmt.Errorf("decode facebook node: %w", err)
	}
	if externalID == "" {
		return listing.Canonical{}, errors.New("empty external_id")
	}
	if n.ID != "" && n.ID != externalID {
		return listing.Canonical{}, fmt.Errorf("external_id %q does not match node id %q", externalID, n.ID)
	}
	title := strings.TrimSpace(n.Title)
	if title == "" {
		return listing.Canonical{}, fmt.Errorf("facebook normalize %s: missing title", externalID)
	}

	c := listing.Canonical{
		Source:     Source,
		ExternalID: externalID,
		URL:        itemURLPrefix + externalID + "/",
		Title:      title,
		Currency:   defaultCurrency,
		Status:     listing.StatusActive,
	}
	if n.Price != nil && n.Price.Amount != "" {
		cents, err := amountToCents(n.Price.Amount.String())
		if err != nil {
			return listing.Canonical{}, fmt.Errorf("facebook normalize %s: %w", externalID, err)
		}
		c.PriceCents = &cents
		if n.Price.Currency != "" {
			c.Currency = n.Price.Currency
		}
	}
	if n.Photo != nil {
		c.ImageURL = n.Photo.Image.URI
	}
	if n.Location != nil {
		g := n.Location.ReverseGeocode
		c.City, c.State = g.City, g.State
		c.LocationText = g.CityPage.DisplayName
		if c.LocationText == "" && c.City != "" {
			c.LocationText = strings.TrimSuffix(c.City+", "+c.State, ", ")
		}
		c.Lat, c.Lng = n.Location.Latitude, n.Location.Longitude
	}
	if n.Seller != nil {
		c.SellerExternalID, c.SellerName = n.Seller.ID, n.Seller.Name
	}
	if n.Description != nil {
		c.Description = n.Description.Text
	}
	switch {
	case n.IsSold:
		c.Status = listing.StatusSold
	case n.IsPending:
		c.Status = listing.StatusPending
	}
	return c, nil
}

// amountToCents parses "23500", "23500.5" or "23500.00" without floats.
func amountToCents(s string) (int64, error) {
	if strings.ContainsAny(s, "eE-") {
		return 0, fmt.Errorf("unsupported amount %q", s)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("amount %q: more than 2 decimal digits", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", s, err)
	}
	// No real listing price gets anywhere near this; it's a sanity bound against
	// corrupted input overflowing int64 once multiplied by 100 below.
	const maxWhole = 1_000_000_000_000
	if w < 0 || w > maxWhole {
		return 0, fmt.Errorf("amount %q: out of range", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", s, err)
	}
	return w*100 + f, nil
}
