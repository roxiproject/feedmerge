package feed

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleEntries() []Entry {
	return []Entry{
		{
			ID: "guid-1", IDSource: "guid",
			Title:      "Ampersands & <angle brackets>",
			Link:       "https://example.com/a?x=1&y=2",
			Content:    "<p>Body with <em>markup</em></p>",
			Summary:    "Plain summary",
			Author:     "Ada Lovelace",
			Categories: []string{"go", "xml"},
			Published:  time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC),
			Updated:    time.Date(2024, 5, 2, 10, 0, 0, 0, time.UTC),
		},
		{
			ID: "https://example.com/b", IDSource: "link",
			Title: "Second", Link: "https://example.com/b",
		},
	}
}

func sampleMeta() Meta {
	return Meta{
		Title:       "Merged",
		Link:        "https://example.com/",
		Description: "A merged feed",
		SelfLink:    "https://example.com/feed.xml",
		Updated:     time.Date(2024, 5, 2, 10, 0, 0, 0, time.UTC),
	}
}

func TestWriteRSSRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRSS(&buf, sampleMeta(), sampleEntries()); err != nil {
		t.Fatalf("WriteRSS: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "<?xml") {
		t.Error("missing XML declaration")
	}
	if !strings.Contains(out, `<rss version="2.0"`) {
		t.Error("missing rss root")
	}
	f, err := ParseBytes(buf.Bytes(), "https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("re-parsing our own RSS failed: %v", err)
	}
	if len(f.Entries) != 2 {
		t.Fatalf("round trip produced %d entries", len(f.Entries))
	}
	if f.Entries[0].Title != "Ampersands & <angle brackets>" {
		t.Errorf("title did not survive the round trip: %q", f.Entries[0].Title)
	}
	if f.Entries[0].ID != "guid-1" {
		t.Errorf("guid did not survive: %q", f.Entries[0].ID)
	}
	if f.Entries[0].Link != "https://example.com/a?x=1&y=2" {
		t.Errorf("link did not survive: %q", f.Entries[0].Link)
	}
	if want := "2024-05-01T10:00:00Z"; f.Entries[0].Published.UTC().Format(time.RFC3339) != want {
		t.Errorf("pubDate = %s, want %s", f.Entries[0].Published, want)
	}
}

func TestWriteAtomRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAtom(&buf, sampleMeta(), sampleEntries()); err != nil {
		t.Fatalf("WriteAtom: %v", err)
	}
	if !strings.Contains(buf.String(), `xmlns="http://www.w3.org/2005/Atom"`) {
		t.Error("missing the Atom namespace")
	}
	if !strings.Contains(buf.String(), `rel="self"`) {
		t.Error("missing rel=self link")
	}
	f, err := ParseBytes(buf.Bytes(), "https://example.com/feed.atom")
	if err != nil {
		t.Fatalf("re-parsing our own Atom failed: %v", err)
	}
	if f.Format != "atom" || len(f.Entries) != 2 {
		t.Fatalf("format %q with %d entries", f.Format, len(f.Entries))
	}
	e := f.Entries[0]
	if e.Title != "Ampersands & <angle brackets>" {
		t.Errorf("title = %q", e.Title)
	}
	if e.Content != "<p>Body with <em>markup</em></p>" {
		t.Errorf("content = %q", e.Content)
	}
	if e.Author != "Ada Lovelace" {
		t.Errorf("author = %q", e.Author)
	}
	if e.Updated.UTC().Format(time.RFC3339) != "2024-05-02T10:00:00Z" {
		t.Errorf("updated = %s", e.Updated)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleMeta(), sampleEntries()); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var doc struct {
		Version string `json:"version"`
		Title   string `json:"title"`
		FeedURL string `json:"feed_url"`
		Items   []struct {
			ID            string   `json:"id"`
			URL           string   `json:"url"`
			Title         string   `json:"title"`
			ContentHTML   string   `json:"content_html"`
			DatePublished string   `json:"date_published"`
			Tags          []string `json:"tags"`
			Authors       []struct {
				Name string `json:"name"`
			} `json:"authors"`
		} `json:"items"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.Version != "https://jsonfeed.org/version/1.1" {
		t.Errorf("version = %q", doc.Version)
	}
	if doc.FeedURL != "https://example.com/feed.xml" {
		t.Errorf("feed_url = %q", doc.FeedURL)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("got %d items", len(doc.Items))
	}
	it := doc.Items[0]
	if it.Title != "Ampersands & <angle brackets>" || it.ID != "guid-1" {
		t.Errorf("item = %+v", it)
	}
	if it.DatePublished != "2024-05-01T10:00:00Z" {
		t.Errorf("date_published = %q", it.DatePublished)
	}
	if len(it.Tags) != 2 || len(it.Authors) != 1 {
		t.Errorf("tags = %v authors = %v", it.Tags, it.Authors)
	}
}

func TestWriteEmptyFeeds(t *testing.T) {
	m := sampleMeta()
	for name, fn := range map[string]func(*bytes.Buffer) error{
		"rss":  func(b *bytes.Buffer) error { return WriteRSS(b, m, nil) },
		"atom": func(b *bytes.Buffer) error { return WriteAtom(b, m, nil) },
		"json": func(b *bytes.Buffer) error { return WriteJSON(b, m, nil) },
	} {
		var buf bytes.Buffer
		if err := fn(&buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("%s: produced no output", name)
		}
	}
}
