package client

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("New", func() {
	It("sets no whole-request timeout, callers bound calls with their context", func() {
		Expect(New("http://example.invalid", "").http.Timeout).To(BeZero())
	})
})
