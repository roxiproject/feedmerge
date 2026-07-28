package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roxiproject/feedmerge/internal/config"
)

func getSaved(t *testing.T, srv *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestSavedSearchFeedFormats(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	tests := []struct {
		path     string
		ctype    string
		contains string
	}{
		{"/saved/go.xml", "application/rss+xml", `<rss version="2.0"`},
		{"/saved/go.rss", "application/rss+xml", `<rss version="2.0"`},
		{"/saved/go.atom", "application/atom+xml", `xmlns="http://www.w3.org/2005/Atom"`},
		{"/saved/go.json", "application/feed+json", `"version": "https://jsonfeed.org/version/1.1"`},
		{"/saved/go", "application/rss+xml", `<rss version="2.0"`},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := getSaved(t, srv, tt.path)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, tt.ctype) {
				t.Errorf("Content-Type = %q, want %q", ct, tt.ctype)
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.contains) {
				t.Errorf("body is not %s:\n%s", tt.ctype, body)
			}
			if !strings.Contains(body, "Go stories") {
				t.Errorf("feed does not carry the saved search title:\n%s", body)
			}
			if !strings.Contains(body, "Shared headline about Go") {
				t.Errorf("feed does not carry the matching entry:\n%s", body)
			}
			if strings.Contains(body, "Unique atom story") {
				t.Errorf("feed carries a non-matching entry:\n%s", body)
			}
		})
	}
}

func TestSavedSearchHonoursItsLimit(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w := getSaved(t, srv, "/saved/capped.xml")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("X-Feed-Entries"); got != "1" {
		t.Errorf("X-Feed-Entries = %q, want 1", got)
	}
	if n := strings.Count(w.Body.String(), "<item>"); n != 1 {
		t.Errorf("feed carries %d items, want 1", n)
	}
}

func TestSavedSearchIndex(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w := getSaved(t, srv, "/saved")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Count    int               `json:"count"`
		Searches []savedIndexEntry `json:"searches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding listing: %v\n%s", err, w.Body.String())
	}
	if got.Count != 2 || len(got.Searches) != 2 {
		t.Fatalf("listing = %+v", got)
	}
	first := got.Searches[0]
	if first.Name != "go" || first.Title != "Go stories" || first.Query != "go headline" {
		t.Errorf("first entry = %+v", first)
	}
	if first.RSS != "/saved/go.xml" || first.Atom != "/saved/go.atom" || first.JSON != "/saved/go.json" {
		t.Errorf("listing links = %+v", first)
	}
	// A limit of zero is left out rather than published as a cap of none.
	if first.Limit != 0 || got.Searches[1].Limit != 1 {
		t.Errorf("limits = %d and %d", first.Limit, got.Searches[1].Limit)
	}
}

func TestSavedSearchNotFound(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	for _, target := range []string{"/saved/missing.xml", "/saved/missing", "/saved/go.txt", "/saved/nested/go.xml"} {
		if code := getSaved(t, srv, target).Code; code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, code)
		}
	}
}

func TestSavedSearchConditionalGET(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w := getSaved(t, srv, "/saved/go.atom")
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a saved search feed")
	}
	req := httptest.NewRequest(http.MethodGet, "/saved/go.atom", nil)
	req.Header.Set("If-None-Match", etag)
	again := httptest.NewRecorder()
	srv.ServeHTTP(again, req)
	if again.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", again.Code)
	}

	// The RSS rendering of the same search must not share the tag.
	other := httptest.NewRequest(http.MethodGet, "/saved/go.xml", nil)
	other.Header.Set("If-None-Match", etag)
	ow := httptest.NewRecorder()
	srv.ServeHTTP(ow, other)
	if ow.Code != http.StatusOK {
		t.Errorf("other representation = %d, want 200", ow.Code)
	}
}

func TestSavedSearchMethodNotAllowed(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/saved/go.xml", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestSplitSavedPath(t *testing.T) {
	tests := []struct {
		path, name, format string
		ok                 bool
	}{
		{"/saved/go.xml", "go", "rss", true},
		{"/saved/go.rss", "go", "rss", true},
		{"/saved/go.atom", "go", "atom", true},
		{"/saved/go.json", "go", "json", true},
		{"/saved/go", "go", "rss", true},
		{"/saved/go.txt", "", "", false},
		{"/saved/", "", "", false},
		{"/saved/a/b.xml", "", "", false},
		{"/feed.xml", "", "", false},
	}
	for _, tt := range tests {
		name, format, ok := splitSavedPath(tt.path)
		if name != tt.name || format != tt.format || ok != tt.ok {
			t.Errorf("splitSavedPath(%q) = %q, %q, %v; want %q, %q, %v",
				tt.path, name, format, ok, tt.name, tt.format, tt.ok)
		}
	}
}

func TestSavedSelfLink(t *testing.T) {
	const base = "https://merged.example/feed.xml"
	tests := []struct {
		base, name, format, want string
	}{
		{base, "go", "rss", "https://merged.example/saved/go.xml"},
		{base, "go", "atom", "https://merged.example/saved/go.atom"},
		{base, "go", "json", "https://merged.example/saved/go.json"},
		{"", "go", "rss", ""},
		{"://broken", "go", "rss", ""},
	}
	for _, tt := range tests {
		if got := savedSelfLink(tt.base, tt.name, tt.format); got != tt.want {
			t.Errorf("savedSelfLink(%q, %q, %q) = %q, want %q", tt.base, tt.name, tt.format, got, tt.want)
		}
	}
}

func TestFindSearch(t *testing.T) {
	searches := []config.SavedSearch{{Name: "go", Query: "golang"}, {Name: "pg", Query: "postgres"}}
	if got, ok := findSearch(searches, "pg"); !ok || got.Query != "postgres" {
		t.Errorf("findSearch(pg) = %+v, %v", got, ok)
	}
	if _, ok := findSearch(searches, "rust"); ok {
		t.Error("findSearch found a search that is not configured")
	}
}
