package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func rssDoc(title string, items ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel>`)
	fmt.Fprintf(&b, "<title>%s</title><link>https://src.example/</link>", title)
	for _, it := range items {
		fmt.Fprintf(&b, `<item><title>%s</title><link>https://src.example/%s</link>
			<guid>%s-%s</guid><pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate></item>`, it, it, title, it)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func testFetcher() *Fetcher {
	f := New(5*time.Second, 0)
	return f
}

func TestFetchParsesFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, rssDoc("Alpha", "one", "two"))
	}))
	defer srv.Close()

	f := testFetcher()
	fd, notMod, status, err := f.Fetch(context.Background(), srv.URL+"/feed.xml")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if notMod || status != 200 {
		t.Errorf("notModified = %v, status = %d", notMod, status)
	}
	if fd.Title != "Alpha" || len(fd.Entries) != 2 {
		t.Errorf("feed = %q with %d entries", fd.Title, len(fd.Entries))
	}
}

func TestFetchSendsUserAgentAndAccept(t *testing.T) {
	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		fmt.Fprint(w, rssDoc("Alpha", "one"))
	}))
	defer srv.Close()

	f := testFetcher()
	f.UserAgent = "feedmerge-test/1"
	if _, _, _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotUA != "feedmerge-test/1" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if !strings.Contains(gotAccept, "rss+xml") {
		t.Errorf("Accept = %q", gotAccept)
	}
}

// TestOutboundConditionalGET checks that a second fetch revalidates with the
// validators from the first response and reuses the cached parse on 304.
func TestOutboundConditionalGET(t *testing.T) {
	const etag = `"v1"`
	lastMod := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).Format(http.TimeFormat)

	var (
		hits          int32
		sawINM        string
		sawIMS        string
		secondRequest = make(chan struct{}, 1)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastMod)
		if n > 1 {
			sawINM = r.Header.Get("If-None-Match")
			sawIMS = r.Header.Get("If-Modified-Since")
			secondRequest <- struct{}{}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fmt.Fprint(w, rssDoc("Cached", "one"))
	}))
	defer srv.Close()

	f := testFetcher()
	url := srv.URL + "/feed.xml"
	first, notMod, _, err := f.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if notMod {
		t.Error("first fetch reported not-modified")
	}

	second, notMod, status, err := f.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	<-secondRequest
	if !notMod || status != http.StatusNotModified {
		t.Errorf("second fetch: notModified = %v, status = %d", notMod, status)
	}
	if sawINM != etag {
		t.Errorf("If-None-Match = %q, want %q", sawINM, etag)
	}
	if sawIMS != lastMod {
		t.Errorf("If-Modified-Since = %q, want %q", sawIMS, lastMod)
	}
	if second == nil || second.Title != first.Title || len(second.Entries) != len(first.Entries) {
		t.Error("304 did not return the cached parse")
	}
	if f.Cache.Len() != 1 {
		t.Errorf("cache has %d entries", f.Cache.Len())
	}
}

func TestFetch304WithoutCacheIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	if _, _, _, err := testFetcher().Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected an error for an unsolicited 304")
	}
}

func TestFetchErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"404", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }},
		{"not a feed", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>nope</html>") }},
		{"empty body", func(w http.ResponseWriter, r *http.Request) {}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			if _, _, _, err := testFetcher().Fetch(context.Background(), srv.URL); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestHTTPErrorMessage(t *testing.T) {
	err := &HTTPError{URL: "https://x.example/f", Status: 503}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestFetchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	f := New(50*time.Millisecond, 0)
	_, _, _, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !IsTimeout(err) {
		t.Errorf("IsTimeout(%v) = false", err)
	}
}

func TestFetchInvalidURL(t *testing.T) {
	if _, _, _, err := testFetcher().Fetch(context.Background(), "://not a url"); err == nil {
		t.Fatal("expected an error for a malformed URL")
	}
}

// TestFetchAllContinuesPastFailures is the key resilience property: one broken
// feed must not prevent the others from being merged.
func TestFetchAllContinuesPastFailures(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssDoc("Good", strings.TrimPrefix(r.URL.Path, "/")))
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{\"not\": \"a feed\"}")
	}))
	defer garbage.Close()

	sources := []Source{
		{URL: ok.URL + "/a", Name: "A"},
		{URL: bad.URL + "/b", Name: "B"},
		{URL: ok.URL + "/c", Name: "C"},
		{URL: garbage.URL + "/d", Name: "D"},
		{URL: "http://127.0.0.1:1/e", Name: "E"},
	}
	results := New(2*time.Second, 0).FetchAll(context.Background(), sources, 3)
	if len(results) != len(sources) {
		t.Fatalf("got %d results", len(results))
	}
	for i, r := range results {
		if r.URL != sources[i].URL || r.Name != sources[i].Name {
			t.Errorf("result %d out of order: %+v", i, r)
		}
	}
	if results[0].Err != nil || results[2].Err != nil {
		t.Errorf("healthy feeds failed: %v / %v", results[0].Err, results[2].Err)
	}
	if results[0].Feed == nil || len(results[0].Feed.Entries) != 1 {
		t.Errorf("feed A did not parse: %+v", results[0].Feed)
	}
	for _, i := range []int{1, 3, 4} {
		if results[i].Err == nil {
			t.Errorf("source %d should have failed", i)
		}
		if results[i].Feed != nil {
			t.Errorf("source %d returned a feed despite failing", i)
		}
	}
}

func TestFetchAllConcurrencyIsBounded(t *testing.T) {
	var (
		mu      sync.Mutex
		current int
		peak    int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		current++
		if current > peak {
			peak = current
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		current--
		mu.Unlock()
		fmt.Fprint(w, rssDoc("S", "one"))
	}))
	defer srv.Close()

	var sources []Source
	for i := 0; i < 12; i++ {
		sources = append(sources, Source{URL: fmt.Sprintf("%s/f%d", srv.URL, i)})
	}
	f := New(5*time.Second, 0)
	f.Limiter = nil // measure the worker pool, not the rate limiter
	results := f.FetchAll(context.Background(), sources, 3)
	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("source %d failed: %v", i, r.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 3 {
		t.Errorf("peak concurrency %d exceeds the worker count 3", peak)
	}
	if peak < 2 {
		t.Errorf("peak concurrency %d suggests requests were serialized", peak)
	}
}

func TestFetchAllEmptyAndCancelled(t *testing.T) {
	f := New(time.Second, 0)
	if got := f.FetchAll(context.Background(), nil, 4); len(got) != 0 {
		t.Errorf("empty source list produced %d results", len(got))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, rssDoc("S", "one"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := f.FetchAll(ctx, []Source{{URL: srv.URL + "/a"}, {URL: srv.URL + "/b"}}, 1)
	for i, r := range results {
		if r.Err == nil {
			t.Errorf("result %d succeeded despite a cancelled context", i)
		}
	}
}

func TestIsTimeout(t *testing.T) {
	if IsTimeout(nil) {
		t.Error("IsTimeout(nil) should be false")
	}
	if !IsTimeout(context.DeadlineExceeded) {
		t.Error("DeadlineExceeded should count as a timeout")
	}
	if !IsTimeout(context.Canceled) {
		t.Error("Canceled should count as a timeout")
	}
	if IsTimeout(fmt.Errorf("unrelated")) {
		t.Error("unrelated errors should not count as timeouts")
	}
}
