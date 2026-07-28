package server

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/roxiproject/feedmerge/internal/opml"
)

// handleOPML serves the configured sources as an OPML subscription list, so a
// reader can import the same set of feeds the merge is built from.
func (s *Server) handleOPML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.merger.Config()
	subs := make([]opml.Subscription, 0, len(cfg.Feeds))
	for _, f := range cfg.Feeds {
		subs = append(subs, opml.Subscription{Name: f.Name, URL: f.URL})
	}

	var buf bytes.Buffer
	// The document carries no timestamp: the subscription list only changes
	// when the config does, and a fresh date would defeat caching.
	if err := opml.Write(&buf, cfg.Title, time.Time{}, subs); err != nil {
		http.Error(w, "opml unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="feeds.opml"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(buf.Bytes())
}
