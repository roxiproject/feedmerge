package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock lets a test control the StoredAt stamps the store writes.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func openClocked(t *testing.T, c *fakeClock) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "entries.jsonl")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.clock = c.Now
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestPurgeOlderThan(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c := &fakeClock{now: base}
	s, _ := openClocked(t, c)

	if _, err := s.Put(entry("old1", "Old one", base), entry("old2", "Old two", base)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	c.now = base.Add(40 * 24 * time.Hour)
	if _, err := s.Put(entry("fresh", "Fresh", c.now)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	removed, err := s.PurgeOlderThan(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if got := ids(s.Entries()); !equal(got, []string{"fresh"}) {
		t.Fatalf("remaining = %v, want [fresh]", got)
	}
	if _, ok := s.Get("old1"); ok {
		t.Error("purged entry is still readable")
	}
}

func TestPurgeOlderThanZeroKeepsEverything(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	s, _ := openClocked(t, c)
	if _, err := s.Put(entry("a", "A", c.now)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	c.now = c.now.Add(10 * 365 * 24 * time.Hour)
	for _, d := range []time.Duration{0, -time.Hour} {
		removed, err := s.PurgeOlderThan(d)
		if err != nil || removed != 0 {
			t.Errorf("PurgeOlderThan(%s) = %d, %v, want 0, nil", d, removed, err)
		}
	}
	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1", s.Len())
	}
}

func TestTombstonesSurviveReopen(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c := &fakeClock{now: base}
	path := filepath.Join(t.TempDir(), "entries.jsonl")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.clock = c.Now
	if _, err := s.Put(entry("a", "A", base), entry("b", "B", base)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	c.now = base.Add(72 * time.Hour)
	if _, err := s.Purge(base.Add(time.Hour)); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Len() != 0 {
		t.Errorf("Len after reopen = %d, want 0", s2.Len())
	}
	st := s2.Stats()
	if st.Records != 4 || st.Garbage != 4 {
		t.Errorf("Stats = %+v, want 4 records all garbage", st)
	}
}

func TestCompactReclaimsGarbage(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c := &fakeClock{now: base}
	s, path := openClocked(t, c)

	for i := 0; i < 10; i++ {
		if _, err := s.Put(entry(fmt.Sprintf("e%02d", i), "Title", base.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	c.now = base.Add(24 * time.Hour)
	if _, err := s.Purge(base.Add(5 * time.Hour)); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	before := s.Stats()
	if before.Garbage == 0 {
		t.Fatal("expected garbage before compaction")
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after := s.Stats()
	if after.Garbage != 0 {
		t.Errorf("Garbage after compaction = %d, want 0", after.Garbage)
	}
	if after.Live != before.Live {
		t.Errorf("Live changed across compaction: %d -> %d", before.Live, after.Live)
	}
	if after.Bytes >= before.Bytes {
		t.Errorf("log did not shrink: %d -> %d", before.Bytes, after.Bytes)
	}

	// The on-disk size must match what Stats claims, and the compacted log
	// must contain exactly the live records.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() != after.Bytes {
		t.Errorf("file size %d, Stats says %d", fi.Size(), after.Bytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n := strings.Count(string(data), "\n"); int64(n) != after.Records {
		t.Errorf("log has %d lines, want %d", n, after.Records)
	}
	if strings.Contains(string(data), `"del":true`) {
		t.Error("tombstone survived compaction")
	}

	// Writing still works after a compaction, and a reopen sees everything.
	if _, err := s.Put(entry("after", "After", base.Add(48*time.Hour))); err != nil {
		t.Fatalf("Put after compact: %v", err)
	}
	want := s.Len()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Len() != want {
		t.Errorf("Len after reopen = %d, want %d", s2.Len(), want)
	}
}

func TestPurgeCompactsAutomatically(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c := &fakeClock{now: base}
	s, _ := openClocked(t, c)

	// Enough records that the compaction threshold applies, all old enough to
	// be purged, which makes the log entirely garbage.
	for i := 0; i < compactMinRecords; i++ {
		if _, err := s.Put(entry(fmt.Sprintf("e%03d", i), "T", base)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	c.now = base.Add(90 * 24 * time.Hour)
	removed, err := s.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if removed != compactMinRecords {
		t.Fatalf("removed = %d, want %d", removed, compactMinRecords)
	}
	st := s.Stats()
	if st.Records != 0 || st.Bytes != 0 || st.Live != 0 {
		t.Errorf("Stats after automatic compaction = %+v, want an empty log", st)
	}
}

func TestSmallLogIsNotCompactedAutomatically(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c := &fakeClock{now: base}
	s, _ := openClocked(t, c)
	if _, err := s.Put(entry("a", "A", base), entry("b", "B", base)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	c.now = base.Add(48 * time.Hour)
	if _, err := s.PurgeOlderThan(time.Hour); err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if st := s.Stats(); st.Records != 4 {
		t.Errorf("Records = %d, want 4 (no automatic compaction)", st.Records)
	}
}

func TestCompactOnClosedStore(t *testing.T) {
	s, _ := openClocked(t, &fakeClock{now: time.Now()})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Compact(); err == nil {
		t.Error("Compact on a closed store = nil error, want an error")
	}
}

func TestCompactLeavesNoTempFiles(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	s, path := openClocked(t, &fakeClock{now: base})
	if _, err := s.Put(entry("a", "A", base)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	names, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.compact-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("temp files left behind: %v", names)
	}
}
