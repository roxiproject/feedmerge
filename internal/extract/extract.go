// Package extract turns entry bodies into plain text: it removes boilerplate
// markup, collapses the result to readable prose, derives a short summary and
// estimates how long the entry takes to read.
//
// The HTML scanner here is deliberately small. Feed bodies are fragments, not
// documents, and are frequently malformed, so the scanner never tries to build
// a tree: it walks the markup once, drops the elements that are known to carry
// navigation or scripting rather than prose, and keeps everything else as text.
package extract

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/roxiproject/feedmerge/internal/feed"
)

// wordsPerMinute is the silent-reading speed used for the reading-time
// estimate. 200 wpm is the low end of the range usually measured for adults
// reading prose on screen, which errs on the generous side.
const wordsPerMinute = 200

// DefaultSummaryLen is the summary length in runes used by Entry.
const DefaultSummaryLen = 280

// Result is everything extraction derives from one entry body.
type Result struct {
	// Text is the body as plain text, with paragraphs separated by blank lines.
	Text string
	// Summary is a truncated, single-paragraph form of Text.
	Summary string
	// Words counts whitespace-separated words in Text.
	Words int
	// ReadingMinutes is the estimated reading time, rounded up, and is zero
	// only when the body has no words at all.
	ReadingMinutes int
}

// dropTags are elements whose *contents* are discarded along with the element,
// because they hold scripting, styling or site furniture rather than prose.
var dropTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"nav": true, "footer": true, "header": true, "aside": true,
	"form": true, "button": true, "select": true, "textarea": true,
	"iframe": true, "object": true, "embed": true, "svg": true, "math": true,
}

// blockTags are elements that end the current line of text.
var blockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "blockquote": true,
	"pre": true, "ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"table": true, "tr": true, "figure": true, "figcaption": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// voidTags never have a closing tag, so a "closing" one must not pop anything.
var voidTags = map[string]bool{
	"br": true, "hr": true, "img": true, "meta": true, "link": true,
	"input": true, "source": true, "col": true, "area": true, "base": true,
	"wbr": true, "param": true, "track": true, "embed": true,
}

