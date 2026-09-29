package routing

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Candidates", func() {
	Context("selecting the resources", func() {
		PIt("applies a resource with neither podSelector nor namespaceSelector to every pod", func() {})
		PIt("skips a resource whose podSelector does not match the labels of the pod", func() {})
		PIt("skips a resource whose namespaceSelector does not match the labels of the pod's namespace", func() {})
		PIt("offers nothing but the original when no resource applies", func() {})
	})

	Context("matching an ImageAlternative", func() {
		PIt("offers the other entries of a repository list with the tag of the image", func() {})
		PIt("offers the other entries of a repositoryGroup list with the remainder below the group", func() {})
		PIt("matches the image once normalised", func() {})
		PIt("carries the remainder of the most specific entry when several entries of one resource match", func() {})
		PIt("does not apply a resource none of whose entries matches the image", func() {})
		PIt("merges the entries of overlapping resources instead of letting one shadow the other", func() {})
	})

	Context("ordering", func() {
		// docs/v3/spec.md, "Candidate ordering": mirror M, alternatives ecr, gcr and docker.io,
		// the pod's original being docker.io.
		DescribeTable("merges the bands of both kinds around the original",
			func(mirrorPolicy, alternativePolicy string, want []string) {},
			PEntry("OnFailure mirror, OnFailure alternative", onFailure, onFailure, []string{dockerIO, ecr, gcr, mirror}),
			PEntry("OnFailure mirror, Always alternative", onFailure, always, []string{ecr, gcr, dockerIO, mirror}),
			PEntry("Always mirror, OnFailure alternative", always, onFailure, []string{mirror, dockerIO, ecr, gcr}),
			PEntry("Always mirror, Always alternative", always, always, []string{mirror, ecr, gcr, dockerIO}),
		)
		PIt("puts the original once, at the pivot, whatever its position in alternatives", func() {})
		PIt("keeps the declared order of a resource's entries within its band", func() {})
		PIt("sorts the resources of one kind and one policy by name", func() {})
	})

	Context("with an entry marked unavailable", func() {
		PIt("applies a resource whose only matching entry is marked unavailable", func() {})
		PIt("offers no candidate for an entry marked unavailable", func() {})
		PIt("demotes the original to the end of the list when it matches an entry marked unavailable", func() {})
	})

	Context("with an ImageMirror", func() {
		PIt("offers one candidate, the destination reference of the original", func() {})
		PIt("offers none under rewritePolicy None", func() {})
		PIt("offers none for an image matching excludeImages", func() {})
		PIt("offers none for an image already under its own destination.path", func() {})
	})

	Context("for a container with imagePullPolicy Always", func() {
		PIt("moves an Always ImageMirror candidate to the OnFailure mirror band", func() {})
		PIt("leaves an Always ImageAlternative in its band", func() {})
		PIt("keeps an Always ImageMirror in its band when demoteMirrorWithPullPolicyAlways is false", func() {})
	})

	Context("the config each candidate is probed with", func() {
		PIt("carries the auth and insecure of the alternative entry that produced it", func() {})
		PIt("carries destination.pull and destination.insecure for a mirror candidate", func() {})
		// The kubelet pulls the original with no entry credential: the pod keeps no record, so
		// nothing is injected for it.
		PIt("carries no entry auth for the original", func() {})
	})

	Context("deduplication", func() {
		PIt("keeps the first occurrence of a reference offered twice with the same config", func() {})
		PIt("keeps both occurrences of a reference offered with different configs", func() {})
	})

	Context("attribution", func() {
		PIt("attributes each candidate to the resource that offered it and to the policy of its band", func() {})
		// Not in the spec: the policy recorded for a mirror demoted by imagePullPolicy Always.
		PIt("attributes a demoted Always ImageMirror candidate the OnFailure policy", func() {})
		PIt("lists every resource that offered a candidate", func() {})
	})
})

// Fixtures of the ordering table: the rewrite policies and the candidates, named as the
// spec's table names them.
const (
	always    = "Always"
	onFailure = "OnFailure"
	dockerIO  = "docker.io"
	ecr       = "ecr"
	gcr       = "gcr"
	mirror    = "M"
)
