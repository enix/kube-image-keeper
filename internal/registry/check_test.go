package registry

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Check", func() {
	Context("on an available image", func() {
		PIt("sends one manifest HEAD and nothing else", func() {})
		PIt("returns the manifest descriptor and the credential that answered", func() {})
	})

	Context("on a reference pinned by digest, tag included", func() {
		PIt("sends one manifest HEAD by digest, whatever the tag points at", func() {})
	})

	DescribeTable("failure reason",
		func() {},
		PEntry("is ManifestNotFound on a 404", nil),
		PEntry("is Unauthorized on a 401", nil),
		PEntry("is Unauthorized on a 403", nil),
		PEntry("is QuotaExceeded on a 429 with rate-limit headers", nil),
		PEntry("is QuotaExceeded on a 429 without rate-limit headers", nil),
		PEntry("is QuotaExceeded on a 200 whose rate-limit headers are exhausted", nil),
		PEntry("is Unreachable when the registry does not answer", nil),
		PEntry("is Unreachable when the deadline expires", nil),
		PEntry("is Unreachable on a 5xx", nil),
	)

	Context("kuik_registry_requests_total", func() {
		PIt("counts a check on a source registry under its host, operation Check and its result", func() {})
	})

	Context("on a mirror destination", func() {
		PIt("reports an outage as Unreachable, never as ManifestNotFound", func() {})
		PIt("counts nothing in kuik_registry_requests_total", func() {})
	})
})
