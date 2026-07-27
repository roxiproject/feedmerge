package feed

import (
	"strings"
	"testing"
	"time"
)

const rssSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"
     xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>Example Weekly</title>
    <link>https://example.com/blog</link>
    <description>News &amp; notes</description>
    <lastBuildDate>Mon, 02 Jan 2006 15:04:05 GMT</lastBuildDate>
    <item>
      <title><![CDATA[Shipping <b>fast</b> & often]]></title>
      <link>/blog/shipping-fast</link>
      <guid isPermaLink="false">tag:example.com,2006:post-1</guid>
      <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
      <dc:creator>Ada Lovelace</dc:creator>
      <category>engineering</category>
      <category>process</category>
      <description>A short summary.</description>
      <content:encoded><![CDATA[<p>The full post body.</p>]]></content:encoded>
    </item>
    <item>
      <title>AT&amp;amp;T buys a router</title>
      <link>https://example.com/blog/att</link>
      <pubDate>Tue, 03 Jan 2006 09:00:00 GMT</pubDate>
    </item>
    <item>
      <title>No guid and no link</title>
      <pubDate>Wed, 04 Jan 2006 09:00:00 GMT</pubDate>
    </item>
    <item>
      <title>Permalink guid only</title>
      <guid>https://example.com/blog/permalink-guid</guid>
      <pubDate>Thu, 05 Jan 2006 09:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

func TestParseRSS(t *testing.T) {
	f, err := ParseBytes([]byte(rssSample), "https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if f.Format != "rss" {
		t.Errorf("Format = %q, want rss", f.Format)
	}
	if f.Title != "Example Weekly" {
		t.Errorf("Title = %q", f.Title)
	}
	if f.Description != "News & notes" {
		t.Errorf("Description = %q, want entity-decoded", f.Description)
	}
	if want := "2006-01-02T15:04:05Z"; f.Updated.UTC().Format(time.RFC3339) != want {
		t.Errorf("Updated = %s, want %s", f.Updated, want)
	}
	if len(f.Entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(f.Entries))
	}

	e := f.Entries[0]
	if e.Title != "Shipping <b>fast</b> & often" {
		t.Errorf("CDATA title = %q", e.Title)
	}
	if e.Link != "https://example.com/blog/shipping-fast" {
		t.Errorf("relative link = %q, want resolved against the channel link", e.Link)
	}
	if e.ID != "tag:example.com,2006:post-1" || e.IDSource != "guid" {
		t.Errorf("ID = %q (%s), want the guid", e.ID, e.IDSource)
	}
	if e.Content != "<p>The full post body.</p>" {
		t.Errorf("content:encoded = %q", e.Content)
	}
	if e.Summary != "A short summary." {
		t.Errorf("Summary = %q", e.Summary)
	}
	if e.Author != "Ada Lovelace" {
		t.Errorf("Author = %q", e.Author)
	}
	if strings.Join(e.Categories, ",") != "engineering,process" {
		t.Errorf("Categories = %v", e.Categories)
	}
	if want := "2006-01-02T22:04:05Z"; e.Published.UTC().Format(time.RFC3339) != want {
		t.Errorf("Published = %s, want %s", e.Published, want)
	}

	if got := f.Entries[1].Title; got != "AT&T buys a router" {
		t.Errorf("double-escaped title = %q, want %q", got, "AT&T buys a router")
	}
	if f.Entries[1].IDSource != "link" || f.Entries[1].ID != "https://example.com/blog/att" {
		t.Errorf("entry 2 ID = %q (%s), want link fallback", f.Entries[1].ID, f.Entries[1].IDSource)
	}
	if f.Entries[2].IDSource != "hash" || !strings.HasPrefix(f.Entries[2].ID, "urn:feedmerge:") {
		t.Errorf("entry 3 ID = %q (%s), want hash fallback", f.Entries[2].ID, f.Entries[2].IDSource)
	}
	if got := f.Entries[3].Link; got != "https://example.com/blog/permalink-guid" {
		t.Errorf("permalink guid was not used as the link: %q", got)
	}
}

const atomSample = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xml:base="https://atom.example/">
  <title>Atom Example</title>
  <subtitle type="text">Everything, atomically</subtitle>
  <link rel="self" href="/feed.atom"/>
  <link rel="alternate" href="/index.html"/>
  <updated>2021-11-03T08:15:30Z</updated>
  <entry>
    <title type="html">Caf&amp;eacute; &amp; croissants</title>
    <id>urn:uuid:1225c695-cfb8-4ebb-aaaa-80da344efa6a</id>
    <link rel="alternate" href="posts/cafe"/>
    <link rel="edit" href="edit/cafe"/>
    <published>2021-11-01T09:00:00Z</published>
    <updated>2021-11-02T09:00:00Z</updated>
    <author><name>Grace Hopper</name></author>
    <category term="food" label="Food"/>
    <summary>Short.</summary>
    <content type="html"><![CDATA[<p>Long &amp; detailed.</p>]]></content>
  </entry>
  <entry xml:base="https://other.example/section/">
    <title>Relative to entry base</title>
    <link href="deep/page.html"/>
    <updated>2021-10-01T00:00:00Z</updated>
  </entry>
  <entry>
    <title type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml">Inline <b>XHTML</b></div></title>
    <link rel="alternate" href="https://atom.example/xhtml"/>
    <updated>2021-09-01T00:00:00Z</updated>
  </entry>
