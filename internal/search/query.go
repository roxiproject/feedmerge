package search

import "strings"

// Term is one word in a query.
type Term struct {
	Word string
	// Require is set by a leading '+': the document must contain the term.
	Require bool
	// Negate is set by a leading '-': the document must not contain it.
	Negate bool
}

// Phrase is a quoted sequence of words that must appear adjacently.
type Phrase struct {
	Terms  []string
	Negate bool
}

// Query is a parsed search expression.
type Query struct {
	Terms   []Term
	Phrases []Phrase
}

// Empty reports whether the query has nothing to match on, which happens when
// the input was blank, punctuation only or entirely stop words.
func (q Query) Empty() bool { return len(q.Terms) == 0 && len(q.Phrases) == 0 }

// ParseQuery understands bare words, "quoted phrases", +required and -excluded
// prefixes on either form. Unbalanced quotes are treated as if the closing
// quote were at the end of the input, so a half-typed query still searches
// rather than erroring.
func ParseQuery(q string) Query {
	var out Query
	i := 0
	for i < len(q) {
		// Skip leading space.
		for i < len(q) && isSpace(q[i]) {
			i++
		}
		if i >= len(q) {
			break
		}
		require, negate := false, false
		switch q[i] {
		case '+':
			require, i = true, i+1
		case '-':
			negate, i = true, i+1
		}
		if i >= len(q) {
			break
		}
		if q[i] == '"' {
			i++
			end := strings.IndexByte(q[i:], '"')
			var body string
			if end < 0 {
				body, i = q[i:], len(q)
			} else {
				body, i = q[i:i+end], i+end+1
			}
			if terms := Tokenize(body); len(terms) > 0 {
				out.Phrases = append(out.Phrases, Phrase{Terms: terms, Negate: negate})
			}
			continue
		}
		start := i
		for i < len(q) && !isSpace(q[i]) {
			i++
		}
		for _, w := range Tokenize(q[start:i]) {
			out.Terms = append(out.Terms, Term{Word: w, Require: require, Negate: negate})
		}
	}
	return out
}

// scoringTerms lists the terms that contribute to the BM25 score: everything
// except exclusions.
func (q Query) scoringTerms() []string {
	out := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		if !t.Negate {
			out = append(out, t.Word)
		}
	}
	return out
}

// String renders the query back into its source form, which is useful in logs
// and in the JSON search response.
func (q Query) String() string {
	parts := make([]string, 0, len(q.Terms)+len(q.Phrases))
	for _, t := range q.Terms {
		parts = append(parts, prefix(t.Require, t.Negate)+t.Word)
	}
	for _, p := range q.Phrases {
		parts = append(parts, prefix(false, p.Negate)+`"`+strings.Join(p.Terms, " ")+`"`)
	}
	return strings.Join(parts, " ")
}

func prefix(require, negate bool) string {
	switch {
	case require:
		return "+"
	case negate:
		return "-"
	}
	return ""
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
