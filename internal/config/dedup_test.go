package config

import (
	"strings"
	"testing"
)

func TestContentThresholdReachesDedupOptions(t *testing.T) {
	cfg, err := ParseYAML("content_threshold: 0.85\n" + feedsSection)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if cfg.ContentThreshold != 0.85 {
		t.Errorf("content_threshold = %v, want 0.85", cfg.ContentThreshold)
	}
	if got := cfg.DedupOptions().ContentThreshold; got != 0.85 {
		t.Errorf("DedupOptions().ContentThreshold = %v, want 0.85", got)
	}
}

func TestContentThresholdDefaultsToOff(t *testing.T) {
	cfg, err := ParseYAML(feedsSection)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if cfg.DedupOptions().ContentThreshold != 0 {
		t.Errorf("content matching is on by default: %v", cfg.ContentThreshold)
	}
}

func TestContentThresholdRange(t *testing.T) {
	for _, src := range []string{"content_threshold: 1.5\n", "content_threshold: -0.1\n"} {
		_, err := ParseYAML(src + feedsSection)
		if err == nil {
			t.Fatalf("ParseYAML(%q) succeeded, want a range error", src)
		}
		if !strings.Contains(err.Error(), "between 0 and 1") {
			t.Errorf("error = %v", err)
		}
	}
}

func TestContentThresholdJSON(t *testing.T) {
	cfg, err := ParseJSON([]byte(`{"content_threshold":0.7,"feeds":["https://a.example/feed.xml"]}`))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if cfg.DedupOptions().ContentThreshold != 0.7 {
		t.Errorf("content_threshold = %v", cfg.ContentThreshold)
	}
}
