package feed

import (
	"hash/fnv"
	"strings"
	"unicode"
)

// shingleSize is the number of consecutive words in one shingle. Four words is
// long enough that ordinary prose does not collide by accident and short enough
// that an edited paragraph still overlaps heavily with the original.
const shingleSize = 4

// ContentWords reduces markup to the lowercased words used for comparison.
// Tags are skipped rather than escaped, so an entry that only differs in its
// wrapper markup compares as identical text.
func ContentWords(body string) []string {
	var (
		out    []string
		word   strings.Builder
		inTag  bool
		inRef  bool
		refBuf strings.Builder
	)
	flush := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, r := range body {
		switch {
		case inTag:
			if r == '>' {
				inTag = false
			}
		case r == '<':
			flush()
			inTag = true
		case inRef:
			if r == ';' {
				inRef = false
				// A decoded entity is a separator unless it is a plain letter
				// or digit reference, which decodes into the current word.
				if d := []rune(DecodeEntities("&" + refBuf.String() + ";")); len(d) == 1 && isWordRune(d[0]) {
					word.WriteRune(unicode.ToLower(d[0]))
				} else {
					flush()
				}
				refBuf.Reset()
				continue
			}
			if refBuf.Len() > 12 || unicode.IsSpace(r) {
				// Not an entity after all; treat the '&' as a separator.
				inRef = false
				refBuf.Reset()
				flush()
				continue
			}
			refBuf.WriteRune(r)
		case r == '&':
			inRef = true
		case isWordRune(r):
			word.WriteRune(unicode.ToLower(r))
		default:
			flush()
		}
	}
	flush()
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// Shingles hashes the overlapping word n-grams of a body. Hashes are stored
// rather than the strings themselves: comparing two long articles otherwise
// costs more memory than the articles.
func Shingles(body string) map[uint64]struct{} {
	words := ContentWords(body)
	out := make(map[uint64]struct{})
	if len(words) == 0 {
		return out
	}
	if len(words) < shingleSize {
		// A body too short to shingle is compared as a single unit, which is
		// what makes two identical one-line entries match.
		out[hashWords(words)] = struct{}{}
		return out
	}
	for i := 0; i+shingleSize <= len(words); i++ {
		out[hashWords(words[i:i+shingleSize])] = struct{}{}
	}
	return out
}

func hashWords(words []string) uint64 {
	h := fnv.New64a()
	for i, w := range words {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(w))
	}
	return h.Sum64()
}

// ContentSimilarity is the Jaccard similarity of two bodies' shingle sets: 1
// for the same text, 0 for texts that share no four-word run. Comparing an
// empty body with anything yields 0, so a body-less entry never matches this
// way.
func ContentSimilarity(a, b string) float64 {
	sa, sb := Shingles(a), Shingles(b)
	return shingleSimilarity(sa, sb)
}

func shingleSimilarity(a, b map[uint64]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Intersect the smaller set against the larger one.
	small, large := a, b
	if len(large) < len(small) {
		small, large = large, small
	}
	inter := 0
	for h := range small {
		if _, ok := large[h]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// entryBody returns the text of an entry that content matching compares,
// preferring the full content over the summary.
func entryBody(e Entry) string {
	if strings.TrimSpace(e.Content) != "" {
		return e.Content
	}
	return e.Summary
}
