package mirrorpath

import (
	"regexp"
	"strings"

	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// Excluded reports whether ref matches one of the excludeImages patterns. A pattern splits
// on its last `:` when that `:` comes after its last `/`: a repository part, then a tag
// part. Each part is matched whole against the same part of ref, `*` inside one path
// segment and `**` across them. A pattern with a tag part never matches a digest-pinned
// ref, which is matched on its repository part alone. See docs/v3/spec.md, "Excluding
// images from a mirror".
func Excluded(patterns []string, ref imagepath.Reference) bool {
	for _, pattern := range patterns {
		if excludes(pattern, ref) {
			return true
		}
	}
	return false
}

func excludes(pattern string, ref imagepath.Reference) bool {
	repository, tag, hasTag := splitPattern(pattern)
	if !glob(repository).MatchString(ref.Repository()) {
		return false
	}
	if !hasTag {
		return true
	}
	return ref.Digest == "" && glob(tag).MatchString(ref.Tag)
}

// splitPattern cuts a pattern into its repository part and its tag part: a `:` before the
// last `/` is a host port.
func splitPattern(pattern string) (repository, tag string, hasTag bool) {
	colon := strings.LastIndex(pattern, ":")
	if colon <= strings.LastIndex(pattern, "/") {
		return pattern, "", false
	}
	return pattern[:colon], pattern[colon+1:], true
}

// glob compiles a pattern part to an anchored regexp: `**` matches any characters, `*` any
// characters but `/`, and everything else itself.
func glob(part string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i, doubles := range strings.Split(part, "**") {
		if i > 0 {
			b.WriteString(".*")
		}
		for j, literal := range strings.Split(doubles, "*") {
			if j > 0 {
				b.WriteString("[^/]*")
			}
			b.WriteString(regexp.QuoteMeta(literal))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
