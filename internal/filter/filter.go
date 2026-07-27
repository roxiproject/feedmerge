// Package filter implements the feedmerge filter DSL: a small, line oriented
// language for deciding which merged entries are published.
//
// Grammar (one rule per line, blank lines and '#' comments ignored):
//
//	rule   := action field op pattern
//	action := "include" | "exclude"
//	field  := "title" | "content" | "summary" | "link" | "author" |
//	          "category" | "source" | "any"
//	op     := "~" (regexp matches) | "!~" (regexp does not match)
//
// The pattern is the rest of the line, a Go regular expression. It may be
// wrapped in slashes, optionally with an "i" flag: /golang/i is equivalent to
// (?i)golang.
//
// Evaluation: an entry is rejected if any exclude rule matches it. If at least
// one include rule is present, the entry must additionally satisfy at least one
// include rule. A rule set with no include rules therefore acts as a blocklist.
package filter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/roxiproject/feedmerge/internal/feed"
)

// Field identifies the part of an entry a rule inspects.
type Field string

// Supported fields.
const (
	FieldTitle    Field = "title"
	FieldContent  Field = "content"
	FieldSummary  Field = "summary"
	FieldLink     Field = "link"
	FieldAuthor   Field = "author"
	FieldCategory Field = "category"
	FieldSource   Field = "source"
	FieldAny      Field = "any"
)

var validFields = map[Field]bool{
	FieldTitle: true, FieldContent: true, FieldSummary: true, FieldLink: true,
	FieldAuthor: true, FieldCategory: true, FieldSource: true, FieldAny: true,
}

// Rule is a single parsed filter rule.
type Rule struct {
	Include bool
	Field   Field
	Negate  bool // true for "!~"
	Re      *regexp.Regexp
	Source  string // the original line, for error messages and debugging
}

// Matches reports whether the rule's condition holds for the entry.
func (r Rule) Matches(e feed.Entry) bool {
	got := r.Re.MatchString(fieldValue(e, r.Field))
	if r.Negate {
		return !got
	}
	return got
}

func fieldValue(e feed.Entry, f Field) string {
	switch f {
	case FieldTitle:
		return e.Title
	case FieldContent:
		return e.Content
	case FieldSummary:
		return e.Summary
	case FieldLink:
		return e.Link
	case FieldAuthor:
		return e.Author
	case FieldCategory:
		return strings.Join(e.Categories, " ")
	case FieldSource:
		return e.SourceTitle + " " + e.SourceURL
	case FieldAny:
		return strings.Join([]string{
			e.Title, e.Summary, e.Content, e.Link, e.Author,
			strings.Join(e.Categories, " "), e.SourceTitle,
		}, "\n")
	default:
		return ""
	}
}

// Set is a compiled collection of rules.
type Set struct {
	rules       []Rule
	hasIncludes bool
}

// Rules returns the compiled rules in source order.
func (s *Set) Rules() []Rule {
	if s == nil {
		return nil
	}
	return s.rules
}

// Len reports the number of rules in the set.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.rules)
}

// Allow reports whether the entry passes the rule set. A nil or empty set
// allows everything.
func (s *Set) Allow(e feed.Entry) bool {
	if s == nil || len(s.rules) == 0 {
		return true
	}
	included := !s.hasIncludes
	for _, r := range s.rules {
		if !r.Matches(e) {
			continue
		}
		if r.Include {
			included = true
		} else {
			return false
		}
	}
	return included
}

// Apply returns the subset of entries the rule set allows.
func (s *Set) Apply(entries []feed.Entry) []feed.Entry {
	if s == nil || len(s.rules) == 0 {
		return entries
	}
	out := make([]feed.Entry, 0, len(entries))
	for _, e := range entries {
		if s.Allow(e) {
			out = append(out, e)
		}
	}
	return out
}

// Parse compiles a rule set from its textual form. Every syntax error is
// reported with the offending line number.
func Parse(src string) (*Set, error) {
	set := &Set{}
	for i, line := range strings.Split(src, "\n") {
		lineNo := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, err := ParseRule(line)
		if err != nil {
			return nil, fmt.Errorf("filter: line %d: %w", lineNo, err)
		}
		if r.Include {
			set.hasIncludes = true
		}
		set.rules = append(set.rules, r)
	}
	return set, nil
}

// ParseLines compiles a rule set from individual rule strings.
func ParseLines(lines []string) (*Set, error) {
	return Parse(strings.Join(lines, "\n"))
}

// ParseRule compiles a single rule.
func ParseRule(line string) (Rule, error) {
	var r Rule
	r.Source = line
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return r, fmt.Errorf("expected \"<include|exclude> <field> <~|!~> <pattern>\", got %q", line)
	}
	switch strings.ToLower(fields[0]) {
	case "include":
		r.Include = true
	case "exclude":
		r.Include = false
	default:
		return r, fmt.Errorf("unknown action %q (want include or exclude)", fields[0])
	}

	r.Field = Field(strings.ToLower(fields[1]))
	if !validFields[r.Field] {
		return r, fmt.Errorf("unknown field %q", fields[1])
	}

	switch fields[2] {
	case "~", "=~":
		r.Negate = false
	case "!~":
		r.Negate = true
	default:
		return r, fmt.Errorf("unknown operator %q (want ~ or !~)", fields[2])
	}

	// The pattern is everything after the operator, preserving inner spacing.
	idx := strings.Index(line, fields[2])
	pattern := strings.TrimSpace(line[idx+len(fields[2]):])
	pattern, err := unwrapPattern(pattern)
	if err != nil {
		return r, err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return r, fmt.Errorf("invalid regexp: %w", err)
	}
	r.Re = re
	return r, nil
}

// unwrapPattern turns /re/flags into a Go regexp source string. Only the "i"
// (case insensitive) and "s" (dot matches newline) flags are accepted.
func unwrapPattern(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty pattern")
	}
	if !strings.HasPrefix(p, "/") {
		return p, nil
	}
	end := strings.LastIndex(p, "/")
	if end == 0 {
		return "", fmt.Errorf("unterminated pattern %q", p)
	}
	body := p[1:end]
	flags := p[end+1:]
	if body == "" {
		return "", fmt.Errorf("empty pattern")
	}
	var goFlags string
	for _, f := range flags {
		switch f {
		case 'i', 's':
			if !strings.ContainsRune(goFlags, f) {
				goFlags += string(f)
			}
		default:
			return "", fmt.Errorf("unknown pattern flag %q", string(f))
		}
	}
	if goFlags != "" {
		return "(?" + goFlags + ")" + body, nil
	}
	return body, nil
}
