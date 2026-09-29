package mirrorpath

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/internal/imagepath"
)

var _ = Describe("Destination", func() {
	DescribeTable("renders the reference an ImageMirror serves an origin at",
		func(path, origin, want string) {
			Expect(Destination(path, parse(origin), clusterID)).To(Equal(want))
		},
		Entry("joins destination.path and the full origin, hostname included, and suffixes the tag with the clusterID",
			"registry.tld/mirror/", "quay.io/thanos/thanos:v0.42.2", "registry.tld/mirror/quay.io/thanos/thanos:v0.42.2_cluster-a"),
		Entry("expands a short origin before joining it",
			"registry.tld/mirror/", "nginx:1.27", "registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a"),
		Entry("lowercases the host of the origin",
			"registry.tld/mirror/", "Quay.IO/acme/foo:v1", "registry.tld/mirror/quay.io/acme/foo:v1_cluster-a"),
		Entry("turns the port separator of the origin host into an underscore",
			"registry.tld/mirror/", "localhost:5000/foo:v1", "registry.tld/mirror/localhost_5000/foo:v1_cluster-a"),
		Entry("adds the trailing slash destination.path lacks",
			"registry.tld/mirror", "quay.io/acme/foo:v1", "registry.tld/mirror/quay.io/acme/foo:v1_cluster-a"),
		Entry("keeps the host port of destination.path verbatim",
			"registry.local:5000/mirror/", "quay.io/acme/foo:v1", "registry.local:5000/mirror/quay.io/acme/foo:v1_cluster-a"),
		Entry("keeps the digest of a tagged and pinned origin after the suffixed tag",
			"registry.tld/mirror/", "quay.io/acme/foo:v1@sha256:"+digestHex, "registry.tld/mirror/quay.io/acme/foo:v1_cluster-a@sha256:"+digestHex),
		Entry("keeps the digest alone for an origin pinned without a tag",
			"registry.tld/mirror/", "quay.io/acme/foo@sha256:"+digestHex, "registry.tld/mirror/quay.io/acme/foo@sha256:"+digestHex),
		Entry("appends the suffix once more to a tag already ending in it",
			"registry.tld/mirror/", "quay.io/acme/foo:v1_cluster-a", "registry.tld/mirror/quay.io/acme/foo:v1_cluster-a_cluster-a"),
	)

	Context("when the suffixed tag would exceed 128 characters", func() {
		// longTag is 128 characters, the longest tag an origin can carry: any suffix pushes it
		// past the limit.
		longTag := strings.Repeat("a", 118) + "0123456789"

		It("truncates the tag and appends a hash of the full upstream tag, as <truncated>-<hash>_<clusterID>", func() {
			tag := destinationTag(longTag)
			Expect(tag).To(MatchRegexp(`^a+-[a-z2-7]{10}_cluster-a$`))
		})

		It("renders a tag of 128 characters at most, the suffix still last", func() {
			tag := destinationTag(longTag)
			Expect(len(tag)).To(BeNumerically("<=", 128))
			Expect(tag).To(HaveSuffix("_" + clusterID))
		})

		It("hashes with the first ten characters of the lowercase base32 sha256 of the full upstream tag", func() {
			sum := sha256.Sum256([]byte(longTag))
			hash := strings.ToLower(base32.StdEncoding.EncodeToString(sum[:]))[:10]
			Expect(destinationTag(longTag)).To(HaveSuffix("-" + hash + "_" + clusterID))
		})

		It("gives two long tags differing past the cut two different destination tags", func() {
			other := strings.Repeat("a", 118) + "9876543210"
			Expect(destinationTag(longTag)).NotTo(Equal(destinationTag(other)))
		})

		It("leaves whole a suffixed tag of exactly 128 characters", func() {
			tag := strings.Repeat("a", 128-len("_"+clusterID))
			Expect(destinationTag(tag)).To(Equal(tag + "_" + clusterID))
		})
	})
})

var _ = Describe("Tags", func() {
	DescribeTable("lists the tags a copy pushes for an origin",
		func(origin string, want []string) {
			Expect(Tags(parse(origin), clusterID)).To(Equal(want))
		},
		Entry("the origin-derived tag for a tagged origin",
			"quay.io/acme/foo:v1", []string{"v1_cluster-a"}),
		Entry("the origin-derived tag and the anchor for a tagged and pinned origin",
			"quay.io/acme/foo:v1@sha256:"+digestHex, []string{"v1_cluster-a", "sha256-" + digestHex + "_cluster-a"}),
		Entry("the anchor alone for an origin pinned without a tag",
			"quay.io/acme/foo@sha256:"+digestHex, []string{"sha256-" + digestHex + "_cluster-a"}),
	)
})

// clusterID is the identity every spec renders tags for.
const clusterID = "cluster-a"

// digestHex is a well-formed sha256 digest, without its algorithm.
const digestHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// parse parses a reference a spec is written with.
func parse(image string) imagepath.Reference {
	GinkgoHelper()
	ref, err := imagepath.Parse(image)
	Expect(err).NotTo(HaveOccurred())
	return ref
}

// destinationTag is the tag of the destination reference of quay.io/acme/foo:<tag>.
func destinationTag(tag string) string {
	GinkgoHelper()
	return parse(Destination("registry.tld/mirror/", parse("quay.io/acme/foo:"+tag), clusterID)).Tag
}
