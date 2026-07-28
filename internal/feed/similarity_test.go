package feed

import (
	"strings"
	"testing"
	"time"
)

func TestContentWords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"markup dropped", `<p class="a">Hello <b>world</b></p>`, []string{"hello", "world"}},
		{"entities decoded", `caf&eacute; noir`, []string{"café", "noir"}},
		{"non-letter entity separates", `one&nbsp;two`, []string{"one", "two"}},
		{"punctuation separates", "one, two. three!", []string{"one", "two", "three"}},
		{"digits kept", "go 1.22 released", []string{"go", "1", "22", "released"}},
		{"bare ampersand separates", "at & t", []string{"at", "t"}},
		{"unterminated tag swallows the rest", "text <b", []string{"text"}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContentWords(tt.in)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("ContentWords(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestContentSimilarity(t *testing.T) {
	const body = "The write ahead log is how Postgres survives a crash without losing committed work."

	if got := ContentSimilarity(body, body); got != 1 {
		t.Errorf("identical bodies scored %v, want 1", got)
	}
	// The same text wrapped in different markup is still the same text.
	wrapped := "<div><p>" + strings.Replace(body, "Postgres", "<em>Postgres</em>", 1) + "</p></div>"
	if got := ContentSimilarity(body, wrapped); got != 1 {
		t.Errorf("re-wrapped body scored %v, want 1", got)
	}
	// An edited copy shares most of its four-word runs.
	edited := "The write ahead log is how Postgres survives a crash without losing any committed work at all."
	if got := ContentSimilarity(body, edited); got < 0.5 || got >= 1 {
		t.Errorf("edited copy scored %v, want a high score below 1", got)
	}
	unrelated := "Structured logging arrived in the standard library with the slog package."
	if got := ContentSimilarity(body, unrelated); got != 0 {
		t.Errorf("unrelated bodies scored %v, want 0", got)
	}
	if got := ContentSimilarity(body, ""); got != 0 {
		t.Errorf("empty body scored %v, want 0", got)
	}
}

func TestContentSimilarityShortBodies(t *testing.T) {
	// Bodies shorter than one shingle are compared whole.
	if got := ContentSimilarity("two words", "two words"); got != 1 {
		t.Errorf("identical short bodies scored %v, want 1", got)
	}
	if got := ContentSimilarity("two words", "other words"); got != 0 {
		t.Errorf("different short bodies scored %v, want 0", got)
	}
}

func TestShinglesCount(t *testing.T) {
	// Seven distinct words yield four overlapping four-word shingles.
	got := Shingles("alpha beta gamma delta epsilon zeta eta")
	if len(got) != 4 {
		t.Errorf("got %d shingles, want 4", len(got))
	}
	if len(Shingles("")) != 0 {
		t.Error("empty body produced shingles")
	}
}

func contentEntry(id, title, body string, day int) Entry {
	return Entry{
		ID:        id,
		Title:     title,
		Link:      "https://" + id + ".example/story",
		Content:   "<p>" + body + "</p>",
		Published: time.Date(2026, 7, day, 0, 0, 0, 0, time.UTC),
	}
}

func TestDedupByContentSimilarity(t *testing.T) {
	const body = "A long enough article body about the write ahead log and how it protects committed work from a crash."
	entries := []Entry{
		contentEntry("origin", "Write ahead logging explained", body, 1),
		contentEntry("mirror", "Everything you wanted to know about WAL", body, 1),
		contentEntry("other", "Structured logging with slog", "The slog package brings structured logging to the standard library at last.", 1),
	}

	opts := DedupOptions{TitleThreshold: 0.9, ContentThreshold: 0.8}
	got := Dedup(entries, opts)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), titlesOf(got))
	}
	if got[0].ID != "origin" {
		t.Errorf("survivor = %q, want the first occurrence", got[0].ID)
	}

	// Content matching is off by default, so the same input keeps both copies.
	if plain := Dedup(entries, DedupOptions{TitleThreshold: 0.9}); len(plain) != 3 {
		t.Errorf("content matching ran while disabled: %v", titlesOf(plain))
	}
}

func TestDedupContentRespectsTheTimeWindow(t *testing.T) {
	const body = "A long enough article body about the write ahead log and how it protects committed work from a crash."
	entries := []Entry{
		contentEntry("origin", "Write ahead logging explained", body, 1),
		contentEntry("repost", "A year later, the same article", body, 20),
	}
	opts := DedupOptions{ContentThreshold: 0.8, TitleWindow: 24 * 3600}
	if got := Dedup(entries, opts); len(got) != 2 {
		t.Errorf("entries outside the window were merged: %v", titlesOf(got))
	}
	opts.TitleWindow = 0
	if got := Dedup(entries, opts); len(got) != 1 {
		t.Errorf("entries were not merged without a window: %v", titlesOf(got))
	}
}

func TestDedupContentIgnoresBodylessEntries(t *testing.T) {
	entries := []Entry{
		{ID: "a", Title: "One", Link: "https://a.example/1"},
		{ID: "b", Title: "Two", Link: "https://b.example/2"},
	}
	if got := Dedup(entries, DedupOptions{ContentThreshold: 0.5}); len(got) != 2 {
		t.Errorf("body-less entries were merged: %v", titlesOf(got))
	}
}

func TestDedupContentFallsBackToTheSummary(t *testing.T) {
	const body = "A long enough article body about the write ahead log and how it protects committed work."
	entries := []Entry{
		{ID: "a", Title: "One", Link: "https://a.example/1", Summary: body},
		{ID: "b", Title: "Two", Link: "https://b.example/2", Content: body},
	}
	if got := Dedup(entries, DedupOptions{ContentThreshold: 0.9}); len(got) != 1 {
		t.Errorf("summary was not compared against content: %v", titlesOf(got))
	}
}

func titlesOf(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Title
	}
	return out
}
