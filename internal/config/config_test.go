package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleYAML = `# a comment
title: My Merged Feed
link: https://example.org/
description: "Two feeds, one river"
addr: ":9090"
refresh: 5m
timeout: 3s
workers: 4
host_interval: 250ms
max_items: 50
title_threshold: 0.8
title_window: 24h

filters:
  - exclude title ~ /sponsored/i
  - include any ~ /go/

feeds:
  - url: https://a.example/feed.xml
    name: Feed A
  - url: https://b.example/atom.xml
  - https://c.example/rss
`

func TestParseYAML(t *testing.T) {
	c, err := ParseYAML(sampleYAML)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if c.Title != "My Merged Feed" || c.Link != "https://example.org/" {
		t.Errorf("title/link = %q / %q", c.Title, c.Link)
	}
	if c.Description != "Two feeds, one river" {
		t.Errorf("description = %q", c.Description)
	}
	if c.Addr != ":9090" {
		t.Errorf("addr = %q", c.Addr)
	}
	if c.Refresh.D() != 5*time.Minute || c.Timeout.D() != 3*time.Second ||
		c.HostInterval.D() != 250*time.Millisecond || c.TitleWindow.D() != 24*time.Hour {
		t.Errorf("durations = %v %v %v %v", c.Refresh.D(), c.Timeout.D(), c.HostInterval.D(), c.TitleWindow.D())
	}
	if c.Workers != 4 || c.MaxItems != 50 {
		t.Errorf("workers = %d, max_items = %d", c.Workers, c.MaxItems)
	}
	if c.TitleThreshold != 0.8 {
		t.Errorf("title_threshold = %v", c.TitleThreshold)
	}
	if len(c.Filters) != 2 || c.FilterSet.Len() != 2 {
		t.Errorf("filters = %v (compiled %d)", c.Filters, c.FilterSet.Len())
	}
	if len(c.Feeds) != 3 {
		t.Fatalf("got %d feeds", len(c.Feeds))
	}
	if c.Feeds[0].URL != "https://a.example/feed.xml" || c.Feeds[0].Name != "Feed A" {
		t.Errorf("feed 0 = %+v", c.Feeds[0])
	}
	if c.Feeds[1].URL != "https://b.example/atom.xml" || c.Feeds[1].Name != "" {
		t.Errorf("feed 1 = %+v", c.Feeds[1])
	}
	if c.Feeds[2].URL != "https://c.example/rss" {
		t.Errorf("bare string feed = %+v", c.Feeds[2])
	}
	if opts := c.DedupOptions(); opts.TitleThreshold != 0.8 || opts.TitleWindow != 86400 {
		t.Errorf("DedupOptions = %+v", opts)
	}
}

func TestParseYAMLDefaults(t *testing.T) {
	c, err := ParseYAML("feeds:\n  - https://only.example/feed\n")
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	d := Defaults()
	if c.Addr != d.Addr || c.Refresh != d.Refresh || c.Workers != d.Workers ||
		c.MaxItems != d.MaxItems || c.TitleThreshold != d.TitleThreshold {
		t.Errorf("defaults were not applied: %+v", c)
	}
}

func TestParseYAMLErrors(t *testing.T) {
	tests := []struct{ name, src string }{
		{"no feeds", "title: x\n"},
		{"empty feed list", "feeds:\n"},
		{"unknown key", "nope: 1\nfeeds:\n  - https://a.example/f\n"},
		{"unknown feed key", "feeds:\n  - url: https://a.example/f\n    colour: red\n"},
		{"bad url scheme", "feeds:\n  - ftp://a.example/f\n"},
		{"duplicate url", "feeds:\n  - https://a.example/f\n  - https://a.example/f\n"},
		{"duplicate key", "title: a\ntitle: b\nfeeds:\n  - https://a.example/f\n"},
		{"bad duration", "refresh: soon\nfeeds:\n  - https://a.example/f\n"},
		{"bad integer", "workers: many\nfeeds:\n  - https://a.example/f\n"},
		{"bad float", "title_threshold: high\nfeeds:\n  - https://a.example/f\n"},
		{"threshold out of range", "title_threshold: 2\nfeeds:\n  - https://a.example/f\n"},
		{"bad filter", "filters:\n  - nonsense\nfeeds:\n  - https://a.example/f\n"},
		{"tab indentation", "feeds:\n\t- https://a.example/f\n"},
		{"feeds not a list", "feeds: https://a.example/f\n"},
		{"not a mapping", "- just\n- a list\n"},
		{"garbage line", "feeds:\n  - https://a.example/f\nthis line has no colon\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if c, err := ParseYAML(tc.src); err == nil {
				t.Fatalf("expected an error, got %+v", c)
			}
		})
	}
}

