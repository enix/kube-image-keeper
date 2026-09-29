// Package mirrorpath derives what an ImageMirror does with an origin reference: the
// destination reference it serves the image at, the tags it pushes, and whether the image
// is excluded from it. See docs/v3/spec.md, "Excluding images from a mirror" and "Mirror
// loop prevention", and walkthrough 02, "A.6 Compute the destination reference".
package mirrorpath

import "github.com/enix/kube-image-keeper/internal/imagepath"

// Destination renders the reference an ImageMirror whose destination.path is path serves
// origin at, the tag carrying clusterID.
func Destination(path string, origin imagepath.Reference, clusterID string) string {
	return ""
}

// Tags lists the tags a copy of origin pushes at the destination.
func Tags(origin imagepath.Reference, clusterID string) []string {
	return nil
}

// Excluded reports whether ref matches one of the excludeImages patterns.
func Excluded(patterns []string, ref imagepath.Reference) bool {
	return false
}

// UnderDestination reports whether ref lives under destination.path.
func UnderDestination(path string, ref imagepath.Reference) bool {
	return false
}
