// Command feedmerge fetches, merges and serves RSS/Atom feeds.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/roxiproject/feedmerge/internal/config"
	"github.com/roxiproject/feedmerge/internal/feed"
	"github.com/roxiproject/feedmerge/internal/fetch"
	"github.com/roxiproject/feedmerge/internal/server"
)

const usage = `feedmerge - merge RSS and Atom feeds into one normalized feed

Usage:
  feedmerge serve --config feeds.yaml [--addr :8080]
  feedmerge fetch <url> [--format summary|rss|atom|json]
  feedmerge version

Run "feedmerge <command> -h" for the flags of a command.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "feedmerge:", err)
		os.Exit(1)
	}
}

// version is the released version of the binary.
const version = "1.0.0"

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("no command given")
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:], stderr)
	case "fetch":
		return cmdFetch(args[1:], stdout)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "feedmerge %s\n", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func cmdServe(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "feeds.yaml", "path to the configuration file (.yaml or .json)")
	addr := fs.String("addr", "", "listen address, overrides the config file")
	refresh := fs.Duration("refresh", 0, "refresh interval, overrides the config file")
	quiet := fs.Bool("quiet", false, "suppress log output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	if *refresh > 0 {
		cfg.Refresh = config.Duration(*refresh)
	}

	logger := log.New(stderr, "", log.LstdFlags)
	if *quiet {
		logger = nil
	}

	f := fetch.New(cfg.Timeout.D(), cfg.HostInterval.D())
	if cfg.UserAgent != "" {
		f.UserAgent = cfg.UserAgent
	}
	merger := server.NewMerger(cfg, f, logger)
	srv := server.NewServer(merger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go merger.Run(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if logger != nil {
			logger.Printf("listening on %s, merging %d feeds every %s", cfg.Addr, len(cfg.Feeds), cfg.Refresh.D())
		}
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		if logger != nil {
			logger.Print("shutting down")
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}

func cmdFetch(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(stdout)
	format := fs.String("format", "summary", "output format: summary, rss, atom or json")
	timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
	limit := fs.Int("limit", 0, "maximum entries to show (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("fetch requires exactly one feed URL")
	}
	url := fs.Arg(0)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("url must start with http:// or https://: %q", url)
	}

	f := fetch.New(*timeout, 0)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout+5*time.Second)
	defer cancel()

	parsed, _, status, err := f.Fetch(ctx, url)
	if err != nil {
		return err
	}
	entries := parsed.Entries
	if *limit > 0 && len(entries) > *limit {
		entries = entries[:*limit]
	}
	meta := feed.Meta{
		Title:       parsed.Title,
		Link:        parsed.Link,
		Description: parsed.Description,
		SelfLink:    url,
		Updated:     parsed.Updated,
	}
	switch *format {
	case "rss":
		return feed.WriteRSS(stdout, meta, entries)
	case "atom":
		return feed.WriteAtom(stdout, meta, entries)
	case "json":
		return feed.WriteJSON(stdout, meta, entries)
	case "summary":
		printSummary(stdout, parsed, entries, status)
		return nil
	default:
		return fmt.Errorf("unknown format %q", *format)
	}
}

func printSummary(w io.Writer, f *feed.Feed, entries []feed.Entry, status int) {
	fmt.Fprintf(w, "format:  %s (HTTP %d)\n", f.Format, status)
	fmt.Fprintf(w, "title:   %s\n", f.Title)
	fmt.Fprintf(w, "link:    %s\n", f.Link)
	if !f.Updated.IsZero() {
		fmt.Fprintf(w, "updated: %s\n", f.Updated.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "entries: %d\n\n", len(f.Entries))
	for i, e := range entries {
		fmt.Fprintf(w, "%3d. %s\n", i+1, oneLine(e.Title))
		fmt.Fprintf(w, "     %s\n", e.Link)
		date := "(no date)"
		if d := e.Date(); !d.IsZero() {
			date = d.Format(time.RFC3339)
		} else if e.PublishedRaw != "" {
			date = "unparsed: " + e.PublishedRaw
		}
		fmt.Fprintf(w, "     %s  id=%s (%s)\n", date, oneLine(e.ID), e.IDSource)
	}
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 100 {
		return s[:97] + "..."
	}
	return s
}
