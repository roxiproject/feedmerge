package feed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrUnknownFormat is returned when the document root is neither an RSS 2.0
// <rss> element nor an Atom 1.0 <feed> element.
var ErrUnknownFormat = errors.New("feed: unrecognized document, expected <rss> or <feed>")

// Parse reads a feed document and returns its normalized form. baseURL is the
// URL the document was fetched from; it is used to resolve relative links and
// is recorded on every entry as the source URL.
func Parse(r io.Reader, baseURL string) (*Feed, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("feed: read: %w", err)
	}
	return ParseBytes(data, baseURL)
}

// ParseBytes is Parse over an in-memory document.
func ParseBytes(data []byte, baseURL string) (*Feed, error) {
	data = bytes.TrimPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("\xef\xbb\xbf"))
	data = bytes.TrimLeft(data, " \t\r\n")
	if len(data) == 0 {
		return nil, errors.New("feed: empty document")
	}
	root, err := rootElement(data)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(root) {
	case "rss":
		return parseRSS(data, baseURL)
	case "feed":
		return parseAtom(data, baseURL)
	case "rdf":
		return nil, fmt.Errorf("%w (got RSS 1.0/RDF)", ErrUnknownFormat)
	default:
		return nil, fmt.Errorf("%w (got <%s>)", ErrUnknownFormat, root)
	}
}

// rootElement returns the local name of the first element in the document.
func rootElement(data []byte) (string, error) {
	d := newDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", ErrUnknownFormat
			}
			return "", fmt.Errorf("feed: xml: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local, nil
		}
	}
}

// ---------------------------------------------------------------- RSS 2.0

type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title string `xml:"title"`
	// AtomLinks carries the Atom link elements RSS feeds borrow to advertise
	// their WebSub hub and their own canonical URL. It has to be declared
	// before Link: an untagged-namespace field like <link> matches an element
	// of any namespace, so whichever of the two comes first wins.
	AtomLinks     []atomLink `xml:"http://www.w3.org/2005/Atom link"`
	Link          string     `xml:"link"`
	Description   string     `xml:"description"`
	PubDate       string     `xml:"pubDate"`
	LastBuildDate string     `xml:"lastBuildDate"`
	Items         []rssItem  `xml:"item"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	Encoded     string   `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	GUID        rssGUID  `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	DCDate      string   `xml:"http://purl.org/dc/elements/1.1/ date"`
	Author      string   `xml:"author"`
	Creator     string   `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Categories  []string `xml:"category"`
	Base        string   `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

func parseRSS(data []byte, baseURL string) (*Feed, error) {
	var doc rssDoc
	if err := newDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("feed: parse rss: %w", err)
	}
	ch := doc.Channel
	f := &Feed{
		Format:      "rss",
		Title:       text(ch.Title),
		Description: text(ch.Description),
		Link:        resolve(baseURL, ch.Link),
	}
	if t, err := ParseDate(firstNonEmpty(ch.LastBuildDate, ch.PubDate)); err == nil {
		f.Updated = t
	}
	f.Hubs, f.Self = relLinks(ch.AtomLinks, baseURL)
	base := firstNonEmpty(f.Link, baseURL)
	for _, it := range ch.Items {
		itemBase := base
		if it.Base != "" {
			itemBase = resolve(base, it.Base)
		}
		e := Entry{
			Title:       text(it.Title),
			Link:        resolve(itemBase, it.Link),
			Content:     firstNonEmpty(strings.TrimSpace(it.Encoded), strings.TrimSpace(it.Description)),
			Summary:     strings.TrimSpace(it.Description),
			Author:      text(firstNonEmpty(it.Creator, it.Author)),
			SourceTitle: f.Title,
			SourceURL:   baseURL,
		}
		for _, c := range it.Categories {
			if c = text(c); c != "" {
				e.Categories = append(e.Categories, c)
			}
		}
		raw := firstNonEmpty(it.PubDate, it.DCDate)
		e.PublishedRaw = strings.TrimSpace(raw)
		if t, err := ParseDate(raw); err == nil {
			e.Published = t
		}
		e.Updated = e.Published

		guid := strings.TrimSpace(it.GUID.Value)
		// A guid that is a permalink and the item has no link of its own is
		// also usable as the entry link.
		if e.Link == "" && guid != "" && !strings.EqualFold(it.GUID.IsPermaLink, "false") &&
			(strings.HasPrefix(guid, "http://") || strings.HasPrefix(guid, "https://")) {
			e.Link = resolve(itemBase, guid)
		}
		assignID(&e, guid)
		f.Entries = append(f.Entries, e)
	}
	return f, nil
}

// ---------------------------------------------------------------- Atom 1.0

type atomDoc struct {
	XMLName  xml.Name    `xml:"feed"`
	Title    atomText    `xml:"title"`
	Subtitle atomText    `xml:"subtitle"`
	Links    []atomLink  `xml:"link"`
	Updated  string      `xml:"updated"`
	Base     string      `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

