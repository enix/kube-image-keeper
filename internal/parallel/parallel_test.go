package parallel

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

// ptr is an helper to get pointer of literal strings in tests
func ptr(s string) *string {
	return &s
}

func TestFirstSuccessful(t *testing.T) {
	tests := []struct {
		name     string
		params   []string
		f        func(*string) (*string, bool)
		expected *string
	}{
		{
			name:   "Success on first element",
			params: []string{"A", "B"},
			f: func(p *string) (*string, bool) {
				return p, true
			},
			expected: ptr("A"),
		},
		{
			name:   "First fails, second succeeds",
			params: []string{"FAIL", "SUCCESS"},
			f: func(p *string) (*string, bool) {
				if *p == "FAIL" {
					return nil, false
				}
				return p, true
			},
			expected: ptr("SUCCESS"),
		},
		{
			name:   "First fails after, second succeeds first",
			params: []string{"FAIL", "SUCCESS"},
			f: func(p *string) (*string, bool) {
				if *p == "SUCCESS" {
					return p, true
				}
				time.Sleep(50 * time.Millisecond)
				return nil, false
			},
			expected: ptr("SUCCESS"),
		},
		{
			name:   "Firsts fails after, last succeeds first",
			params: []string{"FAIL1", "FAIL2", "SUCCESS"},
			f: func(p *string) (*string, bool) {
				if *p == "SUCCESS" {
					return p, true
				}
				time.Sleep(50 * time.Millisecond)
				return nil, false
			},
			expected: ptr("SUCCESS"),
		},
		{
			name:   "Ordered priority (slower first element wins)",
			params: []string{"slow", "fast"},
			f: func(p *string) (*string, bool) {
				if *p == "slow" {
					time.Sleep(50 * time.Millisecond)
					res := "slow_result"
					return &res, true
				}
				res := "fast_result"
				return &res, true
			},
			expected: ptr("slow_result"),
		},
		{
			name:   "All elements fail",
			params: []string{"FAIL1", "FAIL2"},
			f: func(p *string) (*string, bool) {
				return nil, false
			},
			expected: nil,
		},
		{
			name:   "Empty params",
			params: []string{},
			f: func(p *string) (*string, bool) {
				return p, true
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := FirstSuccessful(tt.params, tt.f)

			if tt.expected == nil {
				g.Expect(result).To(BeNil())
			} else {
				g.Expect(result).ToNot(BeNil())
				g.Expect(*result).To(Equal(*tt.expected))
			}
		})
	}
}
