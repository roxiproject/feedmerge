// Package server merges the configured feeds on a schedule and serves the
// result over HTTP in RSS, Atom and JSON Feed form.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/roxiproject/feedmerge/internal/config"
	"github.com/roxiproject/feedmerge/internal/feed"
	"github.com/roxiproject/feedmerge/internal/fetch"
	"github.com/roxiproject/feedmerge/internal/search"
)

// SourceStatus records the outcome of the last fetch of one source.
type SourceStatus struct {
	URL         string `json:"url"`
	Name        string `json:"name,omitempty"`
	OK          bool   `json:"ok"`
	NotModified bool   `json:"not_modified"`
	Status      int    `json:"status,omitempty"`
	Entries     int    `json:"entries"`
	Error       string `json:"error,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
}

// Snapshot is an immutable rendered view of the merged feed.
type Snapshot struct {
	RSS      []byte
	Atom     []byte
	JSON     []byte
	ETag     string
	Modified time.Time
	Entries  int
	// Raw is the merged entry list, exposed for the CLI and for tests.
	Raw     []feed.Entry
	Sources []SourceStatus
	// Failures counts sources that could not be fetched or parsed.
	Failures int
	// Index is a full-text index over Raw, rebuilt with every snapshot so that
	// a search never sees a half-updated index.
	Index *search.Index
}

// Merger owns the fetch/merge/render cycle.
type Merger struct {
	cfg     *config.Config
	fetcher *fetch.Fetcher
	logger  *log.Logger

	mu       sync.RWMutex
	snap     *Snapshot
	lastErr  error
	lastRun  time.Time
	refreshM sync.Mutex
}

// NewMerger returns a Merger for cfg. A nil logger disables logging.
func NewMerger(cfg *config.Config, f *fetch.Fetcher, logger *log.Logger) *Merger {
	if f == nil {
		f = fetch.New(cfg.Timeout.D(), cfg.HostInterval.D())
		if cfg.UserAgent != "" {
			f.UserAgent = cfg.UserAgent
		}
	}
	return &Merger{cfg: cfg, fetcher: f, logger: logger}
}

func (m *Merger) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}

// Config returns the configuration the merger was built with.
func (m *Merger) Config() *config.Config { return m.cfg }

// Snapshot returns the most recent successful render, or nil if none exists.
func (m *Merger) Snapshot() *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap
}

// LastRun reports when the last refresh completed and the error it produced,
// if any.
func (m *Merger) LastRun() (time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastRun, m.lastErr
}

// Refresh fetches every configured feed, merges the entries and renders all
// three output formats. Individual feed failures are recorded in the snapshot
// but do not fail the refresh; an error is returned only when no feed at all
// could be read.
func (m *Merger) Refresh(ctx context.Context) (*Snapshot, error) {
	// Only one refresh at a time; concurrent callers wait and share the result.
	m.refreshM.Lock()
	defer m.refreshM.Unlock()

	sources := make([]fetch.Source, 0, len(m.cfg.Feeds))
	for _, s := range m.cfg.Feeds {
		sources = append(sources, fetch.Source{URL: s.URL, Name: s.Name})
	}
	results := m.fetcher.FetchAll(ctx, sources, m.cfg.Workers)

	var (
		all      []feed.Entry
		statuses = make([]SourceStatus, 0, len(results))
		failures int
		okCount  int
	)
	for _, r := range results {
		st := SourceStatus{
			URL:         r.URL,
			Name:        r.Name,
			NotModified: r.NotModified,
			Status:      r.Status,
			DurationMS:  r.Duration.Milliseconds(),
		}
		if r.Err != nil {
			st.Error = r.Err.Error()
			failures++
			m.logf("feed %s failed: %v", r.URL, r.Err)
		} else if r.Feed != nil {
			st.OK = true
			st.Entries = len(r.Feed.Entries)
			okCount++
			name := r.Name
			for _, e := range r.Feed.Entries {
				if name != "" {
					e.SourceTitle = name
				}
				all = append(all, e)
			}
		}
		statuses = append(statuses, st)
	}

	if okCount == 0 {
		err := fmt.Errorf("merge: all %d feeds failed", len(results))
		m.mu.Lock()
		m.lastErr = err
		m.lastRun = time.Now()
		m.mu.Unlock()
		return nil, err
	}

	entries := feed.Dedup(all, m.cfg.DedupOptions())
	entries = m.cfg.FilterSet.Apply(entries)
	feed.SortByDate(entries)
	if m.cfg.MaxItems > 0 && len(entries) > m.cfg.MaxItems {
		entries = entries[:m.cfg.MaxItems]
	}

	meta := feed.Meta{
		Title:       m.cfg.Title,
		Link:        m.cfg.Link,
		Description: m.cfg.Description,
		SelfLink:    m.cfg.SelfLink,
		Updated:     newestDate(entries),
	}
	if meta.Updated.IsZero() {
		meta.Updated = time.Now().UTC()
	}

	snap := &Snapshot{
		Entries:  len(entries),
		Raw:      entries,
		Sources:  statuses,
		Failures: failures,
		Modified: meta.Updated.Truncate(time.Second),
		Index:    search.New(entries),
	}
	var buf bytes.Buffer
	if err := feed.WriteRSS(&buf, meta, entries); err != nil {
		return nil, err
	}
	snap.RSS = append([]byte(nil), buf.Bytes()...)
	buf.Reset()
	if err := feed.WriteAtom(&buf, meta, entries); err != nil {
		return nil, err
	}
	snap.Atom = append([]byte(nil), buf.Bytes()...)
	buf.Reset()
	if err := feed.WriteJSON(&buf, meta, entries); err != nil {
		return nil, err
	}
	snap.JSON = append([]byte(nil), buf.Bytes()...)

	h := sha256.New()
	h.Write(snap.RSS)
	h.Write(snap.Atom)
	h.Write(snap.JSON)
	snap.ETag = `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`

	m.mu.Lock()
	m.snap = snap
	m.lastErr = nil
	m.lastRun = time.Now()
	m.mu.Unlock()

	m.logf("merged %d entries from %d/%d feeds (%d failed)", len(entries), okCount, len(results), failures)
	return snap, nil
}

// Run refreshes immediately and then on the configured interval until the
// context is cancelled.
func (m *Merger) Run(ctx context.Context) {
	if _, err := m.Refresh(ctx); err != nil {
		m.logf("initial refresh failed: %v", err)
	}
	interval := m.cfg.Refresh.D()
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := m.Refresh(ctx); err != nil && ctx.Err() == nil {
				m.logf("refresh failed: %v", err)
			}
		}
	}
}

func newestDate(entries []feed.Entry) time.Time {
	var newest time.Time
	for _, e := range entries {
		if d := e.Date(); d.After(newest) {
			newest = d
		}
	}
	return newest
}
