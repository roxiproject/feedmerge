package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roxiproject/feedmerge/internal/config"
	"github.com/roxiproject/feedmerge/internal/opml"
)

const opmlDoc = `<?xml version="1.0"?>
<opml version="2.0">
  <head><title>Reader export</title></head>
  <body>
    <outline text="Go">
      <outline type="rss" text="The Go Blog" xmlUrl="https://go.dev/blog/feed.atom"/>
    </outline>
    <outline text="Databases">
      <outline type="rss" text="PostgreSQL: news" xmlUrl="https://www.postgresql.org/news.rss"/>
    </outline>
    <outline type="rss" text="Local notes" xmlUrl="file:///home/notes.xml"/>
  </body>
</opml>`

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func TestRunOPMLImportProducesLoadableConfig(t *testing.T) {
	path := writeFile(t, "subs.opml", opmlDoc)
	var out, errOut bytes.Buffer
	if err := run([]string{"opml", "import", "--title", "My merge", path}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "title: My merge") {
		t.Errorf("title missing:\n%s", got)
	}
	if !strings.Contains(got, "# skipped file:///home/notes.xml") {
		t.Errorf("non-http feed was not reported as skipped:\n%s", got)
	}

	cfg, err := config.ParseYAML(got)
	if err != nil {
		t.Fatalf("generated config does not load: %v\n%s", err, got)
	}
	if len(cfg.Feeds) != 2 {
		t.Fatalf("got %d feeds, want 2: %+v", len(cfg.Feeds), cfg.Feeds)
	}
	if cfg.Feeds[0].URL != "https://go.dev/blog/feed.atom" || cfg.Feeds[0].Name != "The Go Blog" {
		t.Errorf("first feed = %+v", cfg.Feeds[0])
	}
	// A name containing a colon has to survive the quoting.
	if cfg.Feeds[1].Name != "PostgreSQL: news" {
		t.Errorf("quoted name = %q", cfg.Feeds[1].Name)
	}
}

func TestRunOPMLImportFolderFilter(t *testing.T) {
	path := writeFile(t, "subs.opml", opmlDoc)
	var out, errOut bytes.Buffer
	if err := run([]string{"opml", "import", "--folder", "Go", path}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	cfg, err := config.ParseYAML(out.String())
	if err != nil {
		t.Fatalf("config does not load: %v\n%s", err, out.String())
	}
	if len(cfg.Feeds) != 1 || cfg.Feeds[0].Name != "The Go Blog" {
		t.Errorf("folder filter kept %+v", cfg.Feeds)
	}
}

func TestRunOPMLExport(t *testing.T) {
	srv := feedServer(t)
	cfgPath := writeConfig(t, srv.URL)
	var out, errOut bytes.Buffer
	if err := run([]string{"opml", "export", "--config", cfgPath}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	subs, err := opml.Parse(&out)
	if err != nil {
		t.Fatalf("exported document does not parse: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(subs))
	}
	if subs[0].Name != "CLI Source" || !strings.HasSuffix(subs[0].URL, "/feed.xml") {
		t.Errorf("exported subscription = %+v", subs[0])
	}
}

func TestRunOPMLRoundTrip(t *testing.T) {
	path := writeFile(t, "subs.opml", opmlDoc)
	var yaml, errOut bytes.Buffer
	if err := run([]string{"opml", "import", path}, &yaml, &errOut); err != nil {
		t.Fatalf("import: %v", err)
	}
	cfgPath := writeFile(t, "feeds.yaml", yaml.String())

	var back bytes.Buffer
	if err := run([]string{"opml", "export", "--config", cfgPath}, &back, &errOut); err != nil {
		t.Fatalf("export: %v", err)
	}
	subs, err := opml.Parse(&back)
	if err != nil {
		t.Fatalf("re-export does not parse: %v", err)
	}
	want := []string{"https://go.dev/blog/feed.atom", "https://www.postgresql.org/news.rss"}
	for i, s := range subs {
		if i >= len(want) || s.URL != want[i] {
			t.Fatalf("round trip produced %+v, want %v", subs, want)
		}
	}
}

func TestRunOPMLErrors(t *testing.T) {
	good := writeFile(t, "subs.opml", opmlDoc)
	notOPML := writeFile(t, "feed.xml", `<rss version="2.0"><channel/></rss>`)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no subcommand", []string{"opml"}, "import or export"},
		{"unknown subcommand", []string{"opml", "merge"}, "unknown opml subcommand"},
		{"import without a file", []string{"opml", "import"}, "exactly one file"},
		{"import a missing file", []string{"opml", "import", "nope.opml"}, "opml:"},
		{"import a feed document", []string{"opml", "import", notOPML}, "not <opml>"},
		{"unknown folder", []string{"opml", "import", "--folder", "Nope", good}, "no feeds under folder"},
		{"export without a config", []string{"opml", "export", "--config", "nope.yaml"}, "config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := run(tt.args, &out, &errOut)
			if err == nil {
				t.Fatalf("run succeeded, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestYAMLScalar(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"", `""`},
		{"has: colon", `"has: colon"`},
		{"hash # here", `"hash # here"`},
		{" padded ", `" padded "`},
		{"quote \" inside", `"quote \" inside"`},
		{"line\nbreak", `"line break"`},
	}
	for _, tt := range tests {
		if got := yamlScalar(tt.in); got != tt.want {
			t.Errorf("yamlScalar(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
