// Package config loads feedmerge configuration from a small YAML subset (or
// from JSON, which the same loader accepts).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/roxiproject/feedmerge/internal/feed"
	"github.com/roxiproject/feedmerge/internal/filter"
)

// Source is one upstream feed.
type Source struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

// Config is the fully validated configuration.
type Config struct {
	Title       string   `json:"title"`
	Link        string   `json:"link"`
	Description string   `json:"description"`
	SelfLink    string   `json:"self_link"`
	Addr        string   `json:"addr"`
	Refresh     Duration `json:"refresh"`
	Timeout     Duration `json:"timeout"`
	Workers     int      `json:"workers"`
	// HostInterval is the minimum delay between two requests to the same host.
	HostInterval Duration `json:"host_interval"`
	MaxItems     int      `json:"max_items"`
	UserAgent    string   `json:"user_agent"`

	TitleThreshold float64  `json:"title_threshold"`
	TitleWindow    Duration `json:"title_window"`

	Filters []string `json:"filters"`
	Feeds   []Source `json:"feeds"`

	// FilterSet is the compiled form of Filters.
	FilterSet *filter.Set `json:"-"`
}

// Duration is a time.Duration that also accepts a bare number of seconds.
type Duration time.Duration

// UnmarshalJSON accepts "10m" or 600.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := parseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("invalid duration %s", string(b))
	}
	*d = Duration(time.Duration(n * float64(time.Second)))
	return nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if v, err := time.ParseDuration(s); err == nil {
		return v, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	return 0, fmt.Errorf("invalid duration %q", s)
}

// Defaults returns a configuration with every optional field populated.
func Defaults() Config {
	dd := feed.DefaultDedupOptions()
	return Config{
		Title:          "feedmerge",
		Description:    "Merged feed",
		Addr:           ":8080",
		Refresh:        Duration(15 * time.Minute),
		Timeout:        Duration(20 * time.Second),
		Workers:        8,
		HostInterval:   Duration(time.Second),
		MaxItems:       200,
		TitleThreshold: dd.TitleThreshold,
		TitleWindow:    Duration(time.Duration(dd.TitleWindow) * time.Second),
	}
}

// DedupOptions converts the config into feed dedup settings.
func (c Config) DedupOptions() feed.DedupOptions {
	return feed.DedupOptions{
		TitleThreshold: c.TitleThreshold,
		TitleWindow:    int64(c.TitleWindow.D() / time.Second),
	}
}

// Load reads and validates a config file. Files ending in .json are parsed as
// JSON, everything else as the YAML subset.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		return ParseJSON(data)
	}
	return ParseYAML(string(data))
}

// ParseJSON loads a JSON configuration.
func ParseJSON(data []byte) (*Config, error) {
	c := Defaults()
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: json: %w", err)
	}
	return finish(&c)
}