</feed>`

func TestParseAtom(t *testing.T) {
	f, err := ParseBytes([]byte(atomSample), "https://atom.example/feed.atom")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if f.Format != "atom" {
		t.Errorf("Format = %q, want atom", f.Format)
	}
	if f.Title != "Atom Example" || f.Description != "Everything, atomically" {
		t.Errorf("title/subtitle = %q / %q", f.Title, f.Description)
	}
	if f.Link != "https://atom.example/index.html" {
		t.Errorf("feed link = %q, want the rel=alternate link", f.Link)
	}
	if len(f.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(f.Entries))
	}

	e := f.Entries[0]
	if e.Title != "Café & croissants" {
		t.Errorf("entity title = %q", e.Title)
	}
	if e.ID != "urn:uuid:1225c695-cfb8-4ebb-aaaa-80da344efa6a" || e.IDSource != "guid" {
		t.Errorf("ID = %q (%s)", e.ID, e.IDSource)
	}
	if e.Link != "https://atom.example/posts/cafe" {
		t.Errorf("link = %q, want resolved rel=alternate", e.Link)
	}
	if e.Content != "<p>Long &amp; detailed.</p>" {
		t.Errorf("content = %q", e.Content)
	}
	if e.Author != "Grace Hopper" {
		t.Errorf("author = %q", e.Author)
	}
	if len(e.Categories) != 1 || e.Categories[0] != "Food" {
		t.Errorf("categories = %v", e.Categories)
	}
	if e.Published.UTC().Format(time.RFC3339) != "2021-11-01T09:00:00Z" ||
		e.Updated.UTC().Format(time.RFC3339) != "2021-11-02T09:00:00Z" {
		t.Errorf("published/updated = %s / %s", e.Published, e.Updated)
	}

	if got := f.Entries[1].Link; got != "https://other.example/section/deep/page.html" {
		t.Errorf("xml:base on entry ignored: %q", got)
	}
	if got := f.Entries[2].Title; !strings.Contains(got, "<b>XHTML</b>") {
		t.Errorf("xhtml title = %q", got)
	}
	if f.Entries[1].Published.IsZero() {
		t.Error("entry without <published> should fall back to <updated>")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"empty", ""},
		{"whitespace", "   \n\t"},
		{"not xml", "this is not a feed"},
		{"html", "<html><body>hello</body></html>"},
		{"rdf", `<?xml version="1.0"?><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"></rdf:RDF>`},
		{"truncated", `<rss version="2.0"><channel><title>x</title>`},
		{"bad charset", `<?xml version="1.0" encoding="Shift_JIS"?><rss version="2.0"><channel></channel></rss>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if f, err := ParseBytes([]byte(tc.doc), "https://x.example/f"); err == nil {
				t.Fatalf("expected an error, got feed %+v", f)
			}
		})
	}
}

func TestParseHandlesBOMAndLatin1(t *testing.T) {
	doc := "\xef\xbb\xbf" + `<?xml version="1.0" encoding="iso-8859-1"?>
<rss version="2.0"><channel><title>Caf` + "\xe9" + `</title><link>https://l1.example/</link>
<item><title>Se` + "\xf1" + `or</title><link>https://l1.example/a</link></item></channel></rss>`
	f, err := ParseBytes([]byte(doc), "https://l1.example/feed.xml")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if f.Title != "Café" {
		t.Errorf("title = %q, want Café", f.Title)
	}
	if f.Entries[0].Title != "Señor" {
		t.Errorf("item title = %q", f.Entries[0].Title)
	}
}

func TestParseUnparsableDateKeepsRaw(t *testing.T) {
	doc := `<rss version="2.0"><channel><title>t</title><link>https://d.example/</link>
<item><title>i</title><link>https://d.example/i</link><pubDate>whenever</pubDate></item></channel></rss>`
	f, err := ParseBytes([]byte(doc), "https://d.example/feed.xml")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	e := f.Entries[0]
	if !e.Published.IsZero() {
		t.Errorf("Published = %v, want zero for an unparsable date", e.Published)
	}
	if e.PublishedRaw != "whenever" {
		t.Errorf("PublishedRaw = %q", e.PublishedRaw)
	}
}

func TestResolve(t *testing.T) {
	tests := []struct{ base, ref, want string }{
		{"https://a.example/blog/feed.xml", "/post/1", "https://a.example/post/1"},
		{"https://a.example/blog/feed.xml", "post/1", "https://a.example/blog/post/1"},
		{"https://a.example/blog/feed.xml", "https://b.example/x", "https://b.example/x"},
		{"https://a.example/blog/feed.xml", "//cdn.example/x", "https://cdn.example/x"},
		{"", "/post/1", "/post/1"},
		{"https://a.example/", "", ""},
	}
	for _, tc := range tests {
		if got := resolve(tc.base, tc.ref); got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.base, tc.ref, got, tc.want)
		}
	}
}

func TestDecodeEntitiesAndStripTags(t *testing.T) {
	tests := []struct{ in, wantDecoded, wantStripped string }{
		{"plain", "plain", "plain"},
		{"AT&amp;T", "AT&T", "AT&T"},
		{"Caf&eacute;", "Café", "Café"},
		{"&#8212;dash", "—dash", "—dash"},
		{"&#x2014;dash", "—dash", "—dash"},
		{"&notarealentity;", "&notarealentity;", "&notarealentity;"},
		{"<b>bold</b>  text", "<b>bold</b>  text", "bold text"},
	}
	for _, tc := range tests {
		if got := DecodeEntities(tc.in); got != tc.wantDecoded {
			t.Errorf("DecodeEntities(%q) = %q, want %q", tc.in, got, tc.wantDecoded)
		}
		if got := StripTags(tc.in); got != tc.wantStripped {
			t.Errorf("StripTags(%q) = %q, want %q", tc.in, got, tc.wantStripped)
		}
	}
}

func TestParseReader(t *testing.T) {
	f, err := Parse(strings.NewReader(rssSample), "https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Entries) != 4 {
		t.Fatalf("got %d entries", len(f.Entries))
	}
}
