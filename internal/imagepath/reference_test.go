package imagepath

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Parse", func() {
	DescribeTable("normalises a reference",
		func(image, want string) {
			ref, err := Parse(image)
			Expect(err).NotTo(HaveOccurred())
			Expect(ref.String()).To(Equal(want))
		},
		Entry("expands a short name under docker.io/library", "nginx:1.27", "docker.io/library/nginx:1.27"),
		Entry("adds library to a single-segment docker.io path", "docker.io/nginx:1.27", "docker.io/library/nginx:1.27"),
		Entry("reads a reference without a tag as latest", "quay.io/acme/foo", "quay.io/acme/foo:latest"),
		Entry("keeps the port of the host", "registry.local:5000/mirror/foo:v1", "registry.local:5000/mirror/foo:v1"),
		Entry("recognises localhost as a host", "localhost/foo:v1", "localhost/foo:v1"),
		Entry("recognises Docker Hub whatever the case of its host, library included", "Docker.IO/nginx:1.27", "docker.io/library/nginx:1.27"),
		Entry("keeps both the tag and the digest", "quay.io/acme/foo:v1@sha256:"+digestHex, "quay.io/acme/foo:v1@sha256:"+digestHex),
	)

	It("rejects a reference that is not an image reference", func() {
		_, err := Parse("quay.io/Acme/foo:v1")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ParsePath", func() {
	It("accepts a host alone as a repositoryGroup", func() {
		p, err := ParsePath("docker.io", Group)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Host).To(Equal("docker.io"))
		Expect(p.Segments).To(BeEmpty())
	})

	DescribeTable("rejects",
		func(value string, kind Kind) {
			_, err := ParsePath(value, kind)
			Expect(err).To(HaveOccurred())
		},
		Entry("a path carrying a tag", "quay.io/acme/foo:v1", Repository),
		Entry("a path carrying a digest", "quay.io/acme/foo@sha256:"+digestHex, Group),
		Entry("a host alone as a repository", "docker.io", Repository),
		Entry("a path without a registry host", "acme/foo", Group),
		Entry("a path carrying a glob", "quay.io/acme/*", Group),
	)
})

// digestHex is a well-formed sha256 digest, without its algorithm.
const digestHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
