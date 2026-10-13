package search_test

import (
	"testing"

	"scrapperbook/backend/internal/listing"
	"scrapperbook/backend/internal/search"
)

func cents(v int64) *int64 { return &v }

func TestParseAlertRule(t *testing.T) {
	r, err := search.ParseAlertRule([]byte(
		`{"enabled":true,"max_price":30000,"states":["PR","SC"],"on_new":true,"min_drop_pct":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Enabled || !r.OnNew || r.OnPriceDrop || *r.MaxPrice != 30000 || len(r.States) != 2 || r.MinDropPct != 5 {
		t.Fatalf("unexpected rule %+v", r)
	}
	empty, err := search.ParseAlertRule(nil)
	if err != nil || empty.Enabled {
		t.Fatalf("empty rule = %+v, %v", empty, err)
	}
}

func TestMatches(t *testing.T) {
	max := int64(30000)
	rule := search.AlertRule{Enabled: true, MaxPrice: &max, States: []string{"PR"}}
	cases := []struct {
		name string
		c    listing.Canonical
		want bool
	}{
		{"under max in PR", listing.Canonical{PriceCents: cents(2500000), State: "PR"}, true},
		{"exactly max", listing.Canonical{PriceCents: cents(3000000), State: "pr"}, true},
		{"over max", listing.Canonical{PriceCents: cents(3000001), State: "PR"}, false},
		{"no price with max set", listing.Canonical{State: "PR"}, false},
		{"other state", listing.Canonical{PriceCents: cents(100), State: "SP"}, false},
	}
	for _, tc := range cases {
		if got := rule.Matches(tc.c); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
	disabled := search.AlertRule{}
	if disabled.Matches(listing.Canonical{}) {
		t.Error("disabled rule matched")
	}
}

func TestWantsNewAndPriceDrop(t *testing.T) {
	c := listing.Canonical{PriceCents: cents(2000000)}
	r := search.AlertRule{Enabled: true, OnNew: true, OnPriceDrop: true, MinDropPct: 10}
	if !r.WantsNew(c) {
		t.Error("WantsNew = false")
	}
	if !r.WantsPriceDrop(2300000, 2000000, c) {
		t.Error("13% drop should alert with min 10%")
	}
	if r.WantsPriceDrop(2100000, 2000000, c) {
		t.Error("4.7% drop should not alert with min 10%")
	}
	if r.WantsPriceDrop(2000000, 2100000, c) {
		t.Error("price increase should not alert")
	}
	r.OnNew = false
	if r.WantsNew(c) {
		t.Error("WantsNew with on_new=false")
	}
}
