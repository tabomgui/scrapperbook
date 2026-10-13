// Discord implementation of Notifier: one POST per notification to a
// webhook URL — a rich embed (title, price, location, thumbnail) for
// new/price_drop, plain text content for system alerts.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type DiscordNotifier struct {
	WebhookURL string
	HTTPClient *http.Client
}

func NewDiscordNotifier(webhookURL string) *DiscordNotifier {
	return &DiscordNotifier{WebhookURL: webhookURL, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

type discordThumbnail struct {
	URL string `json:"url"`
}

type discordEmbed struct {
	Title       string            `json:"title"`
	URL         string            `json:"url,omitempty"`
	Description string            `json:"description,omitempty"`
	Thumbnail   *discordThumbnail `json:"thumbnail,omitempty"`
}

type discordPayload struct {
	Content string         `json:"content,omitempty"`
	Embeds  []discordEmbed `json:"embeds,omitempty"`
}

func (d *DiscordNotifier) Send(ctx context.Context, n Notification) error {
	body, err := json.Marshal(buildPayload(n))
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post to discord: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("discord rate limited this webhook (429)")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned %d", resp.StatusCode)
	}
	return nil
}

func buildPayload(n Notification) discordPayload {
	if n.Kind == "system" {
		return discordPayload{Content: n.Message}
	}

	price := "preço não informado"
	if n.PriceCents != nil {
		price = formatCents(*n.PriceCents)
	}
	if n.Kind == "price_drop" && n.OldPriceCents != nil && n.PriceCents != nil {
		price = fmt.Sprintf("%s → %s", formatCents(*n.OldPriceCents), formatCents(*n.PriceCents))
	}

	location := n.City
	switch {
	case location != "" && n.State != "":
		location += "/" + n.State
	case location == "":
		location = n.State
	}

	desc := price
	if location != "" {
		desc += " · " + location
	}

	embed := discordEmbed{
		Title:       truncateRunes(n.ListingTitle, discordTitleMaxRunes),
		URL:         n.ListingURL,
		Description: truncateRunes(desc, discordDescriptionMaxRunes),
	}
	if n.ImageURL != "" {
		embed.Thumbnail = &discordThumbnail{URL: n.ImageURL}
	}
	return discordPayload{Embeds: []discordEmbed{embed}}
}

// Discord rejects embeds whose title/description exceed these limits with a
// 400 response. Since Send treats any status >= 300 as retryable, an
// oversized field would otherwise produce a notification that can never
// succeed and gets retried forever.
const (
	discordTitleMaxRunes       = 256
	discordDescriptionMaxRunes = 4096
)

// truncateRunes truncates s to at most max runes, rune-safe (not byte
// slicing) since titles may contain multi-byte UTF-8.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func formatCents(cents int64) string {
	return fmt.Sprintf("R$ %d,%02d", cents/100, cents%100)
}
