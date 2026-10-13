package search

import "strings"

// TitleMatches reports whether every word of keyword appears somewhere in
// title (case-insensitive, substring match — "hatch" matches "hatchback").
// This is deliberately naive: no accent folding, no synonyms, no word-
// boundary checking. It exists to reject listings a crawler's own search
// returned that are obviously unrelated to what was asked for (wrong
// model, accessories, etc.) — not to be a precise relevance ranker.
func TitleMatches(keyword, title string) bool {
	title = strings.ToLower(title)
	for _, word := range strings.Fields(strings.ToLower(keyword)) {
		if !strings.Contains(title, word) {
			return false
		}
	}
	return true
}
