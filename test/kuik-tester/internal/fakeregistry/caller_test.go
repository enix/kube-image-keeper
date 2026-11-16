package fakeregistry

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("callerClass", func() {
	DescribeTable("classifying a user agent",
		func(ua, want string) {
			Expect(callerClass(ua)).To(Equal(want))
		},
		Entry("crane", "crane/v0.22.1 go-containerregistry/v0.22.1", "crane"),
		Entry("the bare go-containerregistry agent", "go-containerregistry/v0.22.1", "kuik"),
		Entry("the kuik controller", "kuik-controller/1.0", "kuik"),
		Entry("containerd", "containerd/v2.1.4", "containerd"),
		Entry("curl", "curl/8", "other"),
		Entry("an empty agent", "", "other"),
	)
})
