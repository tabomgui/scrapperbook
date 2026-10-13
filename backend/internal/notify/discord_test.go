package notify_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scrapperbook/backend/internal/notify"
)

func int64Ptr(v int64) *int64 { return &v }

func TestDiscordSendsAnEmbedForANewListing(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := notify.NewDiscordNotifier(srv.URL)
	err := d.Send(t.Context(), notify.Notification{
		Kind: "new", ListingTitle: "Honda Civic EG 1993", ListingURL: "https://example/item/1",
		ImageURL: "https://example/civic.jpg", City: "Curitiba", State: "PR",
		PriceCents: int64Ptr(2350000),
	})
	if err != nil {
		t.Fatal(err)
	}
	embeds, ok := gotBody["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("body = %+v", gotBody)
	}
	embed := embeds[0].(map[string]any)
	if embed["title"] != "Honda Civic EG 1993" || embed["url"] != "https://example/item/1" {
		t.Errorf("embed = %+v", embed)
	}
	desc, _ := embed["description"].(string)
	if desc != "R$ 23500,00 · Curitiba/PR" {
		t.Errorf("description = %q", desc)
	}
	thumb, ok := embed["thumbnail"].(map[string]any)
	if !ok || thumb["url"] != "https://example/civic.jpg" {
		t.Errorf("thumbnail = %+v", embed["thumbnail"])
	}
}

func TestDiscordShowsOldAndNewPriceForAPriceDrop(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := notify.NewDiscordNotifier(srv.URL)
	err := d.Send(t.Context(), notify.Notification{
		Kind: "price_drop", ListingTitle: "Civic EG5", ListingURL: "https://example/item/2",
		City: "Curitiba", PriceCents: int64Ptr(2100000), OldPriceCents: int64Ptr(2350000),
	})
	if err != nil {
		t.Fatal(err)
	}
	embed := gotBody["embeds"].([]any)[0].(map[string]any)
	if embed["description"] != "R$ 23500,00 → R$ 21000,00 · Curitiba" {
		t.Errorf("description = %v", embed["description"])
	}
}

func TestDiscordSendsPlainContentForASystemAlert(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := notify.NewDiscordNotifier(srv.URL)
	err := d.Send(t.Context(), notify.Notification{Kind: "system", Message: "fonte precisa de atenção"})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["content"] != "fonte precisa de atenção" {
		t.Errorf("content = %v", gotBody["content"])
	}
	if _, hasEmbeds := gotBody["embeds"]; hasEmbeds {
		t.Errorf("system alert should not have embeds, got %v", gotBody["embeds"])
	}
}

func TestDiscordTruncatesAnOversizedTitleInsteadOfFailing(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Multi-byte rune repeated well past Discord's 256-char title limit, to
	// confirm truncation counts runes rather than bytes.
	longTitle := strings.Repeat("é", 300)
	d := notify.NewDiscordNotifier(srv.URL)
	err := d.Send(t.Context(), notify.Notification{
		Kind: "new", ListingTitle: longTitle, ListingURL: "https://example/item/3",
		PriceCents: int64Ptr(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	embed := gotBody["embeds"].([]any)[0].(map[string]any)
	title, _ := embed["title"].(string)
	if got := len([]rune(title)); got != 256 {
		t.Errorf("title rune length = %d, want 256", got)
	}
	if title != strings.Repeat("é", 256) {
		t.Errorf("title = %q, want 256 é runes", title)
	}
}

func TestDiscordReturnsErrorOnRateLimitAndServerError(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		d := notify.NewDiscordNotifier(srv.URL)
		if err := d.Send(t.Context(), notify.Notification{Kind: "system", Message: "x"}); err == nil {
			t.Errorf("status %d: expected error", status)
		}
		srv.Close()
	}
}
