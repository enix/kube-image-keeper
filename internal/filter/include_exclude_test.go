package filter

import (
	"testing"
)

func TestIncludeExcludeFilter_Match(t *testing.T) {
	tests := []struct {
		name        string
		include     []string
		exclude     []string
		input       string
		shouldMatch bool
	}{
		{
			name:        "full match with alternation covering entire string",
			include:     []string{"a|ab"},
			input:       "ab",
			shouldMatch: true,
		},
		{
			name:        "full match with alternation short branch",
			include:     []string{"a|ab"},
			input:       "a",
			shouldMatch: true,
		},
		{
			name:        "no match with alternation when input exceeds all branches",
			include:     []string{"a|ab"},
			input:       "abc",
			shouldMatch: false,
		},
		{
			name:        "typical image pattern full match",
			include:     []string{`docker\.io/library/.*`},
			input:       "docker.io/library/nginx:latest",
			shouldMatch: true,
		},
		{
			name:        "typical image pattern no match on different registry",
			include:     []string{`docker\.io/library/.*`},
			input:       "ghcr.io/library/nginx:latest",
			shouldMatch: false,
		},
		{
			name:        "reject partial match at end",
			include:     []string{"nginx"},
			input:       "nginx-extra",
			shouldMatch: false,
		},
		{
			name:        "reject partial match at start",
			include:     []string{"nginx"},
			input:       "my-nginx",
			shouldMatch: false,
		},
		{
			name:        "exact match",
			include:     []string{"nginx"},
			input:       "nginx",
			shouldMatch: true,
		},
		{
			name:        "exclude takes precedence over include",
			include:     []string{".*"},
			exclude:     []string{"nginx"},
			input:       "nginx",
			shouldMatch: false,
		},
		{
			name:        "exclude does not affect non-matching input",
			include:     []string{".*"},
			exclude:     []string{"nginx"},
			input:       "redis",
			shouldMatch: true,
		},
		{
			name:        "user-provided anchors are not doubled",
			include:     []string{"^nginx$"},
			input:       "nginx",
			shouldMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := CompileIncludeExcludeFilter(tt.include, tt.exclude)
			if err != nil {
				t.Fatalf("CompileIncludeExcludeFilter() error = %v", err)
			}
			if got := filter.Match(tt.input); got != tt.shouldMatch {
				t.Errorf("Match(%q) = %v, want %v", tt.input, got, tt.shouldMatch)
			}
		})
	}
}
