// Package search holds search definition rules.
package search

import (
	"encoding/json"
	"fmt"
	"strings"

	"scrapperbook/backend/internal/listing"
)

// AlertRule is stored as JSON in search_definitions.alert.
type AlertRule struct {
	Enabled     bool     `json:"enabled"`
	MaxPrice    *int64   `json:"max_price"` // whole currency units (reais), not cents
	States      []string `json:"states"`
	OnNew       bool     `json:"on_new"`
	OnPriceDrop bool     `json:"on_price_drop"`
	MinDropPct  float64  `json:"min_drop_pct"`
}

func ParseAlertRule(raw []byte) (AlertRule, error) {
	var r AlertRule
	if len(raw) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return AlertRule{}, fmt.Errorf("parse alert rule: %w", err)
	}
	return r, nil
}

// Matches reports whether the listing passes the rule's filters.
func (r AlertRule) Matches(c listing.Canonical) bool {
	if !r.Enabled {
		return false
	}
	if r.MaxPrice != nil && (c.PriceCents == nil || *c.PriceCents > *r.MaxPrice*100) {
		return false
	}
	if len(r.States) > 0 {
		for _, s := range r.States {
			if strings.EqualFold(s, c.State) {
				return true
			}
		}
		return false
	}
	return true
}

func (r AlertRule) WantsNew(c listing.Canonical) bool {
	return r.OnNew && r.Matches(c)
}

// WantsPriceDrop reports whether a price fall from oldCents to newCents
// clears the rule's MinDropPct threshold. newCents must be the listing's
// current (non-nil) PriceCents — c is used only for Matches' other filters
// (max price, states), not to re-derive the new price.
func (r AlertRule) WantsPriceDrop(oldCents, newCents int64, c listing.Canonical) bool {
	if !r.OnPriceDrop || oldCents <= 0 || newCents >= oldCents {
		return false
	}
	// Cross-multiply instead of dividing first: dropCents*100/oldCents >= MinDropPct
	// becomes dropCents*100 >= MinDropPct*oldCents. This avoids floating-point
	// rounding at exact percentage boundaries, where dividing first can round
	// just below the threshold even when the true percentage equals it exactly.
	dropCents := oldCents - newCents
	return float64(dropCents)*100 >= r.MinDropPct*float64(oldCents) && r.Matches(c)
}
