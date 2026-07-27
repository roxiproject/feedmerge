package feed

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"time"
)

// Meta describes the merged feed being emitted.
type Meta struct {
	Title       string
	Link        string
	Description string
	// SelfLink is the canonical URL of the merged feed itself, used for
	// rel="self" and JSON Feed's feed_url.
	SelfLink string
	Updated  time.Time
}

// WriteRSS writes the entries as an RSS 2.0 document.
func WriteRSS(w io.Writer, m Meta, entries []Entry) error {
	type guidOut struct {
		Value       string `xml:",chardata"`
		IsPermaLink bool   `xml:"isPermaLink,attr"`
	}
	type itemOut struct {
		XMLName     xml.Name `xml:"item"`
		Title       string   `xml:"title"`
		Link        string   `xml:"link,omitempty"`
		GUID        guidOut  `xml:"guid"`
		PubDate     string   `xml:"pubDate,omitempty"`
		Author      string   `xml:"dc:creator,omitempty"`
		Categories  []string `xml:"category,omitempty"`
		Description string   `xml:"description"`
		Source      string   `xml:"source,omitempty"`
	}
	type channelOut struct {
		XMLName       xml.Name  `xml:"channel"`
		Title         string    `xml:"title"`
		Link          string    `xml:"link"`
		Description   string    `xml:"description"`
		Generator     string    `xml:"generator"`
		LastBuildDate string    `xml:"lastBuildDate,omitempty"`
		Items         []itemOut `xml:"item"`
	}
	type rssOut struct {
		XMLName xml.Name   `xml:"rss"`
		Version string     `xml:"version,attr"`
		DC      string     `xml:"xmlns:dc,attr"`
		Channel channelOut `xml:"channel"`
	}

	ch := channelOut{
		Title:       m.Title,
		Link:        m.Link,
		Description: m.Description,
		Generator:   Generator,
	}
	if !m.Updated.IsZero() {
		ch.LastBuildDate = m.Updated.UTC().Format(time.RFC1123Z)
	}
	for _, e := range entries {
		it := itemOut{
			Title:       e.Title,
			Link:        e.Link,
			GUID:        guidOut{Value: e.ID, IsPermaLink: e.IDSource == "link"},
			Author:      e.Author,
			Categories:  e.Categories,
			Description: firstNonEmpty(e.Content, e.Summary),
			Source:      e.SourceTitle,
		}
		if d := e.Date(); !d.IsZero() {
			it.PubDate = d.UTC().Format(time.RFC1123Z)
		}
		ch.Items = append(ch.Items, it)
	}
	return writeXML(w, rssOut{Version: "2.0", DC: "http://purl.org/dc/elements/1.1/", Channel: ch})
}

