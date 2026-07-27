package feed

import (
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// trackingParams are query parameters that identify a campaign rather than a
// document, and so must be ignored when comparing URLs.
var trackingParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true,
	"utm_term": true, "utm_content": true, "utm_name": true, "utm_reader": true,
	"fbclid": true, "gclid": true, "mc_cid": true, "mc_eid": true,
	"ref": true, "source": true, "cmpid": true, "at_medium": true, "at_campaign": true,
}

// NormalizeURL produces a comparable form of a link: lowercased scheme and
// host, http upgraded to https, "www." dropped, default ports dropped,
// tracking parameters removed, remaining parameters sorted, fragment removed
// and a trailing slash trimmed. Unparseable input is returned trimmed.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	port := u.Port()
	if (u.Scheme == "https" && (port == "443" || port == "80")) || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	u.Fragment = ""
	u.RawFragment = ""
	if q := u.Query(); len(q) > 0 {
		for k := range q {
			if trackingParams[strings.ToLower(k)] {
				q.Del(k)
			}
		}
		u.RawQuery = q.Encode() // Encode sorts by key
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if u.Path == "" && u.RawQuery == "" {
		return u.Scheme + "://" + u.Host
	}
	return u.String()
}

// NormalizeTitle reduces a title to lowercase alphanumeric words so that
// punctuation, entity encoding and markup differences between feeds do not
// prevent a match.
func NormalizeTitle(s string) string {
	s = strings.ToLower(StripTags(s))
	var b strings.Builder
	lastSpace := true
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// TitleSimilarity returns the Jaccard similarity of the word sets of two
// titles, in [0,1]. Two empty titles are considered dissimilar (0) so that
// entries lacking titles are never merged on that basis alone.
func TitleSimilarity(a, b string) float64 {
	aw := strings.Fields(NormalizeTitle(a))
	bw := strings.Fields(NormalizeTitle(b))
	if len(aw) == 0 || len(bw) == 0 {
		return 0
	}
	set := make(map[string]bool, len(aw))
	for _, w := range aw {
		set[w] = true
	}
	seen := make(map[string]bool, len(bw))
	inter := 0
	for _, w := range bw {
		if seen[w] {
			continue
		}
		seen[w] = true
		if set[w] {
			inter++
		}
	}
	union := len(set) + len(seen) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// DedupOptions controls Dedup.
type DedupOptions struct {
	// TitleThreshold is the minimum Jaccard similarity at which two entries
	// with different ids and links are considered the same story. Values <= 0
	// disable title-based matching.
	TitleThreshold float64
	// TitleWindow bounds how far apart (in time) two entries may be published
	// and still be matched by title. Zero disables the time constraint.
	TitleWindow int64 // seconds
}

// DefaultDedupOptions are the settings used when a config omits them.
func DefaultDedupOptions() DedupOptions {
	return DedupOptions{TitleThreshold: 0.9, TitleWindow: 3 * 24 * 3600}
}

// Dedup merges entries that refer to the same item. Matching proceeds in three
// stages: exact GUID/id, normalized URL, then title similarity. The first
// occurrence of a duplicate group wins, except that an entry with a real date
// replaces one without.
func Dedup(entries []Entry, opts DedupOptions) []Entry {
	out := make([]Entry, 0, len(entries))
	byID := make(map[string]int, len(entries))
	byURL := make(map[string]int, len(entries))

	for _, e := range entries {
		idx := -1
		if e.ID != "" {
			if i, ok := byID[e.ID]; ok {
				idx = i
			}
		}
		nurl := NormalizeURL(e.Link)
		if idx < 0 && nurl != "" {
			if i, ok := byURL[nurl]; ok {
				idx = i
			}
		}
		if idx < 0 && opts.TitleThreshold > 0 {
			nt := NormalizeTitle(e.Title)
			if nt != "" {
				for i := range out {
					if !withinWindow(out[i], e, opts.TitleWindow) {
						continue
					}
					if TitleSimilarity(out[i].Title, e.Title) >= opts.TitleThreshold {
						idx = i
						break
					}
				}
			}
		}
		if idx >= 0 {
			out[idx] = mergeEntries(out[idx], e)
			if out[idx].ID != "" {
				byID[out[idx].ID] = idx
			}
			if u := NormalizeURL(out[idx].Link); u != "" {
				byURL[u] = idx
			}
			if e.ID != "" {
				byID[e.ID] = idx
			}
			if nurl != "" {
				byURL[nurl] = idx
			}
			continue
		}
		out = append(out, e)
		i := len(out) - 1
		if e.ID != "" {
			byID[e.ID] = i
		}
		if nurl != "" {
			byURL[nurl] = i
		}
	}
	return out
}

func withinWindow(a, b Entry, window int64) bool {
	if window <= 0 {
		return true
	}
	ta, tb := a.Date(), b.Date()
	if ta.IsZero() || tb.IsZero() {
		return true
	}
	d := ta.Unix() - tb.Unix()
	if d < 0 {
		d = -d
	}
	return d <= window
}

// mergeEntries keeps the richer of two duplicates: the winner is the existing
// entry, but empty fields are filled in from the newcomer.
func mergeEntries(keep, other Entry) Entry {
	if keep.Title == "" {
		keep.Title = other.Title
	}
	if keep.Link == "" {
		keep.Link = other.Link
	}
	if keep.Content == "" {
		keep.Content = other.Content
	}
	if keep.Summary == "" {
		keep.Summary = other.Summary
	}
	if keep.Author == "" {
		keep.Author = other.Author
	}
	if keep.Published.IsZero() {
		keep.Published = other.Published
		keep.PublishedRaw = other.PublishedRaw
	}
	if keep.Updated.IsZero() {
		keep.Updated = other.Updated
	}
	if keep.IDSource == "hash" && other.IDSource != "hash" {
		keep.ID, keep.IDSource = other.ID, other.IDSource
	}
	if len(keep.Categories) == 0 {
		keep.Categories = other.Categories
	}
	return keep
}

// SortByDate orders entries newest first; entries without a date sort last but
// keep their relative order.
func SortByDate(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		ti, tj := entries[i].Date(), entries[j].Date()
		switch {
		case ti.IsZero() && tj.IsZero():
			return false
		case ti.IsZero():
			return false
		case tj.IsZero():
			return true
		default:
			return ti.After(tj)
		}
	})
}
