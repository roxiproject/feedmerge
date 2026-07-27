package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roxiproject/feedmerge/internal/config"
	"github.com/roxiproject/feedmerge/internal/fetch"
)

func rssItem(guid, title, link, date string) string {
	return fmt.Sprintf(`<item><title>%s</title><link>%s</link><guid>%s</guid><pubDate>%s</pubDate>
		<description>Body of %s</description></item>`, title, link, guid, date, title)
}

func rssFeed(title string, items ...string) string {
	return `<?xml version="1.0"?><rss version="2.0"><channel><title>` + title +
		`</title><link>https://src.example/</link><description>d</description>` +
		strings.Join(items, "") + `</channel></rss>`
}

const atomFeed = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Source</title>
  <link rel="alternate" href="https://atom.example/"/>
  <updated>2024-05-03T00:00:00Z</updated>
  <entry>
    <title>Unique atom story</title>
    <id>urn:atom:1</id>
    <link rel="alternate" href="https://atom.example/1"/>
    <published>2024-05-03T00:00:00Z</published>
    <updated>2024-05-03T00:00:00Z</updated>
    <content type="html">&lt;p&gt;Atom body&lt;/p&gt;</content>
  </entry>
  <entry>
    <title>Shared headline about Go</title>
    <id>urn:atom:2</id>
    <link rel="alternate" href="https://shared.example/story"/>
    <published>2024-05-02T00:00:00Z</published>
    <updated>2024-05-02T00:00:00Z</updated>
  </entry>
