package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/roxiproject/feedmerge/internal/config"
	"github.com/roxiproject/feedmerge/internal/feed"
	"github.com/roxiproject/feedmerge/internal/search"
	"github.com/roxiproject/feedmerge/internal/server"
)

// cmdSearch merges the configured feeds once and runs a query against the
// result, which is the same index the server exposes at /search.
func cmdSearch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "feeds.yaml", "path to the configuration file (.yaml or .json)")
	limit := fs.Int("limit", 10, "maximum results to show (0 = all)")
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return errors.New("search requires a query")
	}
	if search.ParseQuery(query).Empty() {
		return fmt.Errorf("query %q has no searchable terms", query)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	merger := server.NewMerger(cfg, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout.D()*3+10*time.Second)
	defer cancel()
	snap, err := merger.Refresh(ctx)
	if err != nil {
		return err
	}

	results := snap.Index.Search(query, *limit)
	switch *format {
	case "json":
		return writeSearchJSON(stdout, query, snap.Index.Len(), results)
	case "text":
		printResults(stdout, query, snap.Index.Len(), results)
		return nil
	default:
		return fmt.Errorf("unknown format %q", *format)
	}
}

// searchHitJSON is one hit in the machine-readable output.
type searchHitJSON struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Link      string   `json:"link,omitempty"`
	Source    string   `json:"source,omitempty"`
	Published string   `json:"published,omitempty"`
	Score     float64  `json:"score"`
	Snippet   string   `json:"snippet,omitempty"`
	Matched   []string `json:"matched,omitempty"`
}

// searchJSON is the machine-readable form of a CLI search, deliberately shaped
// like the server's /search body so a script can consume either.
type searchJSON struct {
	Query   string          `json:"query"`
	Indexed int             `json:"indexed"`
	Total   int             `json:"total"`
	Results []searchHitJSON `json:"results"`
}

func writeSearchJSON(w io.Writer, query string, indexed int, results []search.Result) error {
	out := searchJSON{
		Query:   search.ParseQuery(query).String(),
		Indexed: indexed,
		Total:   len(results),
		Results: make([]searchHitJSON, 0, len(results)),
	}
	for _, r := range results {
		hit := searchHitJSON{
			ID: r.Entry.ID, Title: r.Entry.Title, Link: r.Entry.Link,
			Source: r.Entry.SourceTitle, Score: r.Score, Snippet: r.Snippet, Matched: r.Matched,
		}
		if d := r.Entry.Date(); !d.IsZero() {
			hit.Published = d.UTC().Format(time.RFC3339)
		}
		out.Results = append(out.Results, hit)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func printResults(w io.Writer, query string, indexed int, results []search.Result) {
	fmt.Fprintf(w, "query:   %s\n", search.ParseQuery(query).String())
	fmt.Fprintf(w, "indexed: %d entries\n", indexed)
	fmt.Fprintf(w, "matches: %d\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(w, "%3d. [%.3f] %s\n", i+1, r.Score, oneLine(r.Entry.Title))
		fmt.Fprintf(w, "     %s\n", r.Entry.Link)
		fmt.Fprintf(w, "     %s\n", sourceLine(r.Entry))
		if r.Snippet != "" {
			fmt.Fprintf(w, "     %s\n", oneLine(r.Snippet))
		}
		fmt.Fprintf(w, "     matched: %s\n", strings.Join(r.Matched, ", "))
	}
}

// sourceLine describes where a hit came from and when it was published.
func sourceLine(e feed.Entry) string {
	parts := make([]string, 0, 2)
	if e.SourceTitle != "" {
		parts = append(parts, e.SourceTitle)
	}
	if d := e.Date(); !d.IsZero() {
		parts = append(parts, d.Format(time.RFC3339))
	}
	if len(parts) == 0 {
		return "(no source)"
	}
	return strings.Join(parts, "  ")
}
