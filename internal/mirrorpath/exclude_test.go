package mirrorpath

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Excluded", func() {
	DescribeTable("excludes an image an excludeImages pattern matches",
		func(pattern, image string) {
			Expect(Excluded([]string{pattern}, parse(image))).To(BeTrue())
		},
		Entry("** covers everything under a path, at any depth",
			"ghcr.io/foo-bar/**", "ghcr.io/foo-bar/team/app:v1"),
		Entry("* covers one level under a path",
			"ghcr.io/foo-bar/*", "ghcr.io/foo-bar/app:v1"),
		Entry("* matches any characters inside one segment",
			"quay.io/acme/*-debug", "quay.io/acme/foo-debug:v1"),
		Entry("a repository part alone covers every tag",
			"docker.io/library/nginx", "docker.io/library/nginx:1.27"),
		Entry("a repository part alone covers every digest",
			"docker.io/library/nginx", "docker.io/library/nginx@sha256:"+digestHex),
		Entry("a short image is matched as its normalised reference",
			"docker.io/library/nginx", "nginx:1.27"),
		Entry("a tag part narrows the pattern to the tags it matches",
			"**:latest", "quay.io/acme/foo:latest"),
		Entry("a tag part is a glob too",
			"quay.io/acme/foo:v1.*", "quay.io/acme/foo:v1.2"),
		Entry("a : before the last / is a host port, not a tag part",
			"registry.local:5000/mirror/**", "registry.local:5000/mirror/foo:v1"),
	)

	DescribeTable("keeps an image no excludeImages pattern matches",
		func(pattern, image string) {
			Expect(Excluded([]string{pattern}, parse(image))).To(BeFalse())
		},
		Entry("* does not cross a path separator",
			"ghcr.io/foo-bar/*", "ghcr.io/foo-bar/team/app:v1"),
		Entry("a repository part is matched whole, not as a prefix",
			"docker.io/library/nginx", "docker.io/library/nginx-exporter:v1"),
		Entry("a repository part does not cover the repositories below it",
			"docker.io/library/nginx", "docker.io/library/nginx/sub:v1"),
		Entry("a tag part keeps the tags it does not match",
			"**:latest", "quay.io/acme/foo:v1"),
	)

	// The spec says a digest-pinned reference "is matched on its repository part alone": only
	// a pattern with no tag part covers it, so `**:latest` stays about mutable tags.
	Context("with a digest-pinned image", func() {
		It("keeps an image pinned without a tag from a pattern with a tag part", func() {
			Expect(Excluded([]string{"quay.io/acme/foo:*"}, parse("quay.io/acme/foo@sha256:"+digestHex))).To(BeFalse())
		})

		It("keeps an image tagged and pinned from a pattern with a tag part", func() {
			Expect(Excluded([]string{"quay.io/acme/foo:v1"}, parse("quay.io/acme/foo:v1@sha256:"+digestHex))).To(BeFalse())
		})
	})
})

var _ = Describe("UnderDestination", func() {
	It("recognises an image under destination.path", func() {
		Expect(UnderDestination("registry.tld/mirror/", parse("registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a"))).To(BeTrue())
	})

	It("recognises it whether or not destination.path ends with a slash", func() {
		Expect(UnderDestination("registry.tld/mirror", parse("registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a"))).To(BeTrue())
	})

	It("compares the host of destination.path case-insensitively", func() {
		Expect(UnderDestination("Registry.TLD/mirror/", parse("registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a"))).To(BeTrue())
	})

	It("does not take a sibling path sharing a string prefix for the destination", func() {
		Expect(UnderDestination("registry.tld/mirror", parse("registry.tld/mirror-2/docker.io/library/nginx:1.27"))).To(BeFalse())
	})
})
