package routing

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseResource", func() {
	DescribeTable("reads back a resource",
		func(s string, want Resource) {
			got, err := ParseResource(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
			Expect(got.String()).To(Equal(s))
		},
		Entry("of kind ImageAlternative", "ImageAlternative/thanos", Resource{Kind: KindImageAlternative, Name: "thanos"}),
		Entry("of kind ImageMirror", "ImageMirror/prod-mirror", Resource{Kind: KindImageMirror, Name: "prod-mirror"}),
	)
	DescribeTable("rejects",
		func(s string) {
			_, err := ParseResource(s)
			Expect(err).To(HaveOccurred())
		},
		Entry("a kind that routes nothing", "ImageMonitor/global"),
		Entry("an unknown kind", "Deployment/thanos"),
		Entry("a missing name", "ImageAlternative/"),
		Entry("a value without a slash", "ImageAlternative"),
		Entry("an empty value", ""),
	)
})
