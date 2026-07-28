package search

import (
	"strings"
	"testing"
	"time"

	"github.com/roxiproject/feedmerge/internal/feed"
)

func doc(id, title, body string, day int, extra ...string) feed.Entry {
	e := feed.Entry{
		ID:        id,
		Title:     title,
		Content:   "<p>" + body + "</p>",
		Link:      "https://example.org/" + id,
		Published: time.Date(2026, 7, day, 0, 0, 0, 0, time.UTC),
	}
	if len(extra) > 0 {
		e.Author = extra[0]
	}
	if len(extra) > 1 {
		e.Categories = strings.Fields(extra[1])
	}
	if len(extra) > 2 {
		e.SourceTitle = extra[2]
	}
	return e
}

func testIndex() *Index {
	return New([]feed.Entry{
		doc("a", "Structured logging with slog", "The slog package brings structured logging to the standard library.", 1, "Jonathan Amsterdam", "go logging", "The Go Blog"),
		doc("b", "Postgres write ahead log internals", "The write ahead log is how Postgres survives a crash.", 2, "Ann Author", "postgres", "PG News"),
		doc("c", "Logging in production", "A note about logging volume, sampling and cost.", 3, "Ann Author", "ops", "Ops Weekly"),
		doc("d", "Release candidate 2 is out", "This release candidate fixes a slog formatting bug.", 4, "Rel Bot", "go", "The Go Blog"),
	})
}

func resultIDs(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Entry.ID
	}
	return out
}

func TestSearchRanking(t *testing.T) {
	idx := testIndex()
	tests := []struct {
		name  string
		query string
		limit int
		want  []string
	}{
		{"title hit outranks body hit", "slog", 0, []string{"a", "d"}},
		{"common term returns all matches", "logging", 0, []string{"a", "c"}},
		{"phrase must be adjacent", `"write ahead log"`, 0, []string{"b"}},
		{"phrase that never occurs", `"ahead write log"`, 0, nil},
		{"excluded term removes a hit", "slog -release", 0, []string{"a"}},
		{"required term narrows", "logging +production", 0, []string{"c"}},
		{"negated phrase", `slog -"release candidate"`, 0, []string{"a"}},
		{"author is searchable", "amsterdam", 0, []string{"a"}},
		{"category is searchable", "ops", 0, []string{"c"}},
		{"source title is searchable", "weekly", 0, []string{"c"}},
		{"limit applies after ranking", "logging", 1, []string{"a"}},
		{"unknown term finds nothing", "kubernetes", 0, nil},
		{"empty query finds nothing", "", 0, nil},
		{"stop-word-only query finds nothing", "the and of", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resultIDs(idx.Search(tt.query, tt.limit))
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("Search(%q, %d) = %v, want %v", tt.query, tt.limit, got, tt.want)
			}
		})
	}
}

func TestSearchScoresAreDescending(t *testing.T) {
	rs := testIndex().Search("logging slog", 0)
	if len(rs) < 2 {
		t.Fatalf("got %d results, want at least 2", len(rs))
	}
	for i := 1; i < len(rs); i++ {
		if rs[i-1].Score < rs[i].Score {
			t.Errorf("results not sorted: %f before %f", rs[i-1].Score, rs[i].Score)
		}
	}
	if rs[0].Score <= 0 {
		t.Errorf("top score = %f, want > 0", rs[0].Score)
	}
	if len(rs[0].Matched) == 0 {
		t.Error("top result reports no matched terms")
	}
}

func TestSearchSnippetContainsMatch(t *testing.T) {
	body := strings.Repeat("filler words here. ", 60) + "the needle appears late. " + strings.Repeat("more filler. ", 60)
	idx := New([]feed.Entry{{ID: "x", Title: "Long entry", Content: "<p>" + body + "</p>"}})
	rs := idx.Search("needle", 0)
	if len(rs) != 1 {
		t.Fatalf("got %d results, want 1", len(rs))
	}
	if !strings.Contains(rs[0].Snippet, "needle") {
		t.Errorf("snippet does not contain the match: %q", rs[0].Snippet)
	}
	if n := len([]rune(rs[0].Snippet)); n > snippetRunes+2 {
		t.Errorf("snippet is %d runes, want <= %d", n, snippetRunes+2)
	}
}

func TestSearchTiesBreakByDate(t *testing.T) {
	idx := New([]feed.Entry{
		doc("older", "Same headline", "same body", 1),
		doc("newer", "Same headline", "same body", 9),
	})
	got := resultIDs(idx.Search("headline", 0))
	if strings.Join(got, "|") != "newer|older" {
		t.Errorf("tie order = %v, want [newer older]", got)
	}
}

func TestSearchStripsMarkupBeforeIndexing(t *testing.T) {
	idx := New([]feed.Entry{{
		ID: "m", Title: "Markup", Content: `<p>visible</p><script>hiddenword</script>`,
	}})
	if got := idx.Search("hiddenword", 0); len(got) != 0 {
		t.Errorf("script contents were indexed: %v", resultIDs(got))
	}
	if got := idx.Search("visible", 0); len(got) != 1 {
		t.Errorf("body text was not indexed: %v", resultIDs(got))
	}
}

func TestSearchFallsBackToSummary(t *testing.T) {
	idx := New([]feed.Entry{{ID: "s", Title: "T", Summary: "<p>only in the summary</p>"}})
	if got := resultIDs(idx.Search("summary", 0)); len(got) != 1 {
		t.Errorf("summary was not indexed: %v", got)
	}
}
