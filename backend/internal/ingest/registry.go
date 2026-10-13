package ingest

import (
	"encoding/json"

	"scrapperbook/backend/internal/listing"
)

// Normalizer maps one raw item from a source to the canonical model.
type Normalizer interface {
	Source() string
	Normalize(externalID string, raw json.RawMessage) (listing.Canonical, error)
}

// Registry maps source name to normalizer.
type Registry map[string]Normalizer

func NewRegistry(ns ...Normalizer) Registry {
	r := Registry{}
	for _, n := range ns {
		r[n.Source()] = n
	}
	return r
}
