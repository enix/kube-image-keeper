package filter

// Filter is an interface that defines rules to match images.
type Filter interface {
	// Match reports whether the given image matches this filter.
	Match(s string) bool
}
