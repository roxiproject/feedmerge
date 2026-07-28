package search

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		terms []string
		width int
		want  string
	}{
		{"short text returned whole", "just a few words", []string{"few"}, 100, "just a few words"},
		{"empty text", "", []string{"x"}, 100, ""},
		{"zero width", "some text", []string{"x"}, 0, ""},
		{"whitespace flattened", "a\n\nb   c", nil, 100, "a b c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Snippet(tt.text, tt.terms, tt.width); got != tt.want {
				t.Errorf("Snippet = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSnippetWithoutMatchTruncatesFromStart(t *testing.T) {
	text := strings.Repeat("word ", 200)
	got := Snippet(text, []string{"absent"}, 40)
	if !strings.HasPrefix(got, "word word") {
		t.Errorf("snippet = %q, want it to start at the beginning", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("snippet = %q, want a trailing ellipsis", got)
	}
}
