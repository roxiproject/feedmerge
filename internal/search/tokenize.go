package search

import (
	"strings"
	"unicode"
)

// stopWords are dropped at index and query time. The list is deliberately
// short: aggressive stopping hurts phrase queries more than it helps.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "but": true, "by": true, "for": true, "from": true, "in": true,
	"is": true, "it": true, "of": true, "on": true, "or": true, "that": true,
	"the": true, "to": true, "was": true, "were": true, "will": true, "with": true,
}

// Tokenize splits text into lowercased alphanumeric terms, dropping stop words.
// Runs of digits and letters are kept together so that "go1.22" yields "go1"
// and "22" rather than being lost.
func Tokenize(text string) []string {
	var (
		out  []string
		word strings.Builder
	)
	flush := func() {
		if word.Len() == 0 {
			return
		}
		t := word.String()
		word.Reset()
		if !stopWords[t] {
			out = append(out, t)
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return out
}
