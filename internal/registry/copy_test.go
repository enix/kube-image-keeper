package registry

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Copy", func() {
	Context("on a multi-platform image", func() {
		PIt("pushes the upstream index with its digest unchanged", func() {})
	})

	Context("on a single-platform image", func() {
		PIt("pushes the upstream manifest with its digest unchanged", func() {})
	})

	Context("on a digest-pinned source", func() {
		PIt("copies the pinned digest even after the upstream tag moved", func() {})
		PIt("pushes the manifest under every tag it is given, anchor tag included", func() {})
	})

	Context("when the destination repository already holds the digest", func() {
		PIt("pushes the tags without uploading any blob", func() {})
	})

	Context("with credentials on both sides", func() {
		PIt("reads the source with its own credentials and writes the destination with its own", func() {})
	})

	DescribeTable("failure reason",
		func() {},
		PEntry("is SourceNotFound on a 404 from the source", nil),
		PEntry("is SourceUnreachable when the source does not answer", nil),
		PEntry("is DestinationUnreachable when the destination does not answer", nil),
		PEntry("is PushRejected when the destination refuses the manifest", nil),
		PEntry("is Unauthorized on a 401 from the source", nil),
		PEntry("is Unauthorized on a 401 from the destination", nil),
		PEntry("is QuotaExceeded on a 429 from the source", nil),
		PEntry("is SourceUnreachable on a 5xx from the source", nil),
		PEntry("is DestinationUnreachable on a 5xx from the destination", nil),
	)

	Context("kuik_registry_requests_total", func() {
		PIt("counts the source read under operation Copy and nothing for the destination", func() {})
		PIt("counts a source 404 as ManifestNotFound, the side-agnostic reason", func() {})
	})
})
