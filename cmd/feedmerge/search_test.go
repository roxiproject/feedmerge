package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roxiproject/feedmerge/internal/feed"
)

// writeConfig writes a config pointing at srv and returns its path.
func writeConfig(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "feeds.yaml")
	src := fmt.Sprintf("title: CLI Merge\nrefresh: 0\nfeeds:\n  - url: %s/feed.xml\n    name: CLI Source\n", url)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestRunSearchText(t *testing.T) {
	srv := feedServer(t)
	cfg := writeConfig(t, srv.URL)

	var out, errOut bytes.Buffer
	if err := run([]string{"search", "--config", cfg, "story", "one"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	for _, want := range []string{"indexed: 2 entries", "Story one", "https://cli.example/one", "CLI Source", "matched:"} {
		if !strings.Contains(s, want) {
			t.Errorf("output is missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "matches: 2") {
		t.Errorf("both stories should match \"story\":\n%s", s)
	}
	// "Story one" carries the extra term, so it has to rank first.
	if strings.Index(s, "Story one") > strings.Index(s, "Story two") {
		t.Errorf("ranking put the weaker match first:\n%s", s)
	}
}

func TestRunSearchJSON(t *testing.T) {
	srv := feedServer(t)
	cfg := writeConfig(t, srv.URL)

	var out, errOut bytes.Buffer
	if err := run([]string{"search", "--config", cfg, "--format", "json", "-limit", "1", "story", "one"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got searchJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding output: %v\n%s", err, out.String())
	}
	if got.Query != "story one" {
		t.Errorf("query = %q, want %q", got.Query, "story one")
	}
	if got.Indexed != 2 {
		t.Errorf("indexed = %d, want 2", got.Indexed)
	}
	if len(got.Results) != 1 {
		t.Fatalf("got %d results under -limit 1", len(got.Results))
	}
	hit := got.Results[0]
	if hit.Title != "Story one" || hit.ID != "urn:one" {
		t.Errorf("top hit = %+v", hit)
	}
	if hit.Score <= 0 {
		t.Errorf("score = %v, want > 0", hit.Score)
	}
	if hit.Published != "2024-05-01T10:00:00Z" {
		t.Errorf("published = %q", hit.Published)
	}
}

func TestRunSearchExcludedTerm(t *testing.T) {
	srv := feedServer(t)
	cfg := writeConfig(t, srv.URL)

	var out, errOut bytes.Buffer
	if err := run([]string{"search", "--config", cfg, "--format", "json", "story", "-two"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got searchJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding output: %v", err)
	}
	if got.Total != 1 || got.Results[0].Title != "Story one" {
		t.Errorf("exclusion did not apply: %+v", got.Results)
	}
}

func TestRunSearchErrors(t *testing.T) {
	srv := feedServer(t)
	cfg := writeConfig(t, srv.URL)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no query", []string{"search", "--config", cfg}, "requires a query"},
		{"stop words only", []string{"search", "--config", cfg, "the", "and"}, "no searchable terms"},
		{"missing config", []string{"search", "--config", "nope.yaml", "go"}, "config"},
		{"unknown format", []string{"search", "--config", cfg, "--format", "xml", "go"}, "unknown format"},
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

func TestSourceLine(t *testing.T) {
	when := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		entry feed.Entry
		want  string
	}{
		{"source and date", feed.Entry{SourceTitle: "CLI Source", Published: when}, "CLI Source  2024-05-01T10:00:00Z"},
		{"date only", feed.Entry{Published: when}, "2024-05-01T10:00:00Z"},
		{"source only", feed.Entry{SourceTitle: "CLI Source"}, "CLI Source"},
		{"neither", feed.Entry{}, "(no source)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceLine(tt.entry); got != tt.want {
				t.Errorf("sourceLine = %q, want %q", got, tt.want)
			}
		})
	}
}