// breakLevel classifies how much vertical space an element boundary produces:
// 0 none, 1 a line break, 2 a paragraph break.
func breakLevel(name string) int {
	switch {
	case name == "br":
		return 1
	case name == "p" || name == "div" || name == "blockquote" || name == "section" ||
		name == "article" || name == "pre" || name == "figure" ||
		(len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6'):
		return 2
	case blockTags[name]:
		return 1
	default:
		return 0
	}
}

// textWriter assembles plain text, deferring whitespace decisions until the
// next real character arrives. That way a run of adjacent block boundaries
// (\u003c/li\u003e\u003cli\u003e) collapses to one break, trailing whitespace never reaches the
// output, and a paragraph break always wins over a line break.
type textWriter struct {
	b       strings.Builder
	pending int
	space   bool
	wrote   bool
}

func (w *textWriter) brk(level int) {
	if level > w.pending {
		w.pending = level
	}
}

func (w *textWriter) text(s string) {
	for _, r := range s {
		if unicode.IsSpace(r) {
			w.space = true
			continue
		}
		if w.wrote {
			switch {
			case w.pending >= 2:
				w.b.WriteString("\n\n")
			case w.pending == 1:
				w.b.WriteByte('\n')
			case w.space:
				w.b.WriteByte(' ')
			}
		}
		w.b.WriteRune(r)
		w.wrote = true
		w.pending, w.space = 0, false
	}
}

// Plain converts an HTML fragment to plain text. Input that contains no markup
// is returned with only its entities decoded and whitespace normalized, so it
// is safe to call on bodies that were already plain text.
func Plain(html string) string {
	var w textWriter
	w.b.Grow(len(html))

	i := 0
	for i < len(html) {
		lt := strings.IndexByte(html[i:], '<')
		if lt < 0 {
			w.text(feed.DecodeEntities(html[i:]))
			break
		}
		w.text(feed.DecodeEntities(html[i : i+lt]))
		i += lt

		switch {
		case strings.HasPrefix(html[i:], "<!--"):
			i = skipUntil(html, i+4, "-->")
			continue
		case strings.HasPrefix(html[i:], "<![CDATA["):
			end := strings.Index(html[i+9:], "]]>")
			if end < 0 {
				w.text(html[i+9:])
				i = len(html)
				continue
			}
			// CDATA is literal: its contents are not entity-decoded.
			w.text(html[i+9 : i+9+end])
			i += 9 + end + 3
			continue
		case strings.HasPrefix(html[i:], "<!"), strings.HasPrefix(html[i:], "<?"):
			i = skipUntil(html, i+2, ">")
			continue
		}

		name, closing, rest, ok := scanTag(html, i)
		if !ok {
			// A bare '<' that does not start a tag is literal text.
			w.text("<")
			i++
			continue
		}
		i = rest

		if dropTags[name] && !closing {
			i = skipElement(html, i, name)
			w.brk(1)
			continue
		}
		w.brk(breakLevel(name))
	}
	return w.b.String()
}

// scanTag reads the tag starting at html[i] (which must be '<'). It returns the
// lowercased element name, whether it was a closing tag, and the offset just
// past the tag. ok is false when the '<' does not begin a tag at all.
func scanTag(html string, i int) (name string, closing bool, next int, ok bool) {
	j := i + 1
	if j < len(html) && html[j] == '/' {
		closing = true
		j++
	}
	start := j
	for j < len(html) && (isAlnum(html[j]) || html[j] == '-' || html[j] == ':') {
		j++
	}
	if j == start {
		return "", false, i, false
	}
	name = strings.ToLower(html[start:j])
	if c := strings.IndexByte(name, ':'); c >= 0 {
		name = name[c+1:] // ignore XML namespace prefixes
	}
	return name, closing, skipAttrs(html, j), true
}

// skipAttrs advances past a tag's attributes to just after its '>', honouring
// quoted attribute values so that a '>' inside one does not end the tag early.
func skipAttrs(html string, i int) int {
	var quote byte
	for ; i < len(html); i++ {
		c := html[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return len(html)
}

// skipElement discards everything up to the matching close tag of name,
// counting nested openings of the same element. An unclosed element swallows
// the remainder of the fragment, which is what a browser does too.
func skipElement(html string, i int, name string) int {
	depth := 1
	for i < len(html) {
		lt := strings.IndexByte(html[i:], '<')
		if lt < 0 {
			return len(html)
		}
		i += lt
		if strings.HasPrefix(html[i:], "<!--") {
			i = skipUntil(html, i+4, "-->")
			continue
		}
		n, closing, next, ok := scanTag(html, i)
		if !ok {
			i++
			continue
		}
		i = next
		if n != name {
			continue
		}
		if closing {
			if depth--; depth == 0 {
				return i
			}
		} else if !voidTags[n] && !strings.HasSuffix(strings.TrimSpace(html[:i]), "/>") {
			depth++
		}
	}
	return i
}

func skipUntil(s string, i int, marker string) int {
	if idx := strings.Index(s[i:], marker); idx >= 0 {
		return i + idx + len(marker)
	}
	return len(s)
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Summarize returns at most maxRunes of text as a single paragraph. It prefers
// to end on a sentence boundary, falls back to a word boundary and only then
// cuts mid-word, appending an ellipsis whenever anything was dropped.
func Summarize(text string, maxRunes int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if maxRunes <= 0 || utf8.RuneCountInString(flat) <= maxRunes {
		return flat
	}

	// Byte offset of the maxRunes'th rune.
	cut, n := len(flat), 0
	for idx := range flat {
		if n == maxRunes {
			cut = idx
			break
		}
		n++
	}
	head := flat[:cut]

	// Prefer a sentence end, but not one so early that most of the budget is
	// thrown away.
	if s := lastSentenceEnd(head); s >= maxRunes/4 && s > 0 {
		return strings.TrimSpace(head[:s])
	}
	if sp := strings.LastIndexByte(head, ' '); sp > 0 {
		head = head[:sp]
	}
	return strings.TrimRight(strings.TrimSpace(head), ",;:") + "…"
}

// lastSentenceEnd returns the byte offset just past the last sentence-ending
// punctuation in s, or -1 if there is none. A period that is part of a common
// abbreviation or a decimal number does not count.
func lastSentenceEnd(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case '.', '!', '?':
			if i+1 < len(s) && !isSpaceByte(s[i+1]) {
				continue
			}
			if s[i] == '.' && i > 0 && isDigitByte(s[i-1]) {
				continue
			}
			return i + 1
		}
	}
	return -1
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }

// CountWords counts whitespace-separated words.
func CountWords(text string) int { return len(strings.Fields(text)) }

// ReadingMinutes estimates reading time in whole minutes, rounded up. Any
// non-empty text takes at least one minute; empty text takes none.
func ReadingMinutes(words int) int {
	if words <= 0 {
		return 0
	}
	return (words + wordsPerMinute - 1) / wordsPerMinute
}

// Entry extracts text, summary, word count and reading time from an entry
// body, preferring the full content and falling back to the summary field.
// A body that is empty yields a zero Result rather than an error.
func Entry(content, summary string) Result {
	body := content
	if strings.TrimSpace(body) == "" {
		body = summary
	}
	text := Plain(body)
	words := CountWords(text)
	sum := Summarize(text, DefaultSummaryLen)
	if strings.TrimSpace(sum) == "" && strings.TrimSpace(summary) != "" {
		sum = Summarize(Plain(summary), DefaultSummaryLen)
	}
	return Result{
		Text:           text,
		Summary:        sum,
		Words:          words,
		ReadingMinutes: ReadingMinutes(words),
	}
}
