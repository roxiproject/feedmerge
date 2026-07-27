package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const feedDoc = `<?xml version="1.0"?>
<rss version="2.0"><channel>
  <title>CLI Source</title><link>https://cli.example/</link><description>d</description>
  <item><title>Story one</title><link>/one</link><guid>urn:one</guid>
    <pubDate>Wed, 01 May 2024 10:00:00 GMT</pubDate><description>Body</description></item>
  <item><title>Story two</title><link>https://cli.example/two</link>
    <pubDate>bad date</pubDate></item>
</channel></rss>`

func feedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, feedDoc)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunFetchSummary(t *testing.T) {
	srv := feedServer(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"fetch", srv.URL + "/feed.xml"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	for _, want := range []string{"CLI Source", "entries: 2", "Story one", "urn:one", "(guid)", "unparsed: bad date"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary is missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "https://cli.example/one") {
		t.Errorf("relative link was not resolved:\n%s", s)
	}
}

func TestRunFetchFormats(t *testing.T) {
	srv := feedServer(t)
	tests := []struct{ format, contains string }{
		{"rss", `<rss version="2.0"`},
		{"atom", `xmlns="http://www.w3.org/2005/Atom"`},
		{"json", `"version": "https://jsonfeed.org/version/1.1"`},
	}
	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := run([]string{"fetch", "--format", tc.format, srv.URL + "/f"}, &out, &errOut); err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.Contains(out.String(), tc.contains) {
				t.Errorf("output does not contain %q:\n%s", tc.contains, out.String())
			}
		})
	}
}

func TestRunFetchLimit(t *testing.T) {
	srv := feedServer(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"fetch", "--limit", "1", srv.URL + "/f"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "Story two") {
		t.Errorf("--limit was ignored:\n%s", out.String())
	}
}

func TestRunErrors(t *testing.T) {
	srv := feedServer(t)
	tests := []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"unknown command", []string{"frobnicate"}},
		{"fetch without url", []string{"fetch"}},
		{"fetch with two urls", []string{"fetch", "https://a.example/f", "https://b.example/f"}},
		{"fetch with a non-http url", []string{"fetch", "file:///etc/passwd"}},
		{"fetch a failing feed", []string{"fetch", srv.URL + "/broken"}},
		{"unknown format", []string{"fetch", "--format", "yaml", srv.URL + "/f"}},
		{"missing config", []string{"serve", "--config", "does-not-exist.yaml"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := run(tc.args, &out, &errOut); err == nil {
				t.Fatalf("expected an error for %v", tc.args)
			}
		})
	}
}

func TestRunVersionAndHelp(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"help"}, {"-h"}} {
		var out, errOut bytes.Buffer
		if err := run(args, &out, &errOut); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if out.Len() == 0 {
			t.Errorf("run(%v) printed nothing", args)
		}
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("  a\n b\tc "); got != "a b c" {
		t.Errorf("oneLine = %q", got)
	}
	long := strings.Repeat("x", 200)
	if got := oneLine(long); len(got) != 100 || !strings.HasSuffix(got, "...") {
		t.Errorf("oneLine did not truncate: %d chars", len(got))
	}
}
