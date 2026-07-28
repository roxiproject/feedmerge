package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// compactRatio is the share of the log that has to be garbage - superseded
// records and tombstones - before an automatic compaction is worth the rewrite.
const compactRatio = 0.5

// compactMinRecords keeps compaction from firing on a log so small that the
// rewrite costs more than the space it reclaims.
const compactMinRecords = 64

// Stats describes the log's occupancy.
type Stats struct {
	// Live is the number of entries currently readable.
	Live int
	// Records is the number of records in the log, including superseded ones
	// and tombstones.
	Records int64
	// Garbage is Records minus Live: what a compaction would reclaim.
	Garbage int64
	// Bytes is the size of the log file.
	Bytes int64
}

// Stats returns the current occupancy of the log.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{
		Live:    len(s.live),
		Records: s.lines,
		Garbage: s.lines - int64(len(s.live)),
		Bytes:   s.size,
	}
}

// Purge tombstones every entry stored before the cutoff and returns how many
// were removed. It compacts afterwards if the log has become mostly garbage,
// so a long-running server does not grow without bound.
func (s *Store) Purge(cutoff time.Time) (int, error) {
	s.mu.Lock()
	removed := 0
	var err error
	for id, r := range s.live {
		if !r.StoredAt.Before(cutoff) {
			continue
		}
		if err = s.appendLocked(record{V: recordVersion, ID: id, Deleted: true, StoredAt: s.clock().UTC()}); err != nil {
			break
		}
		removed++
	}
	if removed > 0 {
		if ferr := s.flushLocked(); err == nil {
			err = ferr
		}
	}
	needsCompact := s.shouldCompactLocked()
	s.mu.Unlock()

	if err != nil {
		return removed, err
	}
	if needsCompact {
		if err := s.Compact(); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// PurgeOlderThan tombstones entries stored more than retain ago. A retention of
// zero or less disables the policy and removes nothing.
func (s *Store) PurgeOlderThan(retain time.Duration) (int, error) {
	if retain <= 0 {
		return 0, nil
	}
	return s.Purge(s.clock().UTC().Add(-retain))
}

func (s *Store) shouldCompactLocked() bool {
	if s.lines < compactMinRecords {
		return false
	}
	garbage := s.lines - int64(len(s.live))
	return float64(garbage) >= compactRatio*float64(s.lines)
}

// Compact rewrites the log with only the live records, dropping superseded
// versions and tombstones. The new log is written to a temporary file in the
// same directory, fsynced and renamed into place, so a crash at any point
// leaves either the old log or the new one intact - never a partial file.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return errors.New("store: compact on a closed store")
	}
	if err := s.flushLocked(); err != nil {
		return err
	}

	// Compact in the order All returns, so a compacted log replays newest
	// first and stays readable by eye.
	live := make([]record, 0, len(s.live))
	for _, r := range s.live {
		live = append(live, r)
	}
	sortRecords(live)

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".compact-*")
	if err != nil {
		return fmt.Errorf("store: compact: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	var size int64
	for _, r := range live {
		b, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("store: compact: encode %q: %w", r.ID, err)
		}
		b = append(b, '\n')
		n, err := tmp.Write(b)
		size += int64(n)
		if err != nil {
			return fmt.Errorf("store: compact: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("store: compact: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: compact: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("store: compact: rename: %w", err)
	}
	tmp = nil
	syncDir(dir)

	// Swap in a handle on the rewritten file.
	f, err := os.OpenFile(s.path, os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("store: compact: reopen: %w", err)
	}
	s.f.Close()
	s.f = f
	s.w.Reset(f)
	s.size = size
	s.lines = int64(len(live))
	return nil
}

// syncDir fsyncs a directory so that a rename is durable. Failure is not fatal:
// on platforms where directories cannot be opened for sync the rename is
// already atomic from the reader's point of view.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}

// sortRecords orders records newest first, matching All.
func sortRecords(recs []record) {
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		da, db := a.date(), b.date()
		switch {
		case da.IsZero() && db.IsZero():
			return a.ID < b.ID
		case da.IsZero():
			return false
		case db.IsZero():
			return true
		case da.Equal(db):
			return a.ID < b.ID
		}
		return da.After(db)
	})
}

func (r record) date() time.Time {
	if !r.Published.IsZero() {
		return r.Published
	}
	return r.Updated
}
