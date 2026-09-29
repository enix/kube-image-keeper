// Package mirrorpath derives what an ImageMirror does with an origin reference: the
// destination reference it serves the image at, the tags it pushes, and whether the image
// is excluded from it. See docs/v3/spec.md, "Excluding images from a mirror" and "Mirror
// loop prevention", and walkthrough 02, "A.6 Compute the destination reference".
package mirrorpath

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"

	"github.com/enix/kube-image-keeper/internal/imagepath"
)

const (
	// maxTagLength is the OCI limit on a tag.
	maxTagLength = 128
	// hashLength is the number of base32 characters of the hash appended to a truncated tag.
	hashLength = 10
)

// Destination renders the reference an ImageMirror whose destination.path is path serves
// origin at: path verbatim, a slash added if it lacks one, then the full origin, its host
// lowercased and its port separator turned into `_`. The tag carries `_<clusterID>`, and a
// digest is kept as is.
func Destination(path string, origin imagepath.Reference, clusterID string) string {
	var b strings.Builder
	b.WriteString(withSlash(path))
	b.WriteString(strings.Replace(origin.Host, ":", "_", 1))
	b.WriteString("/" + strings.Join(origin.Segments, "/"))
	if origin.Tag != "" {
		b.WriteString(":" + suffixedTag(origin.Tag, clusterID))
	}
	if origin.Digest != "" {
		b.WriteString("@" + origin.Digest)
	}
	return b.String()
}

// Tags lists the tags a copy of origin pushes at the destination: the origin-derived tag
// when origin carries one, and the anchor `sha256-<digest>_<clusterID>` when it is pinned.
// See docs/v3/spec.md, "The anchor tag".
func Tags(origin imagepath.Reference, clusterID string) []string {
	var tags []string
	if origin.Tag != "" {
		tags = append(tags, suffixedTag(origin.Tag, clusterID))
	}
	if origin.Digest != "" {
		tags = append(tags, strings.Replace(origin.Digest, ":", "-", 1)+"_"+clusterID)
	}
	return tags
}

// suffixedTag appends `_<clusterID>` to tag. A result past 128 characters is truncated
// and a hash of the whole tag is appended before the suffix: `<truncated>-<hash>_<clusterID>`,
// the hash being the first ten characters of its sha256 in lowercase base32. See
// docs/v3/spec.md, "Tag naming constraints".
func suffixedTag(tag, clusterID string) string {
	suffix := "_" + clusterID
	if len(tag)+len(suffix) <= maxTagLength {
		return tag + suffix
	}
	sum := sha256.Sum256([]byte(tag))
	hash := strings.ToLower(base32.StdEncoding.EncodeToString(sum[:]))[:hashLength]
	return tag[:maxTagLength-len(suffix)-1-hashLength] + "-" + hash + suffix
}

// UnderDestination reports whether ref lives under destination.path, compared at path
// segment granularity and with the host of path in lowercase.
func UnderDestination(path string, ref imagepath.Reference) bool {
	host, rest, _ := strings.Cut(withSlash(path), "/")
	return strings.HasPrefix(ref.Repository()+"/", strings.ToLower(host)+"/"+rest)
}

// withSlash returns path with a trailing slash.
func withSlash(path string) string {
	if strings.HasSuffix(path, "/") {
		return path
	}
	return path + "/"
}