func TestParseJSON(t *testing.T) {
	src := `{
		"title": "JSON config",
		"refresh": "2m",
		"timeout": 5,
		"workers": 2,
		"filters": ["exclude title ~ /x/"],
		"feeds": [{"url": "https://a.example/feed", "name": "A"}]
	}`
	c, err := ParseJSON([]byte(src))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if c.Title != "JSON config" || c.Refresh.D() != 2*time.Minute || c.Timeout.D() != 5*time.Second {
		t.Errorf("config = %+v", c)
	}
	if len(c.Feeds) != 1 || c.Feeds[0].Name != "A" || c.FilterSet.Len() != 1 {
		t.Errorf("feeds/filters = %+v / %d", c.Feeds, c.FilterSet.Len())
	}

	if _, err := ParseJSON([]byte(`{"feeds": [], "bogus": 1}`)); err == nil {
		t.Error("expected an error for an unknown JSON field")
	}
	if _, err := ParseJSON([]byte(`{"refresh": "eventually", "feeds": []}`)); err == nil {
		t.Error("expected an error for a bad duration")
	}
	if _, err := ParseJSON([]byte(`not json`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "feeds.yaml")
	if err := os.WriteFile(yamlPath, []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(yamlPath); err != nil {
		t.Fatalf("Load yaml: %v", err)
	}

	jsonPath := filepath.Join(dir, "feeds.json")
	if err := os.WriteFile(jsonPath, []byte(`{"feeds":["https://a.example/f"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(jsonPath)
	if err != nil {
		t.Fatalf("Load json: %v", err)
	}
	if len(c.Feeds) != 1 {
		t.Errorf("feeds = %+v", c.Feeds)
	}

	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("expected an error for a missing file")
	}
}

// The example shipped with the project must stay loadable.
func TestExampleConfigLoads(t *testing.T) {
	path := filepath.Join("..", "..", "feeds.example.yaml")
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	if len(c.Feeds) == 0 || c.FilterSet.Len() == 0 {
		t.Errorf("example config looks empty: %+v", c)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"90s", 90 * time.Second, false},
		{"1h30m", 90 * time.Minute, false},
		{"30", 30 * time.Second, false},
		{"0.5", 500 * time.Millisecond, false},
		{"soon", 0, true},
	}
	for _, tc := range tests {
		got, err := parseDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q) = %v, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q): %v", tc.in, err)
		} else if got != tc.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestYAMLScalarHandling(t *testing.T) {
	src := `title: Trailing comment # not part of the title
description: 'single quoted: with colon'
link: https://example.org/path#anchor
feeds:
  - url: "https://a.example/feed?a=1&b=2"
    name: "Quoted \"name\""
`
	c, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if c.Title != "Trailing comment" {
		t.Errorf("title = %q", c.Title)
	}
	if c.Description != "single quoted: with colon" {
		t.Errorf("description = %q", c.Description)
	}
	if c.Link != "https://example.org/path#anchor" {
		t.Errorf("link = %q, a '#' inside a URL must survive", c.Link)
	}
	if c.Feeds[0].URL != "https://a.example/feed?a=1&b=2" {
		t.Errorf("url = %q", c.Feeds[0].URL)
	}
	if c.Feeds[0].Name != `Quoted "name"` {
		t.Errorf("name = %q", c.Feeds[0].Name)
	}
}

func TestYAMLNestedMappingItem(t *testing.T) {
	src := strings.Join([]string{
		"feeds:",
		"  -",
		"    url: https://a.example/feed",
		"    name: A",
	}, "\n")
	c, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if len(c.Feeds) != 1 || c.Feeds[0].Name != "A" {
		t.Errorf("feeds = %+v", c.Feeds)
	}
}
