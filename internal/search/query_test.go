package search

import "testing"

func TestParseQuery(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare words", "go concurrency", "go concurrency"},
		{"required and excluded", "+go -rust", "+go -rust"},
		{"phrase", `"structured logging"`, `"structured logging"`},
		{"negated phrase", `go -"release candidate"`, `go -"release candidate"`},
		{"unbalanced quote", `"structured logging`, `"structured logging"`},
		{"mixed", `postgres "write ahead log" -mysql`, `postgres -mysql "write ahead log"`},
		{"stop words only", "the of and", ""},
		{"empty", "   ", ""},
		{"lone operators", "+ - +\"\"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseQuery(tt.in).String(); got != tt.want {
				t.Errorf("ParseQuery(%q).String() = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
