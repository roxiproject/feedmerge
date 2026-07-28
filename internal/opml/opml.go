// Package opml reads and writes OPML 2.0 subscription lists, the format every
// feed reader uses to move subscriptions between applications.
//
// Only the parts of OPML that describe feeds are modelled: the head title and
// date, and the outline tree with its type/text/title/xmlUrl/htmlUrl
// attributes. Outlines nest, and a reader commonly uses one level of nesting
// for folders, so the tree is flattened to a list of subscriptions that
// remember the folder path they came from.
package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// Outline is one node of an OPML outline tree.
type Outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr,omitempty"`
	Type     string    `xml:"type,attr,omitempty"`
	XMLURL   string    `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string    `xml:"htmlUrl,attr,omitempty"`
	Category string    `xml:"category,attr,omitempty"`
	Children []Outline `xml:"outline"`
}

// Document is a parsed OPML file.
type Document struct {
	XMLName  xml.Name  `xml:"opml"`
	Version  string    `xml:"version,attr"`
	Title    string    `xml:"head>title,omitempty"`
	Created  string    `xml:"head>dateCreated,omitempty"`
	Outlines []Outline `xml:"body>outline"`
}

// Subscription is one feed found in an OPML document.
type Subscription struct {
	// Name is the human-readable title, taken from title or text.
	Name string
	// URL is the feed itself (xmlUrl).
	URL string
	// SiteURL is the feed's home page (htmlUrl), when the document gives one.
	SiteURL string
	// Folder is the outline path the feed was nested under, joined by "/".
	// It is empty for a feed at the top level.
	Folder string
}

// Parse reads an OPML document. A document whose root is not <opml>, or which
// contains no feed outline at all, is an error: silently importing nothing is
// worse than saying so.
func Parse(r io.Reader) ([]Subscription, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	// The root element is checked before decoding so that being handed an RSS
	// file by mistake produces a plain explanation rather than an XML error.
	root, err := rootElement(dec)
	if err != nil {
		return nil, err
	}
	if root.Name.Local != "opml" {
		return nil, fmt.Errorf("opml: root element is <%s>, not <opml>", root.Name.Local)
	}
	var doc Document
	if err := dec.DecodeElement(&doc, &root); err != nil {
		return nil, fmt.Errorf("opml: %w", err)
	}
	var subs []Subscription
	seen := make(map[string]bool)
	for _, o := range doc.Outlines {
		collect(o, "", &subs, seen)
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("opml: document contains no feeds")
	}
	return subs, nil
}

// rootElement advances the decoder to the document's first element.
func rootElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return xml.StartElement{}, fmt.Errorf("opml: document is empty")
			}
			return xml.StartElement{}, fmt.Errorf("opml: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se, nil
		}
	}
}

// ParseString is Parse over a string.
func ParseString(s string) ([]Subscription, error) { return Parse(strings.NewReader(s)) }

// collect walks one outline, appending every node that carries an xmlUrl.
// Duplicate feed URLs are dropped, keeping the first occurrence.
func collect(o Outline, folder string, out *[]Subscription, seen map[string]bool) {
	label := firstNonEmpty(o.Title, o.Text)
	url := strings.TrimSpace(o.XMLURL)
	if url != "" && !seen[url] {
		seen[url] = true
		*out = append(*out, Subscription{
			Name:    label,
			URL:     url,
			SiteURL: strings.TrimSpace(o.HTMLURL),
			Folder:  folder,
		})
	}
	if len(o.Children) == 0 {
		return
	}
	// A node with children and no feed of its own is a folder.
	child := folder
	if url == "" && label != "" {
		if child == "" {
			child = label
		} else {
			child += "/" + label
		}
	}
	for _, c := range o.Children {
		collect(c, child, out, seen)
	}
}

// Write renders subscriptions as an OPML 2.0 document. Feeds that name a
// folder are nested under an outline for it, in first-seen folder order, so a
// round trip through Parse and Write preserves the grouping.
func Write(w io.Writer, title string, now time.Time, subs []Subscription) error {
	doc := Document{
		XMLName: xml.Name{Local: "opml"},
		Version: "2.0",
		Title:   title,
	}
	if !now.IsZero() {
		doc.Created = now.UTC().Format(time.RFC1123Z)
	}

	folders := make(map[string]int)
	for _, s := range subs {
		node := Outline{Type: "rss", Text: s.Name, Title: s.Name, XMLURL: s.URL, HTMLURL: s.SiteURL}
		if node.Text == "" {
			node.Text, node.Title = s.URL, s.URL
		}
		if s.Folder == "" {
			doc.Outlines = append(doc.Outlines, node)
			continue
		}
		i, ok := folders[s.Folder]
		if !ok {
			doc.Outlines = append(doc.Outlines, Outline{Text: s.Folder, Title: s.Folder})
			i = len(doc.Outlines) - 1
			folders[s.Folder] = i
		}
		doc.Outlines[i].Children = append(doc.Outlines[i].Children, node)
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("opml: %w", err)
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
