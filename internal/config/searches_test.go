package config

import (
	"strings"
	"testing"
)

const feedsSection = "feeds:\n  - https://a.example/feed.xml\n"

func TestParseSavedSearches(t *testing.T) {
	src := `searches:
  - name: go
    query: golang "release candidate"
    title: Go stories
    limit: 25
  - name: pg_news
    query: postgres -beta
` + feedsSection

	cfg, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if len(cfg.Searches) != 2 {
		t.Fatalf("got %d searches, want 2", len(cfg.Searches))
	}
	first := cfg.Searches[0]
	if first.Name != "go" || first.Query != `golang "release candidate"` {
		t.Errorf("first search = %+v", first)
	}
	if first.Limit != 25 || first.FeedTitle() != "Go stories" {
		t.Errorf("limit/title = %d/%q", first.Limit, first.FeedTitle())
	}
	// A search without a title is published under its name.
	if got := cfg.Searches[1].FeedTitle(); got != "pg_news" {
		t.Errorf("title fallback = %q", got)
	}
}

func TestParseSavedSearchesJSON(t *testing.T) {
	cfg, err := ParseJSON([]byte(`{"searches":[{"name":"go","query":"golang","limit":5}],
		"feeds":["https://a.example/feed.xml"]}`))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if len(cfg.Searches) != 1 || cfg.Searches[0].Limit != 5 {
		t.Fatalf("searches = %+v", cfg.Searches)
	}
}

func TestSavedSearchErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"no name", "searches:\n  - query: go\n" + feedsSection, "has no name"},
		{"no query", "searches:\n  - name: go\n" + feedsSection, "has no query"},
		{"bad name", "searches:\n  - name: go stories\n    query: go\n" + feedsSection, "may only contain"},
		{"duplicate", "searches:\n  - name: go\n    query: go\n  - name: go\n    query: rust\n" + feedsSection, "duplicate saved search"},
		{"negative limit", "searches:\n  - name: go\n    query: go\n    limit: -1\n" + feedsSection, "must not be negative"},
		{"bad limit", "searches:\n  - name: go\n    query: go\n    limit: soon\n" + feedsSection, "must be an integer"},
		{"unknown key", "searches:\n  - name: go\n    query: go\n    colour: red\n" + feedsSection, "unknown search key"},
		{"not a list", "searches: go\n" + feedsSection, `"searches" must be a list`},
		{"not a mapping", "searches:\n  - go\n" + feedsSection, "must be a mapping"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseYAML(tt.src)
			if err == nil {
				t.Fatalf("ParseYAML succeeded, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestIsSearchName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"go", true}, {"Go-1_2", true}, {"", true}, {"go stories", false},
		{"go/rust", false}, {"go.news", false}, {"café", false},
	}
	for _, tt := range tests {
		if got := isSearchName(tt.in); got != tt.want {
			t.Errorf("isSearchName(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestConfigWithoutSearches(t *testing.T) {
	cfg, err := ParseYAML(feedsSection)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if len(cfg.Searches) != 0 {
		t.Errorf("searches = %+v, want none", cfg.Searches)
	}
}