// WriteAtom writes the entries as an Atom 1.0 document.
func WriteAtom(w io.Writer, m Meta, entries []Entry) error {
	type linkOut struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr,omitempty"`
		Type string `xml:"type,attr,omitempty"`
	}
	type personOut struct {
		Name string `xml:"name"`
	}
	type contentOut struct {
		Type string `xml:"type,attr"`
		Body string `xml:",cdata"`
	}
	type categoryOut struct {
		Term string `xml:"term,attr"`
	}
	type entryOut struct {
		XMLName    xml.Name      `xml:"entry"`
		Title      string        `xml:"title"`
		ID         string        `xml:"id"`
		Links      []linkOut     `xml:"link,omitempty"`
		Updated    string        `xml:"updated"`
		Published  string        `xml:"published,omitempty"`
		Author     *personOut    `xml:"author,omitempty"`
		Categories []categoryOut `xml:"category,omitempty"`
		Content    *contentOut   `xml:"content,omitempty"`
	}
	type feedOut struct {
		XMLName  xml.Name   `xml:"feed"`
		XMLNS    string     `xml:"xmlns,attr"`
		Title    string     `xml:"title"`
		Subtitle string     `xml:"subtitle,omitempty"`
		ID       string     `xml:"id"`
		Updated  string     `xml:"updated"`
		Links    []linkOut  `xml:"link,omitempty"`
		Gen      string     `xml:"generator"`
		Entries  []entryOut `xml:"entry"`
	}

	updated := m.Updated
	if updated.IsZero() {
		updated = time.Now()
	}
	out := feedOut{
		XMLNS:    "http://www.w3.org/2005/Atom",
		Title:    m.Title,
		Subtitle: m.Description,
		ID:       firstNonEmpty(m.SelfLink, m.Link, "urn:feedmerge:merged"),
		Updated:  updated.UTC().Format(time.RFC3339),
		Gen:      Generator,
	}
	if m.Link != "" {
		out.Links = append(out.Links, linkOut{Href: m.Link, Rel: "alternate", Type: "text/html"})
	}
	if m.SelfLink != "" {
		out.Links = append(out.Links, linkOut{Href: m.SelfLink, Rel: "self", Type: "application/atom+xml"})
	}
	for _, e := range entries {
		eo := entryOut{
			Title:   e.Title,
			ID:      e.ID,
			Updated: nonZeroTime(e.Updated, e.Published, updated).UTC().Format(time.RFC3339),
		}
		if !e.Published.IsZero() {
			eo.Published = e.Published.UTC().Format(time.RFC3339)
		}
		if e.Link != "" {
			eo.Links = append(eo.Links, linkOut{Href: e.Link, Rel: "alternate", Type: "text/html"})
		}
		if e.Author != "" {
			eo.Author = &personOut{Name: e.Author}
		}
		for _, c := range e.Categories {
			eo.Categories = append(eo.Categories, categoryOut{Term: c})
		}
		if body := firstNonEmpty(e.Content, e.Summary); body != "" {
			eo.Content = &contentOut{Type: "html", Body: body}
		}
		out.Entries = append(out.Entries, eo)
	}
	return writeXML(w, out)
}

// jsonItem is a JSON Feed 1.1 item.
type jsonItem struct {
	ID            string       `json:"id"`
	URL           string       `json:"url,omitempty"`
	Title         string       `json:"title,omitempty"`
	ContentHTML   string       `json:"content_html,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	DatePublished string       `json:"date_published,omitempty"`
	DateModified  string       `json:"date_modified,omitempty"`
	Authors       []jsonAuthor `json:"authors,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	ExternalURL   string       `json:"external_url,omitempty"`
}

type jsonAuthor struct {
	Name string `json:"name"`
}

type jsonFeed struct {
	Version     string     `json:"version"`
	Title       string     `json:"title"`
	HomePageURL string     `json:"home_page_url,omitempty"`
	FeedURL     string     `json:"feed_url,omitempty"`
	Description string     `json:"description,omitempty"`
	Items       []jsonItem `json:"items"`
}

// WriteJSON writes the entries as a JSON Feed 1.1 document.
func WriteJSON(w io.Writer, m Meta, entries []Entry) error {
	out := jsonFeed{
		Version:     "https://jsonfeed.org/version/1.1",
		Title:       m.Title,
		HomePageURL: m.Link,
		FeedURL:     m.SelfLink,
		Description: m.Description,
		Items:       make([]jsonItem, 0, len(entries)),
	}
	for _, e := range entries {
		it := jsonItem{
			ID:          e.ID,
			URL:         e.Link,
			Title:       e.Title,
			ContentHTML: firstNonEmpty(e.Content, e.Summary),
			Tags:        e.Categories,
		}
		if e.Summary != "" && e.Summary != it.ContentHTML {
			it.Summary = e.Summary
		}
		if !e.Published.IsZero() {
			it.DatePublished = e.Published.UTC().Format(time.RFC3339)
		}
		if !e.Updated.IsZero() {
			it.DateModified = e.Updated.UTC().Format(time.RFC3339)
		}
		if e.Author != "" {
			it.Authors = []jsonAuthor{{Name: e.Author}}
		}
		out.Items = append(out.Items, it)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("feed: encode json: %w", err)
	}
	return nil
}

// Generator identifies this software in emitted documents.
const Generator = "feedmerge"

func writeXML(w io.Writer, v any) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("feed: encode xml: %w", err)
	}
	if err := enc.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func nonZeroTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}
