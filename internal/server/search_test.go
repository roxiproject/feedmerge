package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// searchOnce performs one search request against a warmed-up server.
func searchOnce(t *testing.T, srv *Server, target string) (*httptest.ResponseRecorder, searchResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	var resp searchResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decoding %s: %v (body %q)", target, err, w.Body.String())
		}
	}
	return w, resp
}

func warmedServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, m, cleanup := newTestSetup(t)
	if _, err := m.Refresh(context.Background()); err != nil {
		cleanup()
		t.Fatalf("Refresh: %v", err)
	}
	return srv, cleanup
}

func hitIDs(resp searchResponse) []string {
	out := make([]string, len(resp.Results))
	for i, h := range resp.Results {
		out[i] = h.Title
	}
	return out
}

func TestSearchEndpointRanksMatches(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w, resp := searchOnce(t, srv, "/search?q=headline")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("total = %d, results = %d, want 1/1", resp.Total, len(resp.Results))
	}
	hit := resp.Results[0]
	if hit.Title != "Shared headline about Go" {
		t.Errorf("title = %q", hit.Title)
	}
	if hit.Score <= 0 {
		t.Errorf("score = %f, want > 0", hit.Score)
	}
	if hit.Link == "" || hit.ID == "" {
		t.Errorf("hit is missing identity or link: %+v", hit)
	}
	if resp.Indexed != 3 {
		t.Errorf("indexed = %d, want 3", resp.Indexed)
	}
	if got := w.Header().Get("X-Search-Total"); got != "1" {
		t.Errorf("X-Search-Total = %q, want 1", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSearchEndpointEchoesParsedQuery(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	_, resp := searchOnce(t, srv, "/search?q="+`story+-sponsored`)
	if resp.Query == "" {
		t.Error("response does not echo the raw query")
	}
	if !strings.Contains(resp.Parsed, "story") {
		t.Errorf("parsed = %q, want it to mention the query term", resp.Parsed)
	}
}

func TestSearchEndpointPhraseQuery(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	_, hit := searchOnce(t, srv, "/search?q=%22shared+headline%22")
	if got := hitIDs(hit); len(got) != 1 || got[0] != "Shared headline about Go" {
		t.Errorf("phrase search = %v, want the shared headline", got)
	}
	_, miss := searchOnce(t, srv, "/search?q=%22headline+shared%22")
	if len(miss.Results) != 0 {
		t.Errorf("reversed phrase matched: %v", hitIDs(miss))
	}
}

func TestSearchEndpointLimit(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	_, all := searchOnce(t, srv, "/search?q=story")
	if all.Total < 2 {
		t.Fatalf("total = %d, want at least 2 matches to page over", all.Total)
	}
	w, one := searchOnce(t, srv, "/search?q=story&limit=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if one.Limit != 1 || len(one.Results) != 1 {
		t.Errorf("limit = %d, results = %d, want 1/1", one.Limit, len(one.Results))
	}
	if one.Total != all.Total {
		t.Errorf("total = %d under a limit, want the unpaged %d", one.Total, all.Total)
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", defaultSearchLimit},
		{"5", 5},
		{"0", defaultSearchLimit},
		{"-3", defaultSearchLimit},
		{"not a number", defaultSearchLimit},
		{"100000", maxSearchLimit},
	}
	for _, tt := range tests {
		if got := parseLimit(tt.in); got != tt.want {
			t.Errorf("parseLimit(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestSearchEndpointRejectsEmptyQuery(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	for _, target := range []string{"/search", "/search?q=", "/search?q=+++", "/search?q=the+and+of"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", target, w.Code)
		}
	}
}

func TestSearchEndpointMethodNotAllowed(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/search?q=go", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if got := w.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q", got)
	}
}

func TestSearchEndpointConditionalGET(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w, _ := searchOnce(t, srv, "/search?q=story")
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a search response")
	}

	req := httptest.NewRequest(http.MethodGet, "/search?q=story", nil)
	req.Header.Set("If-None-Match", etag)
	again := httptest.NewRecorder()
	srv.ServeHTTP(again, req)
	if again.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", again.Code)
	}

	// A different query must not be told its cached copy is fresh.
	other := httptest.NewRequest(http.MethodGet, "/search?q=headline", nil)
	other.Header.Set("If-None-Match", etag)
	ow := httptest.NewRecorder()
	srv.ServeHTTP(ow, other)
	if ow.Code != http.StatusOK {
		t.Errorf("other query = %d, want 200", ow.Code)
	}
}

func TestSearchEndpointHeadHasNoBody(t *testing.T) {
	srv, cleanup := warmedServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/search?q=story", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("HEAD returned %d bytes of body", w.Body.Len())
	}
	if w.Header().Get("X-Search-Total") == "" {
		t.Error("HEAD response is missing the result count")
	}
}

func TestSearchRefreshesOnDemand(t *testing.T) {
	srv, _, cleanup := newTestSetup(t)
	defer cleanup()

	// No refresh has happened yet; the first search must trigger one.
	w, resp := searchOnce(t, srv, "/search?q=story")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if resp.Total == 0 {
		t.Error("cold search returned nothing")
	}
}

func TestRound4(t *testing.T) {
	tests := []struct {
		in   float64
		want float64
	}{
		{0, 0}, {1.234567, 1.2346}, {2.5, 2.5}, {0.00004, 0},
	}
	for _, tt := range tests {
		if got := round4(tt.in); got != tt.want {
			t.Errorf("round4(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