// ParseYAML loads a configuration written in the supported YAML subset.
func ParseYAML(src string) (*Config, error) {
	root, err := parseYAML(src)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c := Defaults()

	known := map[string]bool{}
	str := func(key string, dst *string) error {
		known[key] = true
		n := root.child(key)
		if n == nil {
			return nil
		}
		if n.kind != kindScalar {
			return fmt.Errorf("config: line %d: %q must be a scalar", n.line, key)
		}
		*dst = n.scalar
		return nil
	}
	dur := func(key string, dst *Duration) error {
		known[key] = true
		n := root.child(key)
		if n == nil || n.scalar == "" {
			return nil
		}
		if n.kind != kindScalar {
			return fmt.Errorf("config: line %d: %q must be a scalar", n.line, key)
		}
		v, err := parseDuration(n.scalar)
		if err != nil {
			return fmt.Errorf("config: line %d: %q: %w", n.line, key, err)
		}
		*dst = Duration(v)
		return nil
	}
	num := func(key string, dst *int) error {
		known[key] = true
		n := root.child(key)
		if n == nil || n.scalar == "" {
			return nil
		}
		v, err := strconv.Atoi(n.scalar)
		if err != nil {
			return fmt.Errorf("config: line %d: %q must be an integer", n.line, key)
		}
		*dst = v
		return nil
	}
	flt := func(key string, dst *float64) error {
		known[key] = true
		n := root.child(key)
		if n == nil || n.scalar == "" {
			return nil
		}
		v, err := strconv.ParseFloat(n.scalar, 64)
		if err != nil {
			return fmt.Errorf("config: line %d: %q must be a number", n.line, key)
		}
		*dst = v
		return nil
	}

	for _, err := range []error{
		str("title", &c.Title),
		str("link", &c.Link),
		str("description", &c.Description),
		str("self_link", &c.SelfLink),
		str("addr", &c.Addr),
		str("user_agent", &c.UserAgent),
		dur("refresh", &c.Refresh),
		dur("timeout", &c.Timeout),
		dur("host_interval", &c.HostInterval),
		dur("title_window", &c.TitleWindow),
		num("workers", &c.Workers),
		num("max_items", &c.MaxItems),
		flt("title_threshold", &c.TitleThreshold),
	} {
		if err != nil {
			return nil, err
		}
	}

	known["filters"] = true
	if n := root.child("filters"); n != nil && n.kind == kindSeq {
		for _, it := range n.items {
			if it.kind != kindScalar {
				return nil, fmt.Errorf("config: line %d: filter rules must be strings", it.line)
			}
			c.Filters = append(c.Filters, it.scalar)
		}
	} else if n != nil && n.scalar != "" {
		return nil, fmt.Errorf("config: line %d: \"filters\" must be a list", n.line)
	}

	known["feeds"] = true
	n := root.child("feeds")
	if n == nil {
		return nil, fmt.Errorf("config: no \"feeds\" section")
	}
	if n.kind != kindSeq {
		return nil, fmt.Errorf("config: line %d: \"feeds\" must be a list", n.line)
	}
	for _, it := range n.items {
		switch it.kind {
		case kindScalar:
			c.Feeds = append(c.Feeds, Source{URL: it.scalar})
		case kindMap:
			var s Source
			for _, k := range it.keys {
				v := it.fields[k]
				if v.kind != kindScalar {
					return nil, fmt.Errorf("config: line %d: feed %q must be a scalar", v.line, k)
				}
				switch k {
				case "url":
					s.URL = v.scalar
				case "name":
					s.Name = v.scalar
				default:
					return nil, fmt.Errorf("config: line %d: unknown feed key %q", v.line, k)
				}
			}
			c.Feeds = append(c.Feeds, s)
		default:
			return nil, fmt.Errorf("config: line %d: unsupported feed entry", it.line)
		}
	}

	for _, k := range root.keys {
		if !known[k] {
			return nil, fmt.Errorf("config: line %d: unknown key %q", root.fields[k].line, k)
		}
	}
	return finish(&c)
}

// finish validates and compiles a decoded config.
func finish(c *Config) (*Config, error) {
	if len(c.Feeds) == 0 {
		return nil, fmt.Errorf("config: at least one feed is required")
	}
	seen := map[string]bool{}
	for i, s := range c.Feeds {
		u := strings.TrimSpace(s.URL)
		if u == "" {
			return nil, fmt.Errorf("config: feed %d has no url", i+1)
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return nil, fmt.Errorf("config: feed %q: url must start with http:// or https://", u)
		}
		if seen[u] {
			return nil, fmt.Errorf("config: duplicate feed url %q", u)
		}
		seen[u] = true
		c.Feeds[i].URL = u
	}
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.MaxItems < 0 {
		c.MaxItems = 0
	}
	if c.TitleThreshold < 0 || c.TitleThreshold > 1 {
		return nil, fmt.Errorf("config: title_threshold must be between 0 and 1")
	}
	if c.Refresh.D() < 0 || c.Timeout.D() < 0 || c.HostInterval.D() < 0 {
		return nil, fmt.Errorf("config: durations must not be negative")
	}
	if c.Timeout.D() == 0 {
		c.Timeout = Duration(20 * time.Second)
	}
	set, err := filter.ParseLines(c.Filters)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c.FilterSet = set
	return c, nil
}
