package registry

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Client", func() {
	Context("with several credentials", func() {
		PIt("tries them in order and stops at the first that answers", func() {})
		PIt("does not fall back to anonymous when every credential is refused", func() {})
	})

	Context("with no credential", func() {
		PIt("reads anonymously", func() {})
	})

	Context("with an insecure registry", func() {
		PIt("reaches it over plain HTTP", func() {})
	})

	Context("when the registry answers 429", func() {
		PIt("sends the request once, without retrying it", func() {})
	})

	Context("with a deadline on the caller's context", func() {
		PIt("bounds the whole operation, every credential attempt included", func() {})
	})

	Context("with a zero timeout", func() {
		PIt("leaves the operation unbounded", func() {})
	})

	Context("when concurrent calls share one client", func() {
		PIt("reads the rate-limit headers of each call from its own response", func() {})
	})
})