</feed>`

// newTestSetup starts two upstream feed servers, one of which fails, and
// returns a server backed by them.
func newTestSetup(t *testing.T, extraFilters ...string) (*Server, *Merger, func()) {
	t.Helper()
	rss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, rssFeed("RSS Source",
			rssItem("urn:rss:1", "First RSS story", "https://rss.example/1", "Wed, 01 May 2024 10:00:00 GMT"),
			// Same story as the Atom feed's second entry, different guid and URL form.
			rssItem("urn:rss:2", "Shared headline about Go", "http://www.shared.example/story/?utm_source=rss", "Thu, 02 May 2024 00:00:00 GMT"),
			rssItem("urn:rss:3", "SPONSORED: buy things", "https://rss.example/ad", "Wed, 01 May 2024 09:00:00 GMT"),
		))
	}))
	atom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		fmt.Fprint(w, atomFeed)
	}))
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream is down", http.StatusBadGateway)
	}))

	src := "title: Test Merge\nlink: https://merged.example/\nself_link: https://merged.example/feed.xml\n" +
		"filters:\n"
	for _, f := range append([]string{"exclude title ~ /sponsored/i"}, extraFilters...) {
		src += "  - " + f + "\n"
	}
	src += fmt.Sprintf("feeds:\n  - url: %s/rss.xml\n    name: RSS Source\n  - url: %s/atom.xml\n  - url: %s/broken.xml\n",
		rss.URL, atom.URL, broken.URL)

	cfg, err := config.ParseYAML(src)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	m := NewMerger(cfg, fetch.New(5*time.Second, 0), nil)
	return NewServer(m), m, func() {
		rss.Close()
		atom.Close()
		broken.Close()
	}
}

func TestMergeDeduplicatesAndFilters(t *testing.T) {
	srv, m, cleanup := newTestSetup(t)
	defer cleanup()
	_ = srv

	snap, err := m.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if snap.Failures != 1 {
		t.Errorf("Failures = %d, want 1 (the broken upstream)", snap.Failures)
	}
	var titles []string
	for _, e := range snap.Raw {
		titles = append(titles, e.Title)
	}
	got := strings.Join(titles, "|")
	want := "Unique atom story|Shared headline about Go|First RSS story"
	if got != want {
		t.Errorf("merged titles = %q, want %q", got, want)
	}
	if snap.Entries != 3 {
		t.Errorf("Entries = %d", snap.Entries)
	}
	// The RSS copy is seen first, so its source name should be preserved.
	if snap.Raw[1].SourceTitle != "RSS Source" {
		t.Errorf("source name = %q", snap.Raw[1].SourceTitle)
	}
	if len(snap.Sources) != 3 {
		t.Fatalf("got %d source statuses", len(snap.Sources))
	}
	if !snap.Sources[0].OK || snap.Sources[0].Entries != 3 {
		t.Errorf("source 0 = %+v", snap.Sources[0])
	}
	if snap.Sources[2].OK || !strings.Contains(snap.Sources[2].Error, "502") {
		t.Errorf("failing source status = %+v", snap.Sources[2])
	}
}

func TestMergeFailsOnlyWhenEveryFeedFails(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer broken.Close()
	cfg, err := config.ParseYAML(fmt.Sprintf("feeds:\n  - %s/a\n  - %s/b\n", broken.URL, broken.URL))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	m := NewMerger(cfg, fetch.New(2*time.Second, 0), nil)
	if _, err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected Refresh to fail when no feed could be read")
	}
	if _, lastErr := m.LastRun(); lastErr == nil {
		t.Error("LastRun should report the failure")
	}

	rec := httptest.NewRecorder()
	NewServer(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/healthz status = %d, want 503", rec.Code)
	}
}

func TestEndpoints(t *testing.T) {
	srv, m, cleanup := newTestSetup(t)
	defer cleanup()
	if _, err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/feed.xml", "application/rss+xml", `<rss version="2.0"`},
		{"/feed.rss", "application/rss+xml", "<channel>"},
		{"/feed.atom", "application/atom+xml", `xmlns="http://www.w3.org/2005/Atom"`},
		{"/feed.json", "application/feed+json", `"version": "https://jsonfeed.org/version/1.1"`},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.contentType) {
				t.Errorf("Content-Type = %q, want %q", ct, tc.contentType)
			}
			if !strings.Contains(rec.Body.String(), tc.contains) {
				t.Errorf("body does not contain %q:\n%s", tc.contains, rec.Body.String())
			}
			if rec.Header().Get("ETag") == "" || rec.Header().Get("Last-Modified") == "" {
				t.Error("missing cache validators")
			}
			if rec.Header().Get("X-Feed-Entries") != "3" {
				t.Errorf("X-Feed-Entries = %q", rec.Header().Get("X-Feed-Entries"))
			}
		})
	}

	t.Run("etags differ per format", func(t *testing.T) {
		seen := map[string]string{}
		for _, p := range []string{"/feed.xml", "/feed.atom", "/feed.json"} {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			etag := rec.Header().Get("ETag")
			if prev, ok := seen[etag]; ok {
				t.Errorf("%s and %s share the ETag %s", prev, p, etag)
			}
			seen[etag] = p
		}
	})

	t.Run("head", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/feed.xml", nil))
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("HEAD status = %d with %d body bytes", rec.Code, rec.Body.Len())
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/feed.xml", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST status = %d", rec.Code)
		}
	})

	t.Run("index and 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/feed.atom") {
			t.Errorf("index status = %d body = %q", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("unknown path status = %d", rec.Code)
		}
	})
}

// TestInboundConditionalGET covers serving 304 for both validator styles.
func TestInboundConditionalGET(t *testing.T) {
	srv, m, cleanup := newTestSetup(t)
	defer cleanup()
	if _, err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/feed.xml", nil))
	etag := first.Header().Get("ETag")
	lastMod := first.Header().Get("Last-Modified")
	if etag == "" || lastMod == "" {
		t.Fatal("first response carried no validators")
	}

	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"if-none-match hit", map[string]string{"If-None-Match": etag}, http.StatusNotModified},
		{"if-none-match list", map[string]string{"If-None-Match": `"other", ` + etag}, http.StatusNotModified},
		{"if-none-match star", map[string]string{"If-None-Match": "*"}, http.StatusNotModified},
		{"if-none-match weak", map[string]string{"If-None-Match": "W/" + etag}, http.StatusNotModified},
		{"if-none-match miss", map[string]string{"If-None-Match": `"stale"`}, http.StatusOK},
		{"if-modified-since equal", map[string]string{"If-Modified-Since": lastMod}, http.StatusNotModified},
		{"if-modified-since later", map[string]string{
			"If-Modified-Since": time.Now().UTC().Add(time.Hour).Format(http.TimeFormat)}, http.StatusNotModified},
		{"if-modified-since earlier", map[string]string{
			"If-Modified-Since": time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat)}, http.StatusOK},
		{"if-modified-since malformed", map[string]string{"If-Modified-Since": "not a date"}, http.StatusOK},
		{"etag beats modified-since", map[string]string{
			"If-None-Match":     `"stale"`,
			"If-Modified-Since": lastMod,
		}, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if rec.Code == http.StatusNotModified && rec.Body.Len() != 0 {
				t.Errorf("304 response carried %d body bytes", rec.Body.Len())
			}
			if rec.Code == http.StatusNotModified && rec.Header().Get("ETag") != etag {
				t.Errorf("304 ETag = %q, want %q", rec.Header().Get("ETag"), etag)
			}
		})
	}

	// A conditional request against a different format must not 304.
	req := httptest.NewRequest(http.MethodGet, "/feed.json", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cross-format conditional GET returned %d, want 200", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	srv, m, cleanup := newTestSetup(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before the first refresh /healthz = %d, want 503", rec.Code)
	}

	if _, err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("healthz is not valid JSON: %v", err)
	}
	if resp.Status != "degraded" {
		t.Errorf("status = %q, want degraded (one upstream is down)", resp.Status)
	}
	if resp.Entries != 3 || resp.Failures != 1 || len(resp.Sources) != 3 {
		t.Errorf("health response = %+v", resp)
	}
	if resp.LastUpdate == "" {
		t.Error("last_update is empty")
	}
}

// The feed handlers must refresh on demand when no snapshot exists yet.
func TestFeedRefreshesOnDemand(t *testing.T) {
	srv, _, cleanup := newTestSetup(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feed.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Unique atom story") {
		t.Error("on-demand refresh produced no entries")
	}
}

func TestFeedUnavailableWhenAllUpstreamsFail(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer broken.Close()
	cfg, err := config.ParseYAML("feeds:\n  - " + broken.URL + "/a\n")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	srv := NewServer(NewMerger(cfg, fetch.New(2*time.Second, 0), nil))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feed.xml", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestRunRefreshesAndStops(t *testing.T) {
	_, m, cleanup := newTestSetup(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for m.Snapshot() == nil {
		select {
		case <-deadline:
			t.Fatal("Run never produced a snapshot")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
}

func TestMaxItemsTruncates(t *testing.T) {
	rss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := make([]string, 0, 5)
		for i := 0; i < 5; i++ {
			items = append(items, rssItem(
				fmt.Sprintf("urn:%d", i), fmt.Sprintf("Story number %d", i),
				fmt.Sprintf("https://s.example/%d", i),
				fmt.Sprintf("0%d May 2024 10:00:00 GMT", i+1)))
		}
		fmt.Fprint(w, rssFeed("Many", items...))
	}))
	defer rss.Close()

	cfg, err := config.ParseYAML("max_items: 2\nfeeds:\n  - " + rss.URL + "/f\n")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	m := NewMerger(cfg, fetch.New(2*time.Second, 0), nil)
	snap, err := m.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if snap.Entries != 2 {
		t.Errorf("Entries = %d, want 2", snap.Entries)
	}
	if snap.Raw[0].Title != "Story number 4" {
		t.Errorf("truncation kept the wrong entries: %q first", snap.Raw[0].Title)
	}
}

func TestEtagMatch(t *testing.T) {
	tests := []struct {
		header, etag string
		want         bool
	}{
		{`"a"`, `"a"`, true},
		{`W/"a"`, `"a"`, true},
		{`"a", "b"`, `"b"`, true},
		{`*`, `"a"`, true},
		{`"a"`, `"b"`, false},
		{`"a"`, "", false},
		{"", `"a"`, false},
	}
	for _, tc := range tests {
		if got := etagMatch(tc.header, tc.etag); got != tc.want {
			t.Errorf("etagMatch(%q, %q) = %v, want %v", tc.header, tc.etag, got, tc.want)
		}
	}
}
