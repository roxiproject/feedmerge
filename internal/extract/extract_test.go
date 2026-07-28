package extract

import (
	"strings"
	"testing"
)

func TestPlain(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text passes through", "just words", "just words"},
		{"tags become text", "<p>Hello <b>world</b></p>", "Hello world"},
		{"entities decoded", "<p>AT&amp;T caf&eacute;</p>", "AT&T café"},
		{"script contents dropped", `<p>Keep</p><script>var x = 1 < 2;</script>`, "Keep"},
		{"style contents dropped", "<style>p{color:red}</style><p>Body</p>", "Body"},
		{"nav and footer dropped", "<nav>Home About</nav><p>Story</p><footer>(c) 2026</footer>", "Story"},
		{"nested drop tag", "<nav>a<nav>b</nav>c</nav><p>x</p>", "x"},
		{"unclosed drop tag swallows rest", "<p>before</p><script>junk", "before"},
		{"br is a newline", "one<br>two", "one\ntwo"},
		{"paragraphs separated by blank line", "<p>one</p><p>two</p>", "one\n\ntwo"},
		{"list items on their own lines", "<ul><li>a</li><li>b</li></ul>", "a\nb"},
		{"comments removed", "a<!-- hidden <p>x</p> -->b", "ab"},
		{"cdata unwrapped", "<p><![CDATA[raw <text>]]></p>", "raw <text>"},
		{"doctype removed", "<!DOCTYPE html><p>x</p>", "x"},
		{"attribute with angle bracket", `<a href="?a=1>2">link</a>`, "link"},
		{"bare less-than kept", "5 < 6 and 7 > 6", "5 < 6 and 7 > 6"},
		{"namespaced tag", "<x:p>ns</x:p>", "ns"},
		{"whitespace collapsed", "<p>  a \n\n   b  </p>", "a b"},
		{"empty input", "", ""},
		{"markup only", "<p></p><div></div>", ""},
		{"self closing image ignored", `<p>a<img src="x.png"/>b</p>`, "ab"},
		{"iframe dropped", `<iframe src="x"></iframe><p>after</p>`, "after"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Plain(tt.in); got != tt.want {
				t.Errorf("Plain(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPlainDoesNotPanicOnTruncatedMarkup(t *testing.T) {
	inputs := []string{"<", "</", "<p", "<!--", "<![CDATA[", "<a href=\"", "<script", "<<<>>>", "&#;", "&#x110000;"}
	for _, in := range inputs {
		if got := Plain(in); strings.Contains(got, "\x00") {
			t.Errorf("Plain(%q) = %q", in, got)
		}
	}
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"shorter than limit", "a short body", 100, "a short body"},
		{"newlines flattened", "one\n\ntwo", 100, "one two"},
		{"zero limit means no limit", "anything at all", 0, "anything at all"},
		{"sentence boundary preferred", "First sentence here. Second sentence runs on and on and on.", 40, "First sentence here."},
		{"word boundary fallback", "supercalifragilistic wordsmithing beyond", 25, "supercalifragilistic…"},
		{"decimal point is not a sentence end", "Version 1.5 shipped today with many notable changes.", 30, "Version 1.5 shipped today…"},
		{"empty stays empty", "", 20, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Summarize(tt.in, tt.max); got != tt.want {
				t.Errorf("Summarize(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

func TestSummarizeRespectsRuneLimit(t *testing.T) {
	body := strings.Repeat("é ", 400)
	got := Summarize(body, 50)
	if n := len([]rune(got)); n > 51 { // 50 plus the ellipsis
		t.Errorf("summary is %d runes, want <= 51: %q", n, got)
	}
}

func TestReadingMinutes(t *testing.T) {
	tests := []struct {
		words int
		want  int
	}{{0, 0}, {-5, 0}, {1, 1}, {200, 1}, {201, 2}, {1000, 5}, {1001, 6}}
	for _, tt := range tests {
		if got := ReadingMinutes(tt.words); got != tt.want {
			t.Errorf("ReadingMinutes(%d) = %d, want %d", tt.words, got, tt.want)
		}
	}
}

func TestEntry(t *testing.T) {
	body := "<div><nav>skip me</nav><p>" + strings.Repeat("word ", 300) + "</p></div>"
	got := Entry(body, "fallback summary")
	if got.Words != 300 {
		t.Errorf("Words = %d, want 300", got.Words)
	}
	if got.ReadingMinutes != 2 {
		t.Errorf("ReadingMinutes = %d, want 2", got.ReadingMinutes)
	}
	if strings.Contains(got.Text, "skip me") {
		t.Errorf("boilerplate survived: %q", got.Text[:40])
	}
	if n := len([]rune(got.Summary)); n == 0 || n > DefaultSummaryLen+1 {
		t.Errorf("summary length %d out of range", n)
	}
}

func TestEntryFallsBackToSummaryField(t *testing.T) {
	got := Entry("   ", "<p>Only the summary exists</p>")
	if got.Text != "Only the summary exists" {
		t.Errorf("Text = %q", got.Text)
	}
	if got.Summary != "Only the summary exists" {
		t.Errorf("Summary = %q", got.Summary)
	}
	if got.ReadingMinutes != 1 {
		t.Errorf("ReadingMinutes = %d, want 1", got.ReadingMinutes)
	}
}

func TestEntryEmpty(t *testing.T) {
	got := Entry("", "")
	if got != (Result{}) {
		t.Errorf("Entry(\"\", \"\") = %+v, want zero", got)
	}
}
