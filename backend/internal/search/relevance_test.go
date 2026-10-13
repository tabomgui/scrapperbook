package search_test

import (
	"testing"

	"scrapperbook/backend/internal/search"
)

func TestTitleMatchesRequiresEveryKeywordWord(t *testing.T) {
	cases := []struct {
		name    string
		keyword string
		title   string
		want    bool
	}{
		{"all words present", "Civic hatch", "Honda Civic Hatch 2015", true},
		{"extra words in title are fine", "Civic hatch", "Vendo Honda Civic Hatch LX 2015 completo", true},
		{"case insensitive", "Civic hatch", "HONDA CIVIC HATCH", true},
		{"missing one word fails", "Civic hatch", "Honda Civic Sedan 2015", false},
		{"missing all words fails", "Civic hatch", "Fiat Palio 2010", false},
		{"substring-within-word counts (hatch matches hatchback)", "Civic hatch", "Honda Civic Hatchback", true},
		{"single-word keyword", "Civic", "Honda Civic EXL", true},
		{"empty keyword matches everything", "", "anything at all", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := search.TitleMatches(c.keyword, c.title)
			if got != c.want {
				t.Errorf("TitleMatches(%q, %q) = %v, want %v", c.keyword, c.title, got, c.want)
			}
		})
	}
}
