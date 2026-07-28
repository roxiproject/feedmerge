// Package search provides a full-text index over feed entries.
//
// The index is an in-memory inverted index: every document is tokenized once
// into terms, and each term maps to the documents that contain it together
// with a weighted term frequency. Ranking is Okapi BM25, the standard
// probabilistic model, with per-field boosts so that a hit in a title counts
// for more than a hit deep in the body.
//
// Indexes are immutable once built. A refresh builds a new one and swaps it in,
// which keeps readers lock-free and avoids a half-updated index ever being
// queried.
package search

import (
	"sort"
	"strings"

	"github.com/roxiproject/feedmerge/internal/extract"
	"github.com/roxiproject/feedmerge/internal/feed"
)

// Field boosts. A term in the title is worth three body occurrences, which is
// what makes a search for a headline return that headline rather than every
// entry that mentions it in passing.
const (
	boostTitle    = 3.0
	boostCategory = 2.0
	boostAuthor   = 1.5
	boostSource   = 1.0
	boostBody     = 1.0
)

// Document is one indexed entry.
type Document struct {
	Entry feed.Entry
	// Text is the plain-text body used for scoring and snippets.
	Text string
}

// posting records one term's weighted frequency inside one document. A term's
// postings are appended in document order, so the list is always sorted by doc
// and can be searched by bisection.
type posting struct {
	doc int
	tf  float64
}

// Index is a queryable inverted index. It is read-only after New returns and is
// therefore safe for concurrent use.
type Index struct {
	docs []Document
	// tokens holds each document's title and body terms joined by spaces, used
	// to answer phrase queries without storing positions.
	tokens   []string
	postings map[string][]posting
	lengths  []float64
	avgLen   float64
}

// New builds an index over entries. Entry bodies are converted to plain text
// first, so markup never pollutes the term statistics.
func New(entries []feed.Entry) *Index {
	idx := &Index{postings: make(map[string][]posting, len(entries)*16)}
	for _, e := range entries {
		text := extract.Plain(firstNonEmpty(e.Content, e.Summary))
		idx.add(Document{Entry: e, Text: text})
	}
	idx.finish()
	return idx
}

func (idx *Index) add(d Document) {
	docID := len(idx.docs)
	idx.docs = append(idx.docs, d)

	weights := make(map[string]float64)
	addField := func(text string, boost float64) []string {
		terms := Tokenize(text)
		for _, t := range terms {
			weights[t] += boost
		}
		return terms
	}
	title := addField(d.Entry.Title, boostTitle)
	addField(strings.Join(d.Entry.Categories, " "), boostCategory)
	addField(d.Entry.Author, boostAuthor)
	addField(d.Entry.SourceTitle, boostSource)
	body := addField(d.Text, boostBody)

	// The phrase haystack covers title and body, which is where a reader
	// expects a quoted phrase to be found.
	idx.tokens = append(idx.tokens, strings.Join(append(title, body...), " "))

	var length float64
	for term, w := range weights {
		idx.postings[term] = append(idx.postings[term], posting{doc: docID, tf: w})
		length += w
	}
	idx.lengths = append(idx.lengths, length)
}

// finish computes the collection statistics BM25 needs. An empty collection
// gets an average length of one so that scoring never divides by zero.
func (idx *Index) finish() {
	var total float64
	for _, l := range idx.lengths {
		total += l
	}
	if len(idx.lengths) > 0 {
		idx.avgLen = total / float64(len(idx.lengths))
	}
	if idx.avgLen == 0 {
		idx.avgLen = 1
	}
}

// Len reports how many documents are indexed.
func (idx *Index) Len() int {
	if idx == nil {
		return 0
	}
	return len(idx.docs)
}

// Terms reports how many distinct terms are indexed.
func (idx *Index) Terms() int {
	if idx == nil {
		return 0
	}
	return len(idx.postings)
}

// Entries returns the indexed entries in index order.
func (idx *Index) Entries() []feed.Entry {
	if idx == nil {
		return nil
	}
	out := make([]feed.Entry, len(idx.docs))
	for i, d := range idx.docs {
		out[i] = d.Entry
	}
	return out
}

// contains reports whether doc holds term, by bisecting the term's postings.
func (idx *Index) contains(doc int, term string) bool {
	post := idx.postings[term]
	i := sort.Search(len(post), func(i int) bool { return post[i].doc >= doc })
	return i < len(post) && post[i].doc == doc
}

// hasPhrase reports whether the given terms appear adjacently in doc.
func (idx *Index) hasPhrase(doc int, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	hay := " " + idx.tokens[doc] + " "
	return strings.Contains(hay, " "+strings.Join(terms, " ")+" ")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
