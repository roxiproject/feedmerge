package search

import (
	"math"
	"sort"
	"strings"

	"github.com/roxiproject/feedmerge/internal/feed"
)

// BM25 parameters. k1 controls how quickly term frequency saturates and b how
// strongly long documents are penalized; these are the usual defaults.
const (
	k1 = 1.2
	b  = 0.75
)

// Result is one ranked hit.
type Result struct {
	Entry feed.Entry
	Score float64
	// Snippet is an excerpt of the body around the first matching term.
	Snippet string
	// Matched lists the query terms that were found in this document.
	Matched []string
}

// Search runs a query and returns up to limit results, best first. A limit of
// zero or less returns every match. An empty or all-stopword query returns no
// results rather than everything, so a stray "?q=" cannot dump the whole store.
func (idx *Index) Search(query string, limit int) []Result {
	return idx.SearchQuery(ParseQuery(query), limit)
}

// SearchQuery is Search over an already parsed query, which lets a caller
// inspect or rewrite the query before running it.
func (idx *Index) SearchQuery(q Query, limit int) []Result {
	if idx == nil || len(idx.docs) == 0 || q.Empty() {
		return nil
	}

	scores := make(map[int]float64)
	matched := make(map[int][]string)
	n := float64(len(idx.docs))

	for _, term := range q.scoringTerms() {
		post := idx.postings[term]
		if len(post) == 0 {
			continue
		}
		df := float64(len(post))
		// The +1 form of the IDF keeps the weight positive even for a term
		// that appears in most documents.
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		for _, p := range post {
			norm := p.tf * (k1 + 1) / (p.tf + k1*(1-b+b*idx.lengths[p.doc]/idx.avgLen))
			scores[p.doc] += idf * norm
			matched[p.doc] = append(matched[p.doc], term)
		}
	}

	// Phrase queries have no postings of their own; a document that satisfies
	// one earns a bonus proportional to the phrase length.
	for _, ph := range q.Phrases {
		if ph.Negate {
			continue
		}
		for doc := range idx.docs {
			if idx.hasPhrase(doc, ph.Terms) {
				scores[doc] += float64(len(ph.Terms))
				matched[doc] = append(matched[doc], strings.Join(ph.Terms, " "))
			}
		}
	}

	out := make([]Result, 0, len(scores))
	for doc, score := range scores {
		if !idx.satisfies(doc, q) {
			continue
		}
		out = append(out, Result{
			Entry:   idx.docs[doc].Entry,
			Score:   score,
			Snippet: Snippet(idx.docs[doc].Text, matched[doc], snippetRunes),
			Matched: dedupStrings(matched[doc]),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		di, dj := out[i].Entry.Date(), out[j].Entry.Date()
		if !di.Equal(dj) {
			return di.After(dj)
		}
		return out[i].Entry.ID < out[j].Entry.ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// satisfies checks the constraints that ranking alone cannot express: required
// terms, excluded terms and phrases.
func (idx *Index) satisfies(doc int, q Query) bool {
	for _, t := range q.Terms {
		present := idx.contains(doc, t.Word)
		if t.Negate && present {
			return false
		}
		if t.Require && !present {
			return false
		}
	}
	for _, ph := range q.Phrases {
		if idx.hasPhrase(doc, ph.Terms) == ph.Negate {
			return false
		}
	}
	return true
}

func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
