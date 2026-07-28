package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// SourceHealth is the running record of one source across refreshes. A single
// /healthz snapshot only says what happened last; these counters are what tell
// a feed that has been broken for a day apart from one that blipped once.
type SourceHealth struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
	// Fetches counts every attempt, successful or not.
	Fetches int `json:"fetches"`
	// Successes counts attempts that produced a usable document, including
	// the 304 responses that were answered from the cache.
	Successes   int `json:"successes"`
	Failures    int `json:"failures"`
	NotModified int `json:"not_modified"`
	// ConsecutiveFailures is the current streak, reset by any success.
	ConsecutiveFailures int `json:"consecutive_failures"`
	// Entries is the entry count of the last successful fetch.
	Entries int `json:"entries"`
	// LastStatus is the HTTP status of the last attempt.
	LastStatus  int    `json:"last_status,omitempty"`
	LastSuccess string `json:"last_success,omitempty"`
	LastFailure string `json:"last_failure,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	// LastDurationMS and AvgDurationMS describe fetch latency.
	LastDurationMS int64 `json:"last_duration_ms"`
	AvgDurationMS  int64 `json:"avg_duration_ms"`
	// SuccessRate is Successes/Fetches, rounded to four decimals.
	SuccessRate float64 `json:"success_rate"`

	// totalDuration accumulates latency so the average can be recomputed.
	totalDuration time.Duration
}

// healthTracker accumulates per-source health across refreshes. It is safe for
// concurrent use: refreshes write to it while requests read it.
type healthTracker struct {
	mu sync.Mutex
	by map[string]*SourceHealth
}

func newHealthTracker() *healthTracker {
	return &healthTracker{by: make(map[string]*SourceHealth)}
}

// record folds one refresh's source statuses into the running counters.
func (h *healthTracker) record(statuses []SourceStatus, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, st := range statuses {
		e, ok := h.by[st.URL]
		if !ok {
			e = &SourceHealth{URL: st.URL}
			h.by[st.URL] = e
		}
		if st.Name != "" {
			e.Name = st.Name
		}
		e.Fetches++
		e.LastStatus = st.Status
		e.LastDurationMS = st.DurationMS
		e.totalDuration += time.Duration(st.DurationMS) * time.Millisecond
		e.AvgDurationMS = e.totalDuration.Milliseconds() / int64(e.Fetches)
		if st.OK {
			e.Successes++
			e.ConsecutiveFailures = 0
			e.Entries = st.Entries
			e.LastSuccess = now.UTC().Format(time.RFC3339)
			e.LastError = ""
			if st.NotModified {
				e.NotModified++
			}
		} else {
			e.Failures++
			e.ConsecutiveFailures++
			e.LastFailure = now.UTC().Format(time.RFC3339)
			e.LastError = st.Error
		}
		e.SuccessRate = round4(float64(e.Successes) / float64(e.Fetches))
	}
}

// Health returns a copy of the per-source counters, ordered by URL so that
// repeated requests produce a stable document.
func (h *healthTracker) Health() []SourceHealth {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]SourceHealth, 0, len(h.by))
	for _, e := range h.by {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

// SourceHealth returns the running counters for every configured source.
func (m *Merger) SourceHealth() []SourceHealth { return m.health.Health() }

// metricsResponse is the body of /metrics.
type metricsResponse struct {
	UptimeSec int64 `json:"uptime_sec"`
	Refreshes int   `json:"refreshes"`
	// Degraded counts sources that failed their most recent fetch.
	Degraded int            `json:"degraded"`
	Entries  int            `json:"entries"`
	Indexed  int            `json:"indexed"`
	Terms    int            `json:"terms"`
	Sources  []SourceHealth `json:"sources"`
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := metricsResponse{
		UptimeSec: int64(time.Since(s.StartedAt).Seconds()),
		Refreshes: s.merger.Refreshes(),
		Sources:   s.merger.SourceHealth(),
	}
	for _, h := range resp.Sources {
		if h.ConsecutiveFailures > 0 {
			resp.Degraded++
		}
	}
	if snap := s.merger.Snapshot(); snap != nil {
		resp.Entries = snap.Entries
		resp.Indexed = snap.Index.Len()
		resp.Terms = snap.Index.Terms()
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
}
