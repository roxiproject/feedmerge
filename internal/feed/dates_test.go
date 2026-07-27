package feed

import (
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // RFC3339 in UTC
	}{
		{"rfc1123z", "Mon, 02 Jan 2006 15:04:05 -0700", "2006-01-02T22:04:05Z"},
		{"rfc1123 gmt", "Mon, 02 Jan 2006 15:04:05 GMT", "2006-01-02T15:04:05Z"},
		{"rfc1123 est", "Mon, 02 Jan 2006 15:04:05 EST", "2006-01-02T20:04:05Z"},
		{"rfc1123 pdt", "Tue, 04 Jul 2017 09:30:00 PDT", "2017-07-04T16:30:00Z"},
		{"rfc1123 cest", "Wed, 01 Jun 2022 12:00:00 CEST", "2022-06-01T10:00:00Z"},
		{"rfc822", "02 Jan 06 15:04 MST", "2006-01-02T22:04:00Z"},
		{"rfc822z", "02 Jan 06 15:04 +0100", "2006-01-02T14:04:00Z"},
		{"rfc3339", "2006-01-02T15:04:05Z", "2006-01-02T15:04:05Z"},
		{"rfc3339 offset", "2006-01-02T15:04:05+02:00", "2006-01-02T13:04:05Z"},
		{"rfc3339 nanos", "2021-11-03T08:15:30.123456Z", "2021-11-03T08:15:30Z"},

		// Malformed but common in the wild.
		{"single digit day", "Mon, 2 Jan 2006 15:04:05 -0700", "2006-01-02T22:04:05Z"},
		{"no weekday", "02 Jan 2006 15:04:05 +0000", "2006-01-02T15:04:05Z"},
		{"no seconds", "Mon, 02 Jan 2006 15:04 +0000", "2006-01-02T15:04:00Z"},
		{"no zone", "Mon, 02 Jan 2006 15:04:05", "2006-01-02T15:04:05Z"},
		{"trailing paren zone", "Mon, 02 Jan 2006 15:04:05 +0000 (UTC)", "2006-01-02T15:04:05Z"},
		{"trailing paren gmt", "Tue, 15 Mar 2022 08:00:00 -0400 (EDT)", "2022-03-15T12:00:00Z"},
		{"ut zone", "Mon, 02 Jan 2006 15:04:05 UT", "2006-01-02T15:04:05Z"},
		{"extra whitespace", "  Mon,  02   Jan 2006 15:04:05  GMT ", "2006-01-02T15:04:05Z"},
		{"space separator", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"},
		{"space separator zone", "2006-01-02 15:04:05 -0500", "2006-01-02T20:04:05Z"},
		{"date only", "2006-01-02", "2006-01-02T00:00:00Z"},
		{"zone without colon", "2006-01-02T15:04:05-0700", "2006-01-02T22:04:05Z"},
		{"floating local", "2006-01-02T15:04:05", "2006-01-02T15:04:05Z"},
		{"us slashes", "01/02/2006 15:04:05", "2006-01-02T15:04:05Z"},
		{"long month", "January 2, 2006", "2006-01-02T00:00:00Z"},
		{"short month", "Jan 2, 2006", "2006-01-02T00:00:00Z"},
		{"ansic", "Mon Jan 2 15:04:05 2006", "2006-01-02T15:04:05Z"},
		{"two digit year with weekday", "Mon, 02 Jan 06 15:04:05 +0000", "2006-01-02T15:04:05Z"},
		{"separate Z token", "2006-01-02T15:04:05 Z", "2006-01-02T15:04:05Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDate(tc.input)
			if err != nil {
				t.Fatalf("ParseDate(%q) returned error: %v", tc.input, err)
			}
			if g := got.UTC().Format(time.RFC3339); g != tc.want {
				t.Fatalf("ParseDate(%q) = %s, want %s", tc.input, g, tc.want)
			}
		})
	}
}

func TestParseDateErrors(t *testing.T) {
	for _, input := range []string{"", "   ", "not a date", "yesterday", "2006-13-45T99:99:99Z"} {
		if got, err := ParseDate(input); err == nil {
			t.Errorf("ParseDate(%q) unexpectedly succeeded with %v", input, got)
		}
	}
}

func TestNormalizeDate(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Mon, 02 Jan 2006 15:04:05 +0000 (UTC)", "Mon, 02 Jan 2006 15:04:05 +0000"},
		{"  a   b  ", "a b"},
		{"Mon, 02 Jan 2006 15:04:05 UT", "Mon, 02 Jan 2006 15:04:05 UTC"},
	}
	for _, tc := range tests {
		if got := normalizeDate(tc.in); got != tc.want {
			t.Errorf("normalizeDate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
