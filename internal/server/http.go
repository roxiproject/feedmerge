package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Server exposes the merged feed over HTTP.
type Server struct {
	merger *Merger
	mux    *http.ServeMux
	// StartedAt is used by /healthz to report uptime.
	StartedAt time.Time
}

// NewServer builds the HTTP handler set for a merger.
func NewServer(m *Merger) *Server {
	s := &Server{merger: m, mux: http.NewServeMux(), StartedAt: time.Now()}
	s.mux.HandleFunc("/feed.xml", s.handleFeed("rss"))
	s.mux.HandleFunc("/feed.rss", s.handleFeed("rss"))
	s.mux.HandleFunc("/feed.atom", s.handleFeed("atom"))
	s.mux.HandleFunc("/feed.json", s.handleFeed("json"))
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/", s.handleIndex)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// snapshot returns the current snapshot, triggering a refresh if the server has
// not produced one yet.
func (s *Server) snapshot(ctx context.Context) (*Snapshot, error) {
	if snap := s.merger.Snapshot(); snap != nil {
		return snap, nil
	}
	return s.merger.Refresh(ctx)
}

func (s *Server) handleFeed(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snap, err := s.snapshot(r.Context())
		if err != nil {
			http.Error(w, "feed unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}

		var body []byte
		var ctype string
		switch format {
		case "rss":
			body, ctype = snap.RSS, "application/rss+xml; charset=utf-8"
		case "atom":
			body, ctype = snap.Atom, "application/atom+xml; charset=utf-8"
		default:
			body, ctype = snap.JSON, "application/feed+json; charset=utf-8"
		}
		etag := formatETag(snap.ETag, format)

		w.Header().Set("Content-Type", ctype)
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", snap.Modified.UTC().Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("X-Feed-Entries", strconv.Itoa(snap.Entries))

		if notModified(r, etag, snap.Modified) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		w.Write(body)
	}
}

// formatETag derives a per-representation entity tag from the snapshot tag, so
// that /feed.xml and /feed.json never share one.
func formatETag(base, format string) string {
	if base == "" {
		return ""
	}
	return `"` + format + "-" + strings.Trim(base, `"`) + `"`
}

// notModified implements inbound conditional GET: If-None-Match takes priority
// over If-Modified-Since, as required by RFC 9110.
func notModified(r *http.Request, etag string, modified time.Time) bool {
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		return etagMatch(inm, etag)
	}
	ims := r.Header.Get("If-Modified-Since")
	if ims == "" || modified.IsZero() {
		return false
	}
	t, err := http.ParseTime(ims)
	if err != nil {
		return false
	}
	return !modified.Truncate(time.Second).After(t)
}

// etagMatch compares an If-None-Match header value against our tag, honouring
// "*" and weak comparison.
func etagMatch(header, etag string) bool {
	if etag == "" {
		return false
	}
	for _, cand := range strings.Split(header, ",") {
		cand = strings.TrimSpace(cand)
		if cand == "*" {
			return true
		}
		if strings.EqualFold(strings.TrimPrefix(cand, "W/"), strings.TrimPrefix(etag, "W/")) {
			return true
		}
	}
	return false
}

type healthResponse struct {
	Status     string         `json:"status"`
	UptimeSec  int64          `json:"uptime_sec"`
	Entries    int            `json:"entries"`
	Sources    []SourceStatus `json:"sources"`
	Failures   int            `json:"failures"`
	LastUpdate string         `json:"last_update,omitempty"`
	Error      string         `json:"error,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{Status: "ok", UptimeSec: int64(time.Since(s.StartedAt).Seconds())}
	code := http.StatusOK

	snap := s.merger.Snapshot()
	lastRun, lastErr := s.merger.LastRun()
	if !lastRun.IsZero() {
		resp.LastUpdate = lastRun.UTC().Format(time.RFC3339)
	}
	switch {
	case snap == nil && lastErr != nil:
		resp.Status, code = "error", http.StatusServiceUnavailable
		resp.Error = lastErr.Error()
	case snap == nil:
		resp.Status, code = "starting", http.StatusServiceUnavailable
	default:
		resp.Entries = snap.Entries
		resp.Sources = snap.Sources
		resp.Failures = snap.Failures
		if snap.Failures > 0 {
			resp.Status = "degraded"
		}
		if lastErr != nil {
			resp.Error = lastErr.Error()
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strings.Join([]string{
		"feedmerge",
		"",
		"GET /feed.xml    merged feed as RSS 2.0",
		"GET /feed.atom   merged feed as Atom 1.0",
		"GET /feed.json   merged feed as JSON Feed 1.1",
		"GET /healthz     per-source fetch status",
		"",
	}, "\n")))
}
