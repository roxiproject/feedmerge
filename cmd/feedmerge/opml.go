package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/roxiproject/feedmerge/internal/config"
	"github.com/roxiproject/feedmerge/internal/opml"
)

const opmlUsage = `feedmerge opml import <file.opml>   convert a subscription list into a config
feedmerge opml export --config feeds.yaml   write the configured feeds as OPML
`

// cmdOPML moves subscriptions in and out of feedmerge: import turns a reader's
// export into a config file, export turns a config back into OPML.
func cmdOPML(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, opmlUsage)
		return errors.New("opml requires import or export")
	}
	switch args[0] {
	case "import":
		return cmdOPMLImport(args[1:], stdout, stderr)
	case "export":
		return cmdOPMLExport(args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, opmlUsage)
		return fmt.Errorf("unknown opml subcommand %q", args[0])
	}
}

func cmdOPMLImport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("opml import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	title := fs.String("title", "", "title for the generated merged feed")
	folder := fs.String("folder", "", "import only feeds under this OPML folder")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("opml import requires exactly one file (use - for stdin)")
	}

	var (
		r   io.Reader = os.Stdin
		err error
	)
	if path := fs.Arg(0); path != "-" {
		f, ferr := os.Open(path)
		if ferr != nil {
			return fmt.Errorf("opml: %w", ferr)
		}
		defer f.Close()
		r = f
	}
	subs, err := opml.Parse(r)
	if err != nil {
		return err
	}
	if *folder != "" {
		subs = filterFolder(subs, *folder)
		if len(subs) == 0 {
			return fmt.Errorf("opml: no feeds under folder %q", *folder)
		}
	}
	writeConfigYAML(stdout, *title, subs)
	return nil
}

// filterFolder keeps the feeds in a folder and in any folder beneath it.
func filterFolder(subs []opml.Subscription, folder string) []opml.Subscription {
	out := make([]opml.Subscription, 0, len(subs))
	for _, s := range subs {
		if s.Folder == folder || strings.HasPrefix(s.Folder, folder+"/") {
			out = append(out, s)
		}
	}
	return out
}

// writeConfigYAML emits a config file for the imported subscriptions. Only
// http(s) feeds are written, since anything else would be rejected on load.
func writeConfigYAML(w io.Writer, title string, subs []opml.Subscription) {
	if title != "" {
		fmt.Fprintf(w, "title: %s\n", yamlScalar(title))
	}
	fmt.Fprint(w, "feeds:\n")
	for _, s := range subs {
		if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			fmt.Fprintf(w, "  # skipped %s: not an http(s) url\n", s.URL)
			continue
		}
		fmt.Fprintf(w, "  - url: %s\n", yamlScalar(s.URL))
		if s.Name != "" {
			fmt.Fprintf(w, "    name: %s\n", yamlScalar(s.Name))
		}
	}
}

// yamlScalar quotes a value when the loader would otherwise misread it.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\"'\n\t") || strings.TrimSpace(s) != s {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\t", " ").Replace(s) + `"`
	}
	return s
}

func cmdOPMLExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("opml export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "feeds.yaml", "path to the configuration file (.yaml or .json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	return opml.Write(stdout, cfg.Title, time.Now(), configSubscriptions(cfg))
}

// configSubscriptions converts configured sources into OPML subscriptions.
func configSubscriptions(cfg *config.Config) []opml.Subscription {
	subs := make([]opml.Subscription, 0, len(cfg.Feeds))
	for _, f := range cfg.Feeds {
		subs = append(subs, opml.Subscription{Name: f.Name, URL: f.URL})
	}
	return subs
}
