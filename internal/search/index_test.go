package search

import (
	"strings"
	"testing"

	"github.com/roxiproject/feedmerge/internal/feed"
)

func TestEmptyIndex(t *testing.T) {
	idx := New(nil)
	if idx.Len() != 0 || idx.Terms() != 0 {
		t.Errorf("Len/Terms = %d/%d, want 0/0", idx.Len(), idx.Terms())
	}
	if got := idx.Search("anything", 0); got != nil {
		t.Errorf("Search on empty index = %v, want nil", got)
	}
}

func TestNilIndexIsSafe(t *testing.T) {
	var idx *Index
	if idx.Len() != 0 || idx.Terms() != 0 || idx.Search("x", 5) != nil {
		t.Error("nil index did not behave as empty")
	}
}

func TestIndexCounts(t *testing.T) {
	idx := testIndex()
	if idx.Len() != 4 {
		t.Errorf("Len = %d, want 4", idx.Len())
	}
	if idx.Terms() < 20 {
		t.Errorf("Terms = %d, want a realistic vocabulary", idx.Terms())
	}
}

func TestIndexEntriesRoundTrip(t *testing.T) {
	idx := testIndex()
	got := idx.Entries()
	if len(got) != idx.Len() {
		t.Fatalf("Entries returned %d entries, want %d", len(got), idx.Len())
	}
	if strings.Join(entryIDs(got), "|") != "a|b|c|d" {
		t.Errorf("Entries = %v, want index order", entryIDs(got))
	}
}

func TestIndexContainsMatchesPostings(t *testing.T) {
	idx := testIndex()
	for doc := 0; doc < idx.Len(); doc++ {
		for term := range idx.postings {
			want := false
			for _, p := range idx.postings[term] {
				if p.doc == doc {
					want = true
					break
				}
			}
			if got := idx.contains(doc, term); got != want {
				t.Fatalf("contains(%d, %q) = %v, want %v", doc, term, got, want)
			}
		}
	}
	if idx.contains(0, "termthatwasneverindexed") {
		t.Error("contains reported an unknown term as present")
	}
}

func TestIndexPostingsAreSortedByDocument(t *testing.T) {
	idx := testIndex()
	for term, post := range idx.postings {
		for i := 1; i < len(post); i++ {
			if post[i-1].doc >= post[i].doc {
				t.Fatalf("postings for %q are not ascending: %v", term, post)
			}
		}
	}
}

func entryIDs(entries []feed.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}
