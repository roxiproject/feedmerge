// Package store persists merged entries in an append-only log so that they
// survive a restart.
//
// The log is JSON Lines: one self-describing record per line, appended and
// fsynced, never rewritten in place. Deleting a record appends a tombstone
// instead of editing the file, which keeps every write sequential and keeps a
// half-written line at the tail recoverable - the reader stops at the first
// line it cannot decode and truncates the log there.
//
// An index from entry id to its most recent record is held in memory and
// rebuilt from the log on open, so a lookup never touches the disk.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roxiproject/feedmerge/internal/feed"
)

// recordVersion is written with every record so that a future format change can
// be detected rather than misread.
const recordVersion = 1

// maxLineBytes caps a single log line. A record longer than this is refused on
// write and treated as corruption on read.
const maxLineBytes = 8 << 20

// record is the on-disk form of a stored entry. The field names are part of the
// file format, so they are tagged explicitly rather than inherited from the Go
// field names.
type record struct {
	V            int       `json:"v"`
	ID           string    `json:"id"`
	Deleted      bool      `json:"del,omitempty"`
	IDSource     string    `json:"id_source,omitempty"`
	Title        string    `json:"title,omitempty"`
	Link         string    `json:"link,omitempty"`
	Content      string    `json:"content,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	Author       string    `json:"author,omitempty"`
	Published    time.Time `json:"published,omitempty"`
	Updated      time.Time `json:"updated,omitempty"`
	PublishedRaw string    `json:"published_raw,omitempty"`
	Categories   []string  `json:"categories,omitempty"`
	SourceTitle  string    `json:"source_title,omitempty"`
	SourceURL    string    `json:"source_url,omitempty"`
	StoredAt     time.Time `json:"stored_at"`
}

func toRecord(e feed.Entry, now time.Time) record {
	return record{
		V: recordVersion, ID: e.ID, IDSource: e.IDSource,
		Title: e.Title, Link: e.Link, Content: e.Content, Summary: e.Summary,
		Author: e.Author, Published: e.Published, Updated: e.Updated,
		PublishedRaw: e.PublishedRaw, Categories: e.Categories,
		SourceTitle: e.SourceTitle, SourceURL: e.SourceURL, StoredAt: now,
	}
}

func (r record) entry() feed.Entry {
	return feed.Entry{
		ID: r.ID, IDSource: r.IDSource, Title: r.Title, Link: r.Link,
		Content: r.Content, Summary: r.Summary, Author: r.Author,
		Published: r.Published, Updated: r.Updated, PublishedRaw: r.PublishedRaw,
		Categories: r.Categories, SourceTitle: r.SourceTitle, SourceURL: r.SourceURL,
	}
}

// Stored is a stored entry together with the time it was first written.
type Stored struct {
	Entry    feed.Entry
	StoredAt time.Time
}

// Age returns how old a stored record is, relative to now.
func (s Stored) Age(now time.Time) time.Duration { return now.Sub(s.StoredAt) }

// Store is an append-only entry log. It is safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	path  string
	f     *os.File
	w     *bufio.Writer
	size  int64
	live  map[string]record
	lines int64 // records in the log, live and superseded
	clock func() time.Time
}

// Open opens (creating if necessary) the log at path. A truncated final line
// left behind by a crash is discarded and the file truncated to the last intact
// record; any other corruption is reported as an error.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("store: empty path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	s := &Store{path: path, f: f, live: make(map[string]record), clock: time.Now}
	if err := s.replay(); err != nil {
		f.Close()
		return nil, err
	}
	s.w = bufio.NewWriterSize(f, 64<<10)
	return s, nil
}

// replay reads the whole log, rebuilding the live set.
func (s *Store) replay() error {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	br := bufio.NewReaderSize(s.f, 64<<10)
	var off int64
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			var r record
			if jsonErr := json.Unmarshal(line, &r); jsonErr != nil || r.ID == "" {
				if errors.Is(err, io.EOF) {
					// Partial write at the tail: drop it.
					break
				}
				return fmt.Errorf("store: %s: corrupt record at offset %d", s.path, off)
			}
			if r.V != recordVersion {
				return fmt.Errorf("store: %s: record at offset %d has version %d, want %d",
					s.path, off, r.V, recordVersion)
			}
			if errors.Is(err, io.EOF) {
				// Last line has no terminating newline; it may still be a
				// complete record, but we cannot tell, so keep it and let the
				// next append add the newline.
				s.apply(r)
				off += int64(len(line))
				break
			}
			s.apply(r)
			off += int64(len(line)) + 1
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("store: %s: %w", s.path, err)
		}
	}
	if err := s.f.Truncate(off); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if _, err := s.f.Seek(off, io.SeekStart); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	s.size = off
	return nil
}

// apply folds one replayed record into the in-memory index. Every record other
// than the surviving one for each id is garbage that compaction can drop, which
// is exactly lines minus the size of the live set.
func (s *Store) apply(r record) {
	s.lines++
	if r.Deleted {
		delete(s.live, r.ID)
		return
	}
	s.live[r.ID] = r
}

// readLine reads one newline-terminated line, without the newline. It returns
// io.EOF alongside any trailing bytes that were not newline terminated.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLineBytes {
			return nil, fmt.Errorf("record exceeds %d bytes", maxLineBytes)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return buf, err
		}
		return buf[:len(buf)-1], nil
	}
}

// Put appends entries that are not already stored and returns how many were
// added. Entries already present keep their original StoredAt, so an entry that
// keeps reappearing in the upstream feed does not repeatedly refresh its age
// and escape the retention policy.
func (s *Store) Put(entries ...feed.Entry) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock().UTC()
	added := 0
	for _, e := range entries {
		if e.ID == "" {
			continue
		}
		if _, ok := s.live[e.ID]; ok {
			continue
		}
		if err := s.appendLocked(toRecord(e, now)); err != nil {
			return added, err
		}
		added++
	}
	if added > 0 {
		if err := s.flushLocked(); err != nil {
			return added, err
		}
	}
	return added, nil
}

func (s *Store) appendLocked(r record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("store: encode %q: %w", r.ID, err)
	}
	if len(b)+1 > maxLineBytes {
		return fmt.Errorf("store: record %q exceeds %d bytes", r.ID, maxLineBytes)
	}
	b = append(b, '\n')
	n, err := s.w.Write(b)
	s.size += int64(n)
	if err != nil {
		return fmt.Errorf("store: write: %w", err)
	}
	s.apply(r)
	return nil
}

func (s *Store) flushLocked() error {
	if err := s.w.Flush(); err != nil {
		return fmt.Errorf("store: flush: %w", err)
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("store: sync: %w", err)
	}
	return nil
}

// Len reports how many live entries are stored.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.live)
}

// Path returns the log file path.
func (s *Store) Path() string { return s.path }

// Get returns one stored entry by id.
func (s *Store) Get(id string) (Stored, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.live[id]
	if !ok {
		return Stored{}, false
	}
	return Stored{Entry: r.entry(), StoredAt: r.StoredAt}, true
}

// All returns every live entry, newest first. Entries without a usable
// publication date sort last, matching feed.SortByDate.
func (s *Store) All() []Stored {
	s.mu.RLock()
	out := make([]Stored, 0, len(s.live))
	for _, r := range s.live {
		out = append(out, Stored{Entry: r.entry(), StoredAt: r.StoredAt})
	}
	s.mu.RUnlock()

	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].Entry.Date(), out[j].Entry.Date()
		switch {
		case di.IsZero() && dj.IsZero():
			return out[i].Entry.ID < out[j].Entry.ID
		case di.IsZero():
			return false
		case dj.IsZero():
			return true
		case di.Equal(dj):
			return out[i].Entry.ID < out[j].Entry.ID
		}
		return di.After(dj)
	})
	return out
}

// Entries returns the live entries as feed entries, newest first.
func (s *Store) Entries() []feed.Entry {
	all := s.All()
	out := make([]feed.Entry, len(all))
	for i, st := range all {
		out[i] = st.Entry
	}
	return out
}

// Close flushes and closes the log.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.flushLocked()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	s.f, s.w = nil, nil
	return err
}
