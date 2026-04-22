package parallel

import (
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

const successValue = "SUCCESS"

// ptr is an helper to get pointer of literal strings in tests
func ptr(s string) *string {
	return &s
}

func TestFirstSuccessful(t *testing.T) {
	tests := []struct {
		name         string
		params       []string
		f            func(*string) (*string, error)
		expected     *string
		expectedErrs []error
	}{
		{
			name:   "Success on first element",
			params: []string{"A", "B"},
			f: func(p *string) (*string, error) {
				return p, nil
			},
			expected: ptr("A"),
		},
		{
			name:   "First fails, second succeeds",
			params: []string{"FAIL", successValue},
			f: func(p *string) (*string, error) {
				if *p == "FAIL" {
					return nil, errors.New(*p)
				}
				return p, nil
			},
			expected:     ptr(successValue),
			expectedErrs: []error{errors.New("FAIL")},
		},
		{
			name:   "First fails after, second succeeds first",
			params: []string{"FAIL", successValue},
			f: func(p *string) (*string, error) {
				if *p == successValue {
					return p, nil
				}
				time.Sleep(50 * time.Millisecond)
				return nil, errors.New(*p)
			},
			expected:     ptr(successValue),
			expectedErrs: []error{errors.New("FAIL")},
		},
		{
			name:   "Firsts fails after, last succeeds first",
			params: []string{"FAIL1", "FAIL2", successValue},
			f: func(p *string) (*string, error) {
				if *p == successValue {
					return p, nil
				}
				time.Sleep(50 * time.Millisecond)
				return nil, errors.New(*p)
			},
			expected:     ptr(successValue),
			expectedErrs: []error{errors.New("FAIL1"), errors.New("FAIL2")},
		},
		{
			name:   "Ordered priority (slower first element wins)",
			params: []string{"slow", "fast"},
			f: func(p *string) (*string, error) {
				if *p == "slow" {
					time.Sleep(50 * time.Millisecond)
					res := "slow_result"
					return &res, nil
				}
				res := "fast_result"
				return &res, nil
			},
			expected: ptr("slow_result"),
		},
		{
			name:   "All elements fail",
			params: []string{"FAIL1", "FAIL2"},
			f: func(p *string) (*string, error) {
				return nil, errors.New(*p)
			},
			expected:     nil,
			expectedErrs: []error{errors.New("FAIL1"), errors.New("FAIL2")},
		},
		{
			name:   "Only fails before first success are returned",
			params: []string{"FAIL1", "FAIL2", successValue, "FAIL3"},
			f: func(p *string) (*string, error) {
				if *p == successValue {
					return p, nil
				}
				return nil, errors.New(*p)
			},
			expected:     ptr(successValue),
			expectedErrs: []error{errors.New("FAIL1"), errors.New("FAIL2")},
		},
		{
			name:   "Empty params",
			params: []string{},
			f: func(p *string) (*string, error) {
				return p, nil
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result, errs := FirstSuccessful(tt.params, tt.f)

			if tt.expected == nil {
				g.Expect(result).To(BeNil())
			} else {
				g.Expect(result).ToNot(BeNil())
				g.Expect(*result).To(Equal(*tt.expected))
			}

			if tt.expectedErrs == nil {
				tt.expectedErrs = []error{}
			}
			g.Expect(errs).To(Equal(tt.expectedErrs))
		})
	}
}
