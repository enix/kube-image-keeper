package matchers

import (
	"fmt"
	"regexp"

	"github.com/distribution/reference"
)

type RegexpImageMatcher struct {
	r *regexp.Regexp
}

func NewRegexpImageMatcher(pattern string) (*RegexpImageMatcher, error) {
	r, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("could not build matcher: %w", err)
	}

	return &RegexpImageMatcher{r: r}, nil
}

func (r *RegexpImageMatcher) Match(image reference.Named) bool {
	return r.r.MatchString(image.String())
}
