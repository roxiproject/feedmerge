package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/roxiproject/feedmerge/internal/config"
	"github.com/roxiproject/feedmerge/internal/feed"
)

// savedExtensions maps the URL suffix of a saved search to an output format.
var savedExtensions = map[string]string{
	".xml":  "rss",
	".rss":  "rss",
	".atom": "atom",
	".json": "json",
}

// splitSavedPath splits "/saved/go.atom" into its name and output format. An
// unknown or missing extension defaults to RSS, which is what a reader handed a
// bare name expects.
func splitSavedPath(path string) (name, format string, ok bool) {
	rest := strings.TrimPrefix(path, "/saved/")
	if rest == path || rest == "" || strings.Contains(rest, "/") {
		return "", "", false
	}
	if i := strings.LastIndexByte(rest, '.'); i > 0 {
		if f, known := savedExtensions[rest[i:]]; known {
			return rest[:i], f, true
		}
		return "", "", false
	}
	return rest, "rss", true
}

// findSearch looks a saved search up by name.
func findSearch(searches []config.SavedSearch, name string) (config.SavedSearch, bool) {
	for _, s := range searches {
		if s.Name == name {
			return s, true
		}
	}
	return config.SavedSearch{}, false
}

// savedSelfLink derives the public URL of a saved search feed from the merged
// feed's own self link, so the generated document points at itself.
func savedSelfLink(base, name, format string) string {
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	ext := ".xml"
	switch format {
	case "atom":
		ext = ".atom"
	case "json":
		ext = ".json"
	}
	ref, err := url.Parse("/saved/" + name + ext)
	if err != nil {
		return ""
	}
	return u.ResolveReference(ref).String()
}

func (s *Server) handleSaved(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.merger.Config()
	if r.URL.Path == "/saved" || r.URL.Path == "/saved/" {
		s.writeSavedIndex(w, r, cfg)
		return
	}
	name, format, ok := splitSavedPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	saved, ok := findSearch(cfg.Searches, name)
	if !ok {
		http.Error(w, "no saved search named "+name, http.StatusNotFound)
		return
	}
	snap, err := s.snapshot(r.Context())
	if err != nil {
		http.Error(w, "feed unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	results := snap.Index.Search(saved.Query, saved.Limit)
	entries := make([]feed.Entry, 0, len(results))
	for _, res := range results {
		entries = append(entries, res.Entry)
	}
	meta := feed.Meta{
		Title:       saved.FeedTitle(),
		Link:        cfg.Link,
		Description: "Entries matching " + saved.Query,
		SelfLink:    savedSelfLink(cfg.SelfLink, name, format),
		Updated:     newestDate(entries),
	}
	if meta.Updated.IsZero() {
		meta.Updated = snap.Modified
	}

	var buf bytes.Buffer
	var ctype string
	switch format {
	case "atom":
		ctype = "application/atom+xml; charset=utf-8"
		err = feed.WriteAtom(&buf, meta, entries)
	case "json":
		ctype = "application/feed+json; charset=utf-8"
		err = feed.WriteJSON(&buf, meta, entries)
	default:
		ctype = "application/rss+xml; charset=utf-8"
		err = feed.WriteRSS(&buf, meta, entries)
	}
	if err != nil {
		http.Error(w, "feed unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// The tag covers the snapshot, the query and the representation, so editing
	// a saved search invalidates the caches of everyone subscribed to it.
	sum := sha256.Sum256([]byte(saved.Query + "\x00" + strconv.Itoa(saved.Limit)))
	etag := formatETag(snap.ETag, format+"-saved-"+name+"-"+hex.EncodeToString(sum[:8]))

	w.Header().Set("Content-Type", ctype)
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", snap.Modified.UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Feed-Entries", strconv.Itoa(len(entries)))
	if notModified(r, etag, snap.Modified) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(buf.Bytes())
}

// savedIndexEntry describes one saved search in the /saved listing.
type savedIndexEntry struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	RSS   string `json:"rss"`
	Atom  string `json:"atom"`
	JSON  string `json:"json"`
}

func (s *Server) writeSavedIndex(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	out := make([]savedIndexEntry, 0, len(cfg.Searches))
	for _, ss := range cfg.Searches {
		out = append(out, savedIndexEntry{
			Name:  ss.Name,
			Title: ss.FeedTitle(),
			Query: ss.Query,
			Limit: ss.Limit,
			RSS:   "/saved/" + ss.Name + ".xml",
			Atom:  "/saved/" + ss.Name + ".atom",
			JSON:  "/saved/" + ss.Name + ".json",
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(struct {
		Count    int               `json:"count"`
		Searches []savedIndexEntry `json:"searches"`
	}{len(out), out})
}
