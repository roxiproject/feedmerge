package search

import (
	"strings"
	"unicode/utf8"
)

// snippetRunes is how much context Snippet returns around a match.
const snippetRunes = 200

// Snippet returns up to width runes of text centred on the first occurrence of
// any of the given terms, with ellipses marking where text was cut. Text with
// no match is truncated from the start.
func Snippet(text string, terms []string, width int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" || width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(flat) <= width {
		return flat
	}

	pos := -1
	lower := strings.ToLower(flat)
	for _, t := range terms {
		if t == "" {
			continue
		}
		if p := strings.Index(lower, strings.ToLower(t)); p >= 0 && (pos < 0 || p < pos) {
			pos = p
		}
	}
	if pos < 0 {
		return trimEllipsis(runeSlice(flat, 0, width), false, true)
	}

	// Centre the window on the match, clamped to the text.
	startRune := utf8.RuneCountInString(flat[:pos]) - width/3
	if startRune < 0 {
		startRune = 0
	}
	total := utf8.RuneCountInString(flat)
	if startRune+width > total {
		startRune = total - width
		if startRune < 0 {
			startRune = 0
		}
	}
	win := runeSlice(flat, startRune, startRune+width)
	return trimEllipsis(win, startRune > 0, startRune+width < total)
}

// runeSlice returns flat[from:to] measured in runes.
func runeSlice(s string, from, to int) string {
	if from < 0 {
		from = 0
	}
	start, end, n := -1, len(s), 0
	for i := range s {
		if n == from {
			start = i
		}
		if n == to {
			end = i
			break
		}
		n++
	}
	if start < 0 {
		return ""
	}
	return s[start:end]
}

// trimEllipsis trims partial words at a cut edge and marks it with an ellipsis.
func trimEllipsis(s string, cutLeft, cutRight bool) string {
	if cutLeft {
		if sp := strings.IndexByte(s, ' '); sp >= 0 {
			s = s[sp+1:]
		}
	}
	if cutRight {
		if sp := strings.LastIndexByte(s, ' '); sp > 0 {
			s = s[:sp]
		}
	}
	s = strings.TrimSpace(s)
	if cutLeft {
		s = "…" + s
	}
	if cutRight {
		s += "…"
	}
	return s
}
