package feed

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// dateLayouts is tried in order. It covers the formats mandated by the RSS and
// Atom specifications plus the malformed variants that are common in the wild:
// single digit days, missing seconds, missing time zones, space instead of the
// 'T' separator, two digit years, and so on.
var dateLayouts = []string{
	time.RFC1123Z,                    // Mon, 02 Jan 2006 15:04:05 -0700
	time.RFC1123,                     // Mon, 02 Jan 2006 15:04:05 MST
	"Mon, 2 Jan 2006 15:04:05 -0700", // single digit day
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04 -0700", // no seconds
	"Mon, 02 Jan 2006 15:04 MST",
	"Mon, 2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 2006 15:04 MST",
	"Mon, 02 Jan 2006 15:04:05", // no zone at all
	"Mon, 2 Jan 2006 15:04:05",
	"02 Jan 2006 15:04:05 -0700", // missing weekday
	"02 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	"02 Jan 2006 15:04 -0700",
	"2 Jan 2006 15:04 MST",
	time.RFC822Z, // 02 Jan 06 15:04 -0700
	time.RFC822,  // 02 Jan 06 15:04 MST
	"Mon, 02 Jan 06 15:04:05 -0700",
	"Mon, 02 Jan 06 15:04:05 MST",
	time.RFC3339Nano,                     // 2006-01-02T15:04:05.999999999Z07:00
	time.RFC3339,                         // 2006-01-02T15:04:05Z07:00
	"2006-01-02T15:04:05.999999999-0700", // zone without colon
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05",    // floating local time
	"2006-01-02T15:04Z07:00", // no seconds
	"2006-01-02T15:04",
	"2006-01-02 15:04:05 -0700", // space separator
	"2006-01-02 15:04:05 MST",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"01/02/2006 15:04:05",
	"01/02/2006",
	"January 2, 2006 15:04:05 MST",
	"January 2, 2006",
	"Jan 2, 2006",
	"Mon Jan 2 15:04:05 2006", // ANSIC
	"Mon Jan 02 15:04:05 -0700 2006",
}

// zoneOffsets maps the time zone abbreviations that appear in feeds to their
// UTC offsets in seconds. Go's time.Parse accepts an abbreviation but yields a
// zero offset for zones it does not know, so we correct the result here.
var zoneOffsets = map[string]int{
	"UT": 0, "UTC": 0, "GMT": 0, "Z": 0,
	"EST": -5 * 3600, "EDT": -4 * 3600,
	"CST": -6 * 3600, "CDT": -5 * 3600,
	"MST": -7 * 3600, "MDT": -6 * 3600,
	"PST": -8 * 3600, "PDT": -7 * 3600,
	"AKST": -9 * 3600, "AKDT": -8 * 3600,
	"HST": -10 * 3600,
	"BST": 1 * 3600, "IST": 1 * 3600, "WET": 0, "WEST": 1 * 3600,
	"CET": 1 * 3600, "CEST": 2 * 3600,
	"EET": 2 * 3600, "EEST": 3 * 3600,
	"MSK": 3 * 3600,
	"JST": 9 * 3600, "KST": 9 * 3600,
	"AEST": 10 * 3600, "AEDT": 11 * 3600,
	"NZST": 12 * 3600, "NZDT": 13 * 3600,
}

var (
	parenSuffix  = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	multiSpace   = regexp.MustCompile(`\s+`)
	trailingZone = regexp.MustCompile(`(?i)\s([A-Z]{2,4})$`)
)

// normalizeDate cleans up the whitespace and trailing annotations that feeds
// frequently attach to date strings, e.g.
// "Mon, 02 Jan 2006 15:04:05 +0000 (UTC)".
func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	s = parenSuffix.ReplaceAllString(s, "")
	s = multiSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	// "UT" is a legal RFC822 zone but Go does not accept it.
	if strings.HasSuffix(s, " UT") {
		s = strings.TrimSuffix(s, " UT") + " UTC"
	}
	// Some feeds emit "Z" as a separate token instead of a suffix.
	if strings.HasSuffix(s, " Z") {
		s = strings.TrimSuffix(s, " Z") + "Z"
	}
	return s
}

// ParseDate decodes a feed date string. It returns an error only when no known
// layout matches; callers generally keep the raw string in that case.
func ParseDate(s string) (time.Time, error) {
	s = normalizeDate(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	for _, layout := range dateLayouts {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		return fixZone(t, s), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized date format: %q", s)
}

// fixZone repairs timestamps parsed with a named zone that Go resolved to a
// zero offset because the abbreviation is not in the local zone database.
func fixZone(t time.Time, s string) time.Time {
	name, offset := t.Zone()
	if offset != 0 || name == "UTC" || name == "" {
		return t.UTC()
	}
	m := trailingZone.FindStringSubmatch(s)
	if m == nil {
		return t.UTC()
	}
	abbr := strings.ToUpper(m[1])
	if abbr != strings.ToUpper(name) {
		return t.UTC()
	}
	want, ok := zoneOffsets[abbr]
	if !ok {
		return t.UTC()
	}
	return t.Add(-time.Duration(want) * time.Second).UTC()
}
