// Package notify sends pending notifications (new listing, price drop,
// system alert) to an external channel. Discord is the only implementation
// today; Notifier exists so a different channel can be swapped in later
// without touching the outbox-draining logic in outbox.go.
package notify

import "context"

// Notification mirrors one row of the notifications table joined with the
// listing data a Notifier needs to build a message — no DB access needed
// inside a Notifier implementation.
type Notification struct {
	Kind          string // "new" | "price_drop" | "system"
	Message       string // only set for kind == "system"
	ListingTitle  string
	ListingURL    string
	ImageURL      string
	City          string
	State         string
	PriceCents    *int64
	OldPriceCents *int64 // only set for kind == "price_drop"
}

type Notifier interface {
	// Send delivers one notification. A non-nil error means the caller must
	// NOT mark it sent — it gets retried on the next drain.
	Send(ctx context.Context, n Notification) error
}
