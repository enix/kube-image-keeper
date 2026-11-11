package registry

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Request pacing", func() {
	Context("with a requestInterval on a source host", func() {
		PIt("spaces the requests of a check to that host by the interval, ping and token included", func() {})
		PIt("spaces the source requests of a copy by the interval, child manifests and blobs included", func() {})
		PIt("spaces the source check of a copy and its transfer in one series", func() {})
		PIt("spaces a check and a copy to the same host in one series", func() {})
		PIt("sends the requests waiting on a host in the order they asked", func() {})
		PIt("does not pace a request redirected to another host", func() {})
		PIt("does not pace a token request sent to another host", func() {})
		PIt("does not pace the requests to a mirror destination", func() {})
		PIt("does not pace the requests to another host", func() {})
		PIt("fails a check whose wait outlasts check.timeout, without sending the request or counting it in kuik_registry_requests_total", func() {})
		PIt("fails a copy whose wait outlasts copy.timeout, without sending the request or counting it in kuik_registry_requests_total", func() {})
	})

	Context("with requestInterval 0", func() {
		PIt("sends every request at once", func() {})
	})

	Context("on a reload", func() {
		PIt("spaces the next request by the new interval from the slot already reserved", func() {})
		PIt("sends the requests that ask after the interval drops to 0 at once", func() {})
	})

	Context("on the webhook's active check", func() {
		PIt("sends every request at once, whatever the requestInterval of the host", func() {})
	})
})
