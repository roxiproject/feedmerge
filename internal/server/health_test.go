package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func healthByURL(hs []SourceHealth, suffix string) (SourceHealth, bool) {
	for _, h := range hs {
		if len(h.URL) >= len(suffix) && h.URL[len(h.URL)-len(suffix):] == suffix {
			return h, true
		}
	}
	return SourceHealth{}, false
}

func TestHealthTrackerCounters(t *testing.T) {
	tr := newHealthTracker()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	tr.record([]SourceStatus{{URL: "https://a.example/f", Name: "A", OK: true, Status: 200, Entries: 4, DurationMS: 100}}, now)
	tr.record([]SourceStatus{{URL: "https://a.example/f", Status: 502, Error: "boom", DurationMS: 300}}, now.Add(time.Minute))
	tr.record([]SourceStatus{{URL: "https://a.example/f", Status: 502, Error: "boom again", DurationMS: 200}}, now.Add(2*time.Minute))

	all := tr.Health()
	if len(all) != 1 {
		t.Fatalf("got %d sources, want 1", len(all))
	}
	h := all[0]
	if h.Name != "A" {
		t.Errorf("name = %q, want it remembered from the first fetch", h.Name)
	}
	if h.Fetches != 3 || h.Successes != 1 || h.Failures != 2 {
		t.Errorf("counts = %d/%d/%d, want 3/1/2", h.Fetches, h.Successes, h.Failures)
	}
	if h.ConsecutiveFailures != 2 {
		t.Errorf("consecutive failures = %d, want 2", h.ConsecutiveFailures)
	}
	if h.LastError != "boom again" || h.LastStatus != 502 {
		t.Errorf("last failure = %q / %d", h.LastError, h.LastStatus)
	}
	if h.Entries != 4 {
		t.Errorf("entries = %d, want the count from the last success", h.Entries)
	}
	if h.AvgDurationMS != 200 || h.LastDurationMS != 200 {
		t.Errorf("durations = avg %d, last %d", h.AvgDurationMS, h.LastDurationMS)
	}
	if h.SuccessRate != 0.3333 {
		t.Errorf("success rate = %v, want 0.3333", h.SuccessRate)
	}
	if h.LastSuccess != "2026-07-27T12:00:00Z" || h.LastFailure != "2026-07-27T12:02:00Z" {
		t.Errorf("timestamps = %q / %q", h.LastSuccess, h.LastFailure)
	}
}

func TestHealthTrackerSuccessResetsTheStreak(t *testing.T) {
	tr := newHealthTracker()
	now := time.Now()
	tr.record([]SourceStatus{{URL: "u", Status: 500, Error: "down"}}, now)
	tr.record([]SourceStatus{{URL: "u", OK: true, NotModified: true, Status: 304}}, now)

	h := tr.Health()[0]
	if h.ConsecutiveFailures != 0 {
		t.Errorf("consecutive failures = %d, want 0 after a success", h.ConsecutiveFailures)
	}
	if h.NotModified != 1 {
		t.Errorf("not_modified = %d, want 1", h.NotModified)
	}
	if h.LastError != "" {
		t.Errorf("last error = %q, want it cleared by the success", h.LastError)
	}
}

func TestHealthTrackerOrdersByURL(t *testing.T) {
	tr := newHealthTracker()
	tr.record([]SourceStatus{{URL: "https://z.example/f"}, {URL: "https://a.example/f"}}, time.Now())
	all := tr.Health()
	if all[0].URL != "https://a.example/f" || all[1].URL != "https://z.example/f" {
		t.Errorf("order = %q, %q", all[0].URL, all[1].URL)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv, m, cleanup := newTestSetup(t)
	defer cleanup()

	for i := 0; i < 2; i++ {
		if _, err := m.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
	var resp metricsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding metrics: %v\n%s", err, w.Body.String())
	}
	if resp.Refreshes != 2 {
		t.Errorf("refreshes = %d, want 2", resp.Refreshes)
	}
	if len(resp.Sources) != 3 {
		t.Fatalf("got %d sources, want 3", len(resp.Sources))
	}
	if resp.Degraded != 1 {
		t.Errorf("degraded = %d, want 1 (the broken upstream)", resp.Degraded)
	}
	if resp.Entries != 3 || resp.Indexed != 3 {
		t.Errorf("entries/indexed = %d/%d, want 3/3", resp.Entries, resp.Indexed)
	}
	if resp.Terms == 0 {
		t.Error("terms = 0, want the index vocabulary")
	}

	broken, ok := healthByURL(resp.Sources, "/broken.xml")
	if !ok {
		t.Fatalf("the broken source is missing from %+v", resp.Sources)
	}
	if broken.Fetches != 2 || broken.Failures != 2 || broken.ConsecutiveFailures != 2 {
		t.Errorf("broken source = %+v", broken)
	}
	if broken.SuccessRate != 0 || broken.LastError == "" {
		t.Errorf("broken source rate/error = %v / %q", broken.SuccessRate, broken.LastError)
	}

	good, ok := healthByURL(resp.Sources, "/rss.xml")
	if !ok {
		t.Fatal("the RSS source is missing from the metrics")
	}
	if good.Successes != 2 || good.ConsecutiveFailures != 0 || good.Entries == 0 {
		t.Errorf("rss source = %+v", good)
	}
	if good.Name != "RSS Source" {
		t.Errorf("name = %q", good.Name)
	}
}

func TestMetricsBeforeAnyRefresh(t *testing.T) {
	srv, _, cleanup := newTestSetup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp metricsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding metrics: %v", err)
	}
	if resp.Refreshes != 0 || len(resp.Sources) != 0 || resp.Indexed != 0 {
		t.Errorf("cold metrics = %+v", resp)
	}
}

func TestMetricsCountsFailedRefreshes(t *testing.T) {
	srv, m, cleanup := newTestSetup(t)
	defer cleanup()
	cleanup() // every upstream is now closed, so the refresh has to fail

	if _, err := m.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh succeeded with every upstream down")
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	var resp metricsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding metrics: %v", err)
	}
	if resp.Refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", resp.Refreshes)
	}
	if resp.Degraded != 3 {
		t.Errorf("degraded = %d, want every source", resp.Degraded)
	}
}

func TestMetricsMethodNotAllowed(t *testing.T) {
	srv, _, cleanup := newTestSetup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}