// atomText models an Atom text construct: plain text, escaped HTML, or inline
// XHTML, all of which we flatten to a string.
type atomText struct {
	Type  string `xml:"type,attr"`
	Chars string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

func (t atomText) value() string {
	if strings.EqualFold(t.Type, "xhtml") {
		return strings.TrimSpace(t.Inner)
	}
	return text(t.Chars)
}

// html returns the construct as markup, without decoding character references:
// content and summary bodies are handed to readers as-is.
func (t atomText) html() string {
	if strings.EqualFold(t.Type, "xhtml") {
		return strings.TrimSpace(t.Inner)
	}
	return strings.TrimSpace(t.Chars)
}

type atomEntry struct {
	Title      atomText       `xml:"title"`
	ID         string         `xml:"id"`
	Links      []atomLink     `xml:"link"`
	Updated    string         `xml:"updated"`
	Published  string         `xml:"published"`
	Issued     string         `xml:"issued"` // Atom 0.3 leftover
	Summary    atomText       `xml:"summary"`
	Content    atomText       `xml:"content"`
	Authors    []atomPerson   `xml:"author"`
	Categories []atomCategory `xml:"category"`
	Base       string         `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
}

type atomPerson struct {
	Name string `xml:"name"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

func parseAtom(data []byte, baseURL string) (*Feed, error) {
	var doc atomDoc
	if err := newDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("feed: parse atom: %w", err)
	}
	base := baseURL
	if doc.Base != "" {
		base = resolve(baseURL, doc.Base)
	}
	f := &Feed{
		Format:      "atom",
		Title:       doc.Title.value(),
		Description: doc.Subtitle.value(),
		Link:        resolve(base, pickLink(doc.Links)),
	}
	if t, err := ParseDate(doc.Updated); err == nil {
		f.Updated = t
	}
	f.Hubs, f.Self = relLinks(doc.Links, base)
	for _, ae := range doc.Entries {
		entryBase := base
		if ae.Base != "" {
			entryBase = resolve(base, ae.Base)
		}
		e := Entry{
			Title:       ae.Title.value(),
			Link:        resolve(entryBase, pickLink(ae.Links)),
			Content:     ae.Content.html(),
			Summary:     ae.Summary.html(),
			SourceTitle: f.Title,
			SourceURL:   baseURL,
		}
		if e.Content == "" {
			e.Content = e.Summary
		}
		for _, a := range ae.Authors {
			if n := text(a.Name); n != "" {
				e.Author = n
				break
			}
		}
		for _, c := range ae.Categories {
			if v := text(firstNonEmpty(c.Label, c.Term)); v != "" {
				e.Categories = append(e.Categories, v)
			}
		}
		pubRaw := firstNonEmpty(ae.Published, ae.Issued, ae.Updated)
		e.PublishedRaw = strings.TrimSpace(pubRaw)
		if t, err := ParseDate(pubRaw); err == nil {
			e.Published = t
		}
		if t, err := ParseDate(ae.Updated); err == nil {
			e.Updated = t
		} else {
			e.Updated = e.Published
		}
		assignID(&e, strings.TrimSpace(ae.ID))
		f.Entries = append(f.Entries, e)
	}
	return f, nil
}

// pickLink chooses the entry/feed permalink: rel="alternate" wins, then a link
// with no rel, then the first href present.
func pickLink(links []atomLink) string {
	var noRel, any string
	for _, l := range links {
		if l.Href == "" {
			continue
		}
		switch strings.ToLower(l.Rel) {
		case "alternate":
			return l.Href
		case "":
			if noRel == "" {
				noRel = l.Href
			}
		}
		if any == "" {
			any = l.Href
		}
	}
	return firstNonEmpty(noRel, any)
}

// relLinks extracts the WebSub hub URLs and the rel="self" URL from a set of
// Atom link elements. Duplicate hubs are collapsed, preserving first-seen
// order, and every href is resolved against base.
func relLinks(links []atomLink, base string) (hubs []string, self string) {
	seen := map[string]bool{}
	for _, l := range links {
		href := resolve(base, l.Href)
		if href == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(l.Rel)) {
		case "hub":
			if !seen[href] {
				seen[href] = true
				hubs = append(hubs, href)
			}
		case "self":
			if self == "" {
				self = href
			}
		}
	}
	return hubs, self
}

// ---------------------------------------------------------------- helpers

// assignID implements the identity fallback chain: guid/id, then link, then a
// hash of the title and publication date.
func assignID(e *Entry, guid string) {
	if guid != "" {
		e.ID, e.IDSource = guid, "guid"
		return
	}
	if e.Link != "" {
		e.ID, e.IDSource = e.Link, "link"
		return
	}
	key := StripTags(e.Title) + "\x00" + e.PublishedRaw
	sum := sha256.Sum256([]byte(key))
	e.ID, e.IDSource = "urn:feedmerge:"+hex.EncodeToString(sum[:16]), "hash"
}

func text(s string) string { return strings.TrimSpace(DecodeEntities(s)) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
