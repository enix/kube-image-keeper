package ref

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Ref", func() {
	DescribeTable("ValidLabel",
		func(s string, want bool) {
			Expect(ValidLabel(s)).To(Equal(want), "ValidLabel(%q)", s)
		},
		Entry("a single letter", "a", true),
		Entry("a name", "upstream", true),
		Entry("an underscore", "alt_1", true),
		Entry("a dash", "a-b", true),
		Entry("63 bytes", strings.Repeat("a", 63), true),
		Entry("empty", "", false),
		Entry("64 bytes", strings.Repeat("a", 64), false),
		Entry("an upper-case letter", "Upstream", false),
		Entry("a leading underscore", "_a", false),
		Entry("a leading dash", "-a", false),
		Entry("a wildcard", "we*ird", false),
		Entry("a dot", "a.b", false),
	)

	DescribeTable("ValidRepo",
		func(s string, want bool) {
			Expect(ValidRepo(s)).To(Equal(want), "ValidRepo(%q)", s)
		},
		Entry("two segments", "acme/foo", true),
		Entry("a double underscore", "a__b", true),
		Entry("dashes and a dot", "a--b/c.d", true),
		Entry("255 bytes", strings.Repeat("a", 255), true),
		Entry("32 segments", strings.Repeat("a/", 31)+"a", true),
		Entry("empty", "", false),
		Entry("256 bytes", strings.Repeat("a", 256), false),
		Entry("33 segments", strings.Repeat("a/", 32)+"a", false),
		Entry("an upper-case letter", "Acme/foo", false),
		Entry("an empty segment", "acme//foo", false),
		Entry("a trailing slash", "acme/", false),
		Entry("a leading slash", "/acme", false),
		Entry("a triple underscore", "a___b", false),
		Entry("a dot", ".", false),
		Entry("two dots", "..", false),
		Entry("a leading dash", "-a", false),
		Entry("a wildcard", "acme/*", false),
	)

	DescribeTable("ValidTag",
		func(s string, want bool) {
			Expect(ValidTag(s)).To(Equal(want), "ValidTag(%q)", s)
		},
		Entry("a short tag", "t1", true),
		Entry("a leading underscore", "_t", true),
		Entry("dots, dashes and underscores", "v1.2-rc_3", true),
		Entry("128 characters", strings.Repeat("a", 128), true),
		Entry("empty", "", false),
		Entry("129 characters", strings.Repeat("a", 129), false),
		Entry("a leading dot", ".t", false),
		Entry("a leading dash", "-t", false),
		Entry("a colon", "t:1", false),
		Entry("a digest", "sha256:"+strings.Repeat("a", 64), false),
	)
})
