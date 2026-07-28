package search

import (
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"simple words", "Hello World", []string{"hello", "world"}},
		{"punctuation splits", "go-routines, channels!", []string{"go", "routines", "channels"}},
		{"stop words dropped", "the state of the art", []string{"state", "art"}},
		{"digits kept", "go1.22 released", []string{"go1", "22", "released"}},
		{"unicode letters", "Café Ürün", []string{"café", "ürün"}},
		{"empty", "", nil},
		{"only punctuation", "--- ... ???", nil},
		{"only stop words", "the and of", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.in)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("Tokenize(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestTokenizeIsIdempotentOverItsOwnOutput(t *testing.T) {
	in := "Structured Logging, with slog (go1.22)!"
	once := Tokenize(in)
	twice := Tokenize(strings.Join(once, " "))
	if strings.Join(once, "|") != strings.Join(twice, "|") {
		t.Errorf("re-tokenizing changed the terms: %v then %v", once, twice)
	}
}
