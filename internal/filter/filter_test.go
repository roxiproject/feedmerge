package filter

import (
	"strings"
	"testing"

	"github.com/roxiproject/feedmerge/internal/feed"
)

var entries = []feed.Entry{
	{
		Title: "Go 1.22 is released", Link: "https://go.dev/blog/go1.22",
		Content: "The Go team is pleased to announce Go 1.22.", Summary: "Release notes",
		Author: "The Go Team", Categories: []string{"release", "go"},
		SourceTitle: "The Go Blog", SourceURL: "https://go.dev/blog/feed.atom",
	},
	{
		Title: "SPONSORED: buy our thing", Link: "https://ads.example/x?utm_campaign=paid",
		Content: "Marketing copy.", Author: "Ads Inc", Categories: []string{"promo"},
		SourceTitle: "Ad Network", SourceURL: "https://ads.example/feed.xml",
	},
	{
		Title: "Postgres 16 performance notes", Link: "https://pg.example/perf",
		Content: "Benchmarks and tuning.", Author: "DBA", Categories: []string{"postgres"},
		SourceTitle: "PG Weekly", SourceURL: "https://pg.example/feed.xml",
	},
}

func titles(es []feed.Entry) string {
	var out []string
	for _, e := range es {
		out = append(out, e.Title)
	}
	return strings.Join(out, "|")
}

func TestApply(t *testing.T) {
	tests := []struct {
		name  string
		rules string
		want  string
	}{
		{"empty set allows everything", "", "Go 1.22 is released|SPONSORED: buy our thing|Postgres 16 performance notes"},
		{"comments and blanks ignored", "# nothing here\n\n   \n", "Go 1.22 is released|SPONSORED: buy our thing|Postgres 16 performance notes"},
		{"exclude by title", "exclude title ~ /sponsored/i", "Go 1.22 is released|Postgres 16 performance notes"},
		{"exclude by link", "exclude link ~ utm_campaign=paid", "Go 1.22 is released|Postgres 16 performance notes"},
		{"include only", "include title ~ /postgres/i", "Postgres 16 performance notes"},
		{"two includes are a union", "include title ~ /postgres/i\ninclude title ~ /^Go /", "Go 1.22 is released|Postgres 16 performance notes"},
		{"exclude wins over include", "include title ~ .\nexclude category ~ promo", "Go 1.22 is released|Postgres 16 performance notes"},
		{"negated operator", "include title !~ /sponsored/i", "Go 1.22 is released|Postgres 16 performance notes"},
		{"field any", "include any ~ /benchmarks/i", "Postgres 16 performance notes"},
		{"field author", "exclude author ~ Ads Inc", "Go 1.22 is released|Postgres 16 performance notes"},
		{"field content", "include content ~ /pleased to announce/", "Go 1.22 is released"},
		{"field summary", "include summary ~ /Release notes/", "Go 1.22 is released"},
		{"field source", "include source ~ go\\.dev", "Go 1.22 is released"},
		{"bare pattern without slashes", "include title ~ (?i)postgres", "Postgres 16 performance notes"},
		{"pattern with spaces", "include title ~ /is released/", "Go 1.22 is released"},
		{"= tilde alias", "include title =~ /postgres/i", "Postgres 16 performance notes"},
		{"nothing matches", "include title ~ /nonexistent/", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set, err := Parse(tc.rules)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.rules, err)
			}
			if got := titles(set.Apply(entries)); got != tc.want {
				t.Errorf("Apply = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, rule string }{
		{"too few tokens", "include title"},
		{"unknown action", "maybe title ~ x"},
		{"unknown field", "include headline ~ x"},
		{"unknown operator", "include title == x"},
		{"invalid regexp", "include title ~ /([unclosed/"},
		{"unknown flag", "include title ~ /x/q"},
		{"empty pattern", "include title ~ //"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.rule); err == nil {
				t.Fatalf("expected an error for %q", tc.rule)
			}
		})
	}
}

func TestParseErrorReportsLineNumber(t *testing.T) {
	_, err := Parse("include title ~ /ok/\n\nexclude bogus ~ /x/")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error %q does not mention line 3", err)
	}
}

func TestNilAndEmptySet(t *testing.T) {
	var s *Set
	if !s.Allow(entries[0]) {
		t.Error("a nil set must allow everything")
	}
	if got := len(s.Apply(entries)); got != 3 {
		t.Errorf("nil set Apply returned %d entries", got)
	}
	if s.Len() != 0 || s.Rules() != nil {
		t.Error("nil set should report zero rules")
	}
}

func TestParseLinesAndRuleAccessors(t *testing.T) {
	set, err := ParseLines([]string{"exclude title ~ /spam/i", "include any ~ go"})
	if err != nil {
		t.Fatalf("ParseLines: %v", err)
	}
	if set.Len() != 2 {
		t.Fatalf("Len = %d", set.Len())
	}
	rules := set.Rules()
	if rules[0].Include || rules[0].Field != FieldTitle {
		t.Errorf("rule 0 = %+v", rules[0])
	}
	if !rules[1].Include || rules[1].Field != FieldAny {
		t.Errorf("rule 1 = %+v", rules[1])
	}
	if rules[0].Source != "exclude title ~ /spam/i" {
		t.Errorf("Source = %q", rules[0].Source)
	}
}

func TestUnwrapPattern(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"abc", "abc", false},
		{"/abc/", "abc", false},
		{"/abc/i", "(?i)abc", false},
		{"/abc/is", "(?is)abc", false},
		{"/abc", "", true},
		{"", "", true},
	}
	for _, tc := range tests {
		got, err := unwrapPattern(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("unwrapPattern(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("unwrapPattern(%q): %v", tc.in, err)
		} else if got != tc.want {
			t.Errorf("unwrapPattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
