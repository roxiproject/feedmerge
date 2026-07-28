package opml

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Subscriptions</title></head>
  <body>
    <outline text="Go">
      <outline type="rss" text="The Go Blog" title="The Go Blog"
        xmlUrl="https://go.dev/blog/feed.atom" htmlUrl="https://go.dev/blog"/>
      <outline type="rss" text="Go Release Notes" xmlUrl="https://go.dev/doc/devel/release.rss"/>
    </outline>
    <outline type="rss" text="PostgreSQL News" xmlUrl="https://www.postgresql.org/news.rss"/>
  </body>
</opml>`

func urls(subs []Subscription) []string {
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.URL
	}
	return out
}

func TestParse(t *testing.T) {
	subs, err := ParseString(sample)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("got %d subscriptions, want 3: %v", len(subs), urls(subs))
	}
	first := subs[0]
	if first.Name != "The Go Blog" || first.URL != "https://go.dev/blog/feed.atom" {
		t.Errorf("first subscription = %+v", first)
	}
	if first.SiteURL != "https://go.dev/blog" {
		t.Errorf("htmlUrl = %q", first.SiteURL)
	}
	if first.Folder != "Go" {
		t.Errorf("folder = %q, want Go", first.Folder)
	}
	if subs[2].Folder != "" {
		t.Errorf("top-level feed got folder %q", subs[2].Folder)
	}
	// text is the fallback when title is absent.
	if subs[1].Name != "Go Release Notes" {
		t.Errorf("name fallback = %q", subs[1].Name)
	}
}

func TestParseNestedFolders(t *testing.T) {
	src := `<opml version="2.0"><body>
	  <outline text="Tech"><outline text="Databases">
	    <outline type="rss" text="PG" xmlUrl="https://pg.example/feed"/>
	  </outline></outline></body></opml>`
	subs, err := ParseString(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if subs[0].Folder != "Tech/Databases" {
		t.Errorf("folder = %q, want Tech/Databases", subs[0].Folder)
	}
}

func TestParseDropsDuplicateURLs(t *testing.T) {
	src := `<opml version="2.0"><body>
	  <outline type="rss" text="One" xmlUrl="https://a.example/feed"/>
	  <outline text="Folder"><outline type="rss" text="Copy" xmlUrl="https://a.example/feed"/></outline>
	  </body></opml>`
	subs, err := ParseString(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(subs) != 1 || subs[0].Name != "One" {
		t.Errorf("duplicate url survived: %+v", subs)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"not opml", `<rss version="2.0"><channel/></rss>`, "not <opml>"},
		{"no feeds", `<opml version="2.0"><body><outline text="Empty folder"/></body></opml>`, "no feeds"},
		{"malformed", `<opml><body><outline`, "opml:"},
		{"empty input", ``, "opml:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString(tt.src)
			if err == nil {
				t.Fatalf("Parse succeeded, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestWrite(t *testing.T) {
	subs := []Subscription{
		{Name: "The Go Blog", URL: "https://go.dev/blog/feed.atom", SiteURL: "https://go.dev/blog", Folder: "Go"},
		{Name: "PostgreSQL News", URL: "https://www.postgresql.org/news.rss"},
	}
	var buf bytes.Buffer
	when := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	if err := Write(&buf, "My feeds", when, subs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<opml version="2.0">`,
		`<title>My feeds</title>`,
		`xmlUrl="https://go.dev/blog/feed.atom"`,
		`htmlUrl="https://go.dev/blog"`,
		`27 Jul 2026 12:00:00 +0000`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	subs, err := ParseString(sample)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var buf bytes.Buffer
	if err := Write(&buf, "Subscriptions", time.Time{}, subs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	again, err := Parse(&buf)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if len(again) != len(subs) {
		t.Fatalf("round trip changed the count: %d then %d", len(subs), len(again))
	}
	for i := range subs {
		if subs[i] != again[i] {
			t.Errorf("subscription %d changed:\n%+v\n%+v", i, subs[i], again[i])
		}
	}
}

func TestWriteFallsBackToTheURLForUnnamedFeeds(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "t", time.Time{}, []Subscription{{URL: "https://a.example/feed"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(buf.String(), `text="https://a.example/feed"`) {
		t.Errorf("unnamed feed lost its label:\n%s", buf.String())
	}
}
