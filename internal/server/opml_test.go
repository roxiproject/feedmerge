package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roxiproject/feedmerge/internal/opml"
)

func TestOPMLEndpoint(t *testing.T) {
	srv, _, cleanup := newTestSetup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/feeds.opml", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/x-opml") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "feeds.opml") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	body := w.Body.Bytes()
	subs, err := opml.Parse(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("served document does not parse: %v\n%s", err, body)
	}
	// Every configured source is listed, including the one that fails to fetch.
	if len(subs) != 3 {
		t.Fatalf("got %d subscriptions, want 3: %+v", len(subs), subs)
	}
	if subs[0].Name != "RSS Source" {
		t.Errorf("first subscription = %+v", subs[0])
	}
	if !strings.Contains(string(body), "<title>Test Merge</title>") {
		t.Errorf("document does not carry the merged feed title:\n%s", body)
	}
}

func TestOPMLEndpointNeedsNoRefresh(t *testing.T) {
	srv, m, cleanup := newTestSetup(t)
	defer cleanup()

	// The subscription list comes from the config, so it must be served before
	// any merge has run.
	if m.Snapshot() != nil {
		t.Fatal("expected no snapshot before the first refresh")
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/feeds.opml", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestOPMLEndpointMethodNotAllowed(t *testing.T) {
	srv, _, cleanup := newTestSetup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/feeds.opml", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
	if got := w.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q", got)
	}
}

func TestOPMLEndpointHeadHasNoBody(t *testing.T) {
	srv, _, cleanup := newTestSetup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/feeds.opml", nil))
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d body bytes", w.Code, w.Body.Len())
	}
	if w.Header().Get("Content-Length") == "0" {
		t.Error("HEAD should still advertise the document length")
	}
}
