package matchers

import (
	"regexp"
	"slices"

	"github.com/distribution/reference"
)

type IncludeExcludeImageMatcher struct {
	include []regexp.Regexp
	exclude []regexp.Regexp
}

func CompileIncludeExcludeImageMatcher(include, exclude []string) (*IncludeExcludeImageMatcher, error) {
	matcher := &IncludeExcludeImageMatcher{
		include: make([]regexp.Regexp, len(include)),
		exclude: make([]regexp.Regexp, len(exclude)),
	}

	for i := range include {
		r, err := regexp.Compile(include[i])
		if err != nil {
			return nil, err
		}
		matcher.include[i] = *r
	}

	for i := range exclude {
		r, err := regexp.Compile(exclude[i])
		if err != nil {
			return nil, err
		}
		matcher.exclude[i] = *r
	}

	if len(matcher.include) == 0 {
		matcher.include = []regexp.Regexp{*regexp.MustCompile(".*")}
	}

	return matcher, nil
}

func (i *IncludeExcludeImageMatcher) Match(image reference.Named) bool {
	imageStr := image.String()

	included := slices.ContainsFunc(i.include, func(include regexp.Regexp) bool {
		return include.FindString(imageStr) == imageStr // Must be a full match
	})

	if included {
		return !slices.ContainsFunc(i.exclude, func(exclude regexp.Regexp) bool {
			return exclude.FindString(imageStr) == imageStr // Must be a full match
		})
	}

	return false
}
