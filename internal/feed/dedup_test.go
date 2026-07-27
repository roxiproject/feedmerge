package feed

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://example.com/post", "https://example.com/post"},
		{"http://example.com/post", "https://example.com/post"},
		{"https://WWW.Example.COM/Post/", "https://example.com/Post"},
		{"https://example.com/post#section", "https://example.com/post"},
		{"https://example.com/post?utm_source=rss&id=7", "https://example.com/post?id=7"},
		{"https://example.com/post?b=2&a=1", "https://example.com/post?a=1&b=2"},
		{"https://example.com:443/post", "https://example.com/post"},
		{"https://example.com/", "https://example.com"},
		{"", ""},
		{"not a url", "not a url"},
	}
	for _, tc := range tests {
		if got := NormalizeURL(tc.in); got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeTitleAndSimilarity(t *testing.T) {
	if got := NormalizeTitle("  <b>Go 1.22</b> Is  Released! "); got != "go 1 22 is released" {
		t.Errorf("NormalizeTitle = %q", got)
	}
	tests := []struct {
		a, b string
		min  float64
		max  float64
	}{
		{"Go 1.22 is released", "Go 1.22 Is Released!", 0.99, 1.01},
		{"Go 1.22 is released", "go 1 22 is released", 0.99, 1.01},
		{"Go 1.22 is released", "Rust 1.75 is released", 0.0, 0.5},
		{"", "anything", -0.01, 0.01},
		{"a b c d", "", -0.01, 0.01},
	}
	for _, tc := range tests {
		got := TitleSimilarity(tc.a, tc.b)
		if got < tc.min || got > tc.max {
			t.Errorf("TitleSimilarity(%q, %q) = %v, want in [%v, %v]", tc.a, tc.b, got, tc.min, tc.max)
		}
	}
}

func mkEntry(id, link, title string, day int) Entry {
	e := Entry{ID: id, Link: link, Title: title, IDSource: "guid"}
	if id == "" {
		e.IDSource = "link"
		e.ID = link
	}
	if day > 0 {
		e.Published = time.Date(2024, 3, day, 12, 0, 0, 0, time.UTC)
		e.Updated = e.Published
	}
	return e
}

func TestDedup(t *testing.T) {
	tests := []struct {
		name  string
		in    []Entry
		opts  DedupOptions
		want  int
		check func(t *testing.T, out []Entry)
	}{
		{
			name: "by guid",
			in: []Entry{
				mkEntry("guid-1", "https://a.example/1", "First", 1),
				mkEntry("guid-1", "https://b.example/mirror", "First (mirror)", 1),
			},
			opts: DedupOptions{},
			want: 1,
		},
		{
			name: "by normalized url",
			in: []Entry{
				mkEntry("guid-a", "https://a.example/post", "Post", 1),
				mkEntry("guid-b", "http://www.a.example/post/?utm_source=rss", "Post elsewhere", 1),
			},
			opts: DedupOptions{},
			want: 1,
		},
		{
			name: "by title similarity",
			in: []Entry{
				mkEntry("guid-a", "https://a.example/go-122", "Go 1.22 is released", 1),
				mkEntry("guid-b", "https://b.example/go122", "Go 1.22 Is Released!", 1),
			},
			opts: DefaultDedupOptions(),
			want: 1,
		},
		{
			name: "title matching disabled",
			in: []Entry{
				mkEntry("guid-a", "https://a.example/go-122", "Go 1.22 is released", 1),
				mkEntry("guid-b", "https://b.example/go122", "Go 1.22 Is Released!", 1),
			},
			opts: DedupOptions{TitleThreshold: 0},
			want: 2,
		},
		{
			name: "title match outside the time window",
			in: []Entry{
				mkEntry("guid-a", "https://a.example/go-122", "Go 1.22 is released", 1),
				mkEntry("guid-b", "https://b.example/go122", "Go 1.22 Is Released!", 20),
			},
			opts: DedupOptions{TitleThreshold: 0.9, TitleWindow: 24 * 3600},
			want: 2,
		},
		{
			name: "distinct entries survive",
			in: []Entry{
				mkEntry("guid-a", "https://a.example/1", "Alpha release notes", 1),
				mkEntry("guid-b", "https://b.example/2", "Beta postmortem writeup", 2),
				mkEntry("guid-c", "https://c.example/3", "Gamma incident review", 3),
			},
			opts: DefaultDedupOptions(),
			want: 3,
		},
		{
			name: "transitive via url then guid",
			in: []Entry{
				mkEntry("guid-a", "https://a.example/post", "Post", 1),
				mkEntry("guid-b", "https://a.example/post", "Post again", 1),
				mkEntry("guid-b", "https://c.example/other", "Yet another copy", 1),
			},
			opts: DedupOptions{},
			want: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := Dedup(tc.in, tc.opts)
			if len(out) != tc.want {
				var got []string
				for _, e := range out {
					got = append(got, e.Title)
				}
				t.Fatalf("Dedup produced %d entries (%s), want %d", len(out), strings.Join(got, " | "), tc.want)
			}
			if tc.check != nil {
				tc.check(t, out)
			}
		})
	}
}

func TestDedupKeepsRicherFields(t *testing.T) {
	a := Entry{ID: "x", IDSource: "guid", Title: "Title"}
	b := Entry{
		ID: "x", IDSource: "guid", Title: "Title", Link: "https://a.example/x",
		Content: "body", Author: "Ada", Categories: []string{"go"},
		Published: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	out := Dedup([]Entry{a, b}, DedupOptions{})
	if len(out) != 1 {
		t.Fatalf("got %d entries", len(out))
	}
	got := out[0]
	if got.Link != b.Link || got.Content != "body" || got.Author != "Ada" ||
		got.Published != b.Published || len(got.Categories) != 1 {
		t.Errorf("merged entry lost data: %+v", got)
	}
}

func TestDedupPrefersRealIDOverHash(t *testing.T) {
	a := Entry{ID: "urn:feedmerge:abc", IDSource: "hash", Title: "Same story", Link: "https://a.example/s"}
	b := Entry{ID: "guid-real", IDSource: "guid", Title: "Same story", Link: "https://a.example/s"}
	out := Dedup([]Entry{a, b}, DedupOptions{})
	if len(out) != 1 || out[0].ID != "guid-real" {
		t.Fatalf("got %+v", out)
	}
}

func TestSortByDate(t *testing.T) {
	in := []Entry{
		mkEntry("a", "https://x/1", "old", 1),
		{ID: "n", Title: "no date"},
		mkEntry("b", "https://x/2", "new", 9),
		mkEntry("c", "https://x/3", "middle", 5),
	}
	SortByDate(in)
	want := []string{"new", "middle", "old", "no date"}
	for i, w := range want {
		if in[i].Title != w {
			t.Fatalf("position %d = %q, want %q (order: %+v)", i, in[i].Title, w, in)
		}
	}
}
