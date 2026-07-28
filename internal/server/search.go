package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/roxiproject/feedmerge/internal/search"
)

// defaultSearchLimit and maxSearchLimit bound how many hits one request can
// ask for, so a client cannot turn a search into a full dump of the store.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 200
)

// searchHit is one result in the JSON search response.
type searchHit struct {
	ID         string   `json:"id"`
	Title      string   `json:"title,omitempty"`
	Link       string   `json:"link,omitempty"`
	Author     string   `json:"author,omitempty"`
	Source     string   `json:"source,omitempty"`
	Published  string   `json:"published,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Score      float64  `json:"score"`
	Snippet    string   `json:"snippet,omitempty"`
	Matched    []string `json:"matched,omitempty"`
}

// searchResponse is the body of /search.
type searchResponse struct {
	Query string `json:"query"`
	// Parsed echoes how the query was understood, which is the quickest way to
	// see why a phrase or an operator did not do what the caller expected.
	Parsed  string      `json:"parsed"`
	Total   int         `json:"total"`
	Limit   int         `json:"limit"`
	Indexed int         `json:"indexed"`
	Results []searchHit `json:"results"`
}

// parseLimit reads the limit parameter, clamping it into range. A missing or
// unparseable value uses the default.
func parseLimit(raw string) int {
	if raw == "" {
		return defaultSearchLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultSearchLimit
	}
	if n > maxSearchLimit {
		return maxSearchLimit
	}
	return n
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw := r.URL.Query().Get("q")
	q := search.ParseQuery(raw)
	if q.Empty() {
		http.Error(w, "search requires a non-empty q parameter", http.StatusBadRequest)
		return
	}
	snap, err := s.snapshot(r.Context())
	if err != nil {
		http.Error(w, "search unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	limit := parseLimit(r.URL.Query().Get("limit"))
	// Rank everything, then page, so that total reports the real match count.
	all := snap.Index.SearchQuery(q, 0)
	resp := searchResponse{
		Query:   raw,
		Parsed:  q.String(),
		Total:   len(all),
		Limit:   limit,
		Indexed: snap.Index.Len(),
		Results: make([]searchHit, 0, limit),
	}
	if len(all) > limit {
		all = all[:limit]
	}
	for _, res := range all {
		hit := searchHit{
			ID:         res.Entry.ID,
			Title:      res.Entry.Title,
			Link:       res.Entry.Link,
			Author:     res.Entry.Author,
			Source:     res.Entry.SourceTitle,
			Categories: res.Entry.Categories,
			Score:      round4(res.Score),
			Snippet:    res.Snippet,
			Matched:    res.Matched,
		}
		if d := res.Entry.Date(); !d.IsZero() {
			hit.Published = d.UTC().Format(time.RFC3339)
		}
		resp.Results = append(resp.Results, hit)
	}

	// The query is hashed into the tag: it can contain quotes and spaces, which
	// are not legal in an entity tag.
	sum := sha256.Sum256([]byte(q.String() + "\x00" + strconv.Itoa(limit)))
	etag := formatETag(snap.ETag, "search"+hex.EncodeToString(sum[:8]))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Search-Total", strconv.Itoa(resp.Total))
	if notModified(r, etag, snap.Modified) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
}

// round4 trims a score to four decimals so that responses stay stable and
// readable rather than exposing float noise.
func round4(f float64) float64 {
	return float64(int64(f*10000+0.5)) / 10000
}
