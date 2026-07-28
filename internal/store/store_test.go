package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roxiproject/feedmerge/internal/feed"
)

func entry(id, title string, published time.Time) feed.Entry {
	return feed.Entry{
		ID: id, IDSource: "guid", Title: title,
		Link:      "https://example.org/" + id,
		Content:   "<p>Body of " + title + "</p>",
		Published: published, Updated: published,
		SourceTitle: "Example", SourceURL: "https://example.org/feed.xml",
	}
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "entries.jsonl"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open(\"\") = nil error, want an error")
	}
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Error("Open(directory) = nil error, want an error")
	}
}

func TestPutAndAll(t *testing.T) {
	s := openTemp(t)
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	n, err := s.Put(entry("a", "Alpha", base), entry("b", "Beta", base.Add(time.Hour)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != 2 {
		t.Fatalf("Put added %d, want 2", n)
	}
	// Re-putting an existing id is a no-op.
	if n, err := s.Put(entry("a", "Alpha changed", base)); err != nil || n != 0 {
		t.Fatalf("Put(existing) = %d, %v, want 0, nil", n, err)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
	all := s.All()
	if len(all) != 2 || all[0].Entry.ID != "b" || all[1].Entry.ID != "a" {
		t.Fatalf("All order = %v", ids(s.Entries()))
	}
	if all[0].StoredAt.IsZero() {
		t.Error("StoredAt not set")
	}
	if got, ok := s.Get("a"); !ok || got.Entry.Title != "Alpha" {
		t.Errorf("Get(a) = %+v, %v", got, ok)
	}
	if _, ok := s.Get("missing"); ok {
		t.Error("Get(missing) reported found")
	}
}

func TestPutIgnoresEmptyID(t *testing.T) {
	s := openTemp(t)
	n, err := s.Put(feed.Entry{Title: "no id"})
	if err != nil || n != 0 {
		t.Fatalf("Put(no id) = %d, %v, want 0, nil", n, err)
	}
	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0", s.Len())
	}
}

func TestAllSortsUndatedLast(t *testing.T) {
	s := openTemp(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Put(
		entry("undated", "No date", time.Time{}),
		entry("old", "Old", base),
		entry("new", "New", base.Add(48*time.Hour)),
	); err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := []string{"new", "old", "undated"}
	if got := ids(s.Entries()); !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestReopenRestoresEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "entries.jsonl")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Put(entry("a", "Alpha", base), entry("b", "Beta", base)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Len() != 2 {
		t.Fatalf("Len after reopen = %d, want 2", s2.Len())
	}
	got, ok := s2.Get("a")
	if !ok {
		t.Fatal("entry a missing after reopen")
	}
	if got.Entry.Title != "Alpha" || got.Entry.Link != "https://example.org/a" ||
		!got.Entry.Published.Equal(base) || got.Entry.SourceTitle != "Example" {
		t.Errorf("round trip lost fields: %+v", got.Entry)
	}
	// Appending after a reopen must not corrupt the log.
	if _, err := s2.Put(entry("c", "Gamma", base)); err != nil {
		t.Fatalf("Put after reopen: %v", err)
	}
	if s2.Len() != 3 {
		t.Errorf("Len = %d, want 3", s2.Len())
	}
}

func TestReopenDiscardsTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entries.jsonl")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Put(entry("a", "Alpha", time.Now().UTC())); err != nil {
		t.Fatalf("Put: %v", err)
	}
	s.Close()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(`{"v":1,"id":"torn-record","ti`); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s2.Len())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "torn-record") {
		t.Error("torn record was not truncated from the log")
	}
}

func TestReopenRejectsCorruptMiddleRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entries.jsonl")
	if err := os.WriteFile(path, []byte("not json at all\n{\"v\":1,\"id\":\"a\"}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open on corrupt log = nil error, want an error")
	}
}

func TestReopenRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entries.jsonl")
	if err := os.WriteFile(path, []byte("{\"v\":99,\"id\":\"a\"}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("Open = %v, want a version error", err)
	}
}

func TestConcurrentPutAndRead(t *testing.T) {
	s := openTemp(t)
	base := time.Now().UTC()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(2)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				id := string(rune('a'+w)) + string(rune('0'+i%10)) + string(rune('0'+i/10))
				if _, err := s.Put(entry(id, "T"+id, base)); err != nil {
					t.Errorf("Put: %v", err)
					return
				}
			}
		}(w)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				s.All()
				s.Len()
			}
		}()
	}
	wg.Wait()
	if s.Len() != 100 {
		t.Errorf("Len = %d, want 100", s.Len())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "e.jsonl"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func ids(entries []feed.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
