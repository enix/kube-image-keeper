// Package ref holds the grammar of registry host labels, repository names and tags.
package ref

import (
	"regexp"
	"strings"
)

var (
	// labelRe: 63 bytes is the DNS label limit (RFC 1035, section 2.3.4).
	labelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	// tagRe is the OCI distribution spec tag grammar, at most 128 bytes.
	tagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	// repoSegmentRe is the OCI distribution path component.
	repoSegmentRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
)

const (
	// maxRepoLen is the repository name length most clients enforce (OCI
	// distribution spec, note on "name").
	maxRepoLen = 255
	// maxRepoSegments is this tester's own bound on ** matching depth, not an
	// OCI limit.
	maxRepoSegments = 32
)

// ValidLabel reports a host label: lower case, at most 63 bytes.
func ValidLabel(s string) bool { return labelRe.MatchString(s) }

// ValidTag reports a tag of at most 128 bytes.
func ValidTag(s string) bool { return tagRe.MatchString(s) }

// ValidRepo reports a repository name of at most 255 bytes and 32 segments.
func ValidRepo(s string) bool {
	if len(s) > maxRepoLen {
		return false
	}
	segs := strings.Split(s, "/")
	if len(segs) > maxRepoSegments {
		return false
	}
	for _, seg := range segs {
		if !repoSegmentRe.MatchString(seg) {
			return false
		}
	}
	return true
}
