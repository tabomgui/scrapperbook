// Package listing defines the source-independent listing model.
package listing

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

type Status string

const (
	StatusActive  Status = "active"
	StatusPending Status = "pending"
	StatusSold    Status = "sold"
	StatusUnseen  Status = "unseen"
)

// Canonical is the shape every source normalizer produces. It mirrors the
// listings table columns of the same name (see migrations/00001_init.sql).
type Canonical struct {
	Source     string // e.g. "facebook_marketplace"
	ExternalID string // stable ID from the source; (Source, ExternalID) is the dedup key
	URL        string

	Title       string
	Description string
	PriceCents  *int64 // nil means price unknown/hidden, not zero
	Currency    string // ISO 4217 code, e.g. "BRL"

	// LocationText is the source's own display string (e.g. "Curitiba, PR").
	// City/State are parsed out of it by the normalizer when possible; either
	// may be empty if the source didn't expose a clean breakdown.
	LocationText string
	City         string
	State        string
	Lat          *float64
	Lng          *float64

	ImageURL string

	SellerExternalID string // source-scoped; see Fingerprint's doc comment
	SellerName       string

	Status Status
}

// ContentHash changes whenever a field worth a snapshot changes: Title,
// PriceCents, Status or Description. Everything else (location, seller,
// image, currency, URL) is metadata we don't version in listing_snapshots.
func (c Canonical) ContentHash() string {
	return hashParts(c.Title, priceKey(c.PriceCents), string(c.Status), c.Description)
}

// Fingerprint identifies reposts of the same item within one source (same
// seller, title, price and location, different ExternalID). It is NOT yet
// useful across sources: SellerExternalID is source-scoped, so the same
// person's listing on two different marketplaces will not match. Real
// cross-source matching needs a different signal and is future work.
func (c Canonical) Fingerprint() string {
	return hashParts(
		NormalizeText(c.Title),
		priceKey(c.PriceCents),
		NormalizeText(c.SellerExternalID),
		NormalizeText(c.City),
		NormalizeText(c.State),
	)
}

// NormalizeText lowercases, strips accents and collapses everything that is
// not a letter or digit into single spaces.
func NormalizeText(s string) string {
	// transform.Chain is stateful, so build one per call.
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err != nil {
		folded = s
	}
	var b strings.Builder
	pendingSpace := false
	for _, r := range strings.ToLower(folded) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
			continue
		}
		pendingSpace = true
	}
	return b.String()
}

func priceKey(p *int64) string {
	if p == nil {
		return "none"
	}
	return strconv.FormatInt(*p, 10)
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0x1f})
	}
	return hex.EncodeToString(h.Sum(nil))
}
