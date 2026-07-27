// Package feed implements parsing, normalization, deduplication and
// re-encoding of RSS 2.0 and Atom 1.0 feeds using only the standard library.
package feed

import (
	"net/url"
	"strings"
	"time"
)

// Entry is the normalized representation of a single feed item, independent of
// whether it originated from an RSS <item> or an Atom <entry>.
type Entry struct {
	// ID is the stable identity of the entry. It is derived from the source
	// feed's guid/id when present, otherwise from the link, otherwise from a
	// hash of title+published (see assignID).
	ID string
	// IDSource records how ID was derived: "guid", "link" or "hash".
	IDSource string

	Title     string
	Link      string
	Content   string
	Summary   string
	Author    string
	Published time.Time
	Updated   time.Time
	// PublishedRaw keeps the unparsed date string, useful for diagnostics when
	// a feed uses a format we could not decode.
	PublishedRaw string
	Categories   []string

	// SourceTitle and SourceURL identify the feed this entry was merged from.
	SourceTitle string
	SourceURL   string
}

// Date returns the best timestamp available for ordering purposes.
func (e Entry) Date() time.Time {
	if !e.Published.IsZero() {
		return e.Published
	}
	return e.Updated
}

// Feed is a normalized feed document.
type Feed struct {
	Title       string
	Link        string
	Description string
	Updated     time.Time
	Entries     []Entry
	// Format is "rss" or "atom", describing the document that was parsed.
	Format string
}

// resolve resolves ref against base. If base is empty or either URL fails to
// parse, ref is returned unchanged.
func resolve(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || base == "" {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}
