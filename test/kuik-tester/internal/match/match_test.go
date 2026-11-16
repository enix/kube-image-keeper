package match

import (
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Repo", func() {
	ginkgo.DescribeTable("matching a repository against a pattern",
		func(pattern, repo string, want bool) {
			Expect(Repo(pattern, repo)).To(Equal(want))
		},
		ginkgo.Entry("an identical name", repoFoo, repoFoo, true),
		ginkgo.Entry("a name with a longer last segment", repoFoo, "acme/foo-bar", false),
		ginkgo.Entry("a deeper repository", repoFoo, repoFooBar, false),
		ginkgo.Entry("a single star on one segment", "acme/*", repoFoo, true),
		ginkgo.Entry("a single star on two segments", "acme/*", repoFooBar, false),
		ginkgo.Entry("a double star on several segments", "acme/**", "acme/foo/bar/oni", true),
		ginkgo.Entry("a double star on none", "acme/**", "acme", true),
		ginkgo.Entry("a lone double star", "**", "a/b/c", true),
		ginkgo.Entry("a star with a suffix", "acme/*-debug", "acme/app-debug", true),
		ginkgo.Entry("a double star in the middle", "acme/**/oni", "acme/foo/bar/oni", true),
		ginkgo.Entry("a double star in the middle without the tail", "acme/**/oni", repoFooBar, false),
		ginkgo.Entry("an empty pattern", "", "any/thing", true),
	)
})

var _ = ginkgo.Describe("Repo on a deep repository", func() {
	ginkgo.It("matches many ** against a deep repo in bounded time", func() {
		pattern := "**/*/**/*/**/*/**/*/**/*/**/*/**/b"
		repo := strings.TrimSuffix(strings.Repeat("x/", 64), "/")
		done := make(chan bool, 1)
		go func() {
			defer ginkgo.GinkgoRecover()
			done <- Repo(pattern, repo)
		}()
		Eventually(done).WithTimeout(time.Second).Should(Receive(BeFalse()))
	})
})

var _ = ginkgo.Describe("TrimPrefix", func() {
	ginkgo.DescribeTable("trimming a prefix from a repository",
		func(repo, prefix, rest string, ok bool) {
			gotRest, gotOK := TrimPrefix(repo, prefix)
			Expect(gotRest).To(Equal(rest))
			Expect(gotOK).To(Equal(ok))
		},
		ginkgo.Entry("a prefix of whole segments", "mirror/acme/foo/bar", "mirror/acme", "foo/bar", true),
		ginkgo.Entry("a prefix ending mid-segment", "mirror/acmex/foo", "mirror/acme", "", false),
		ginkgo.Entry("a repository equal to the prefix", repoFoo, repoFoo, "", true),
		ginkgo.Entry("a repository shorter than the prefix", "acme", repoFoo, "", false),
	)
})

var _ = ginkgo.Describe("Label", func() {
	ginkgo.DescribeTable("matching a label against a pattern",
		func(pattern, s string, want bool) {
			Expect(Label(pattern, s)).To(Equal(want))
		},
		ginkgo.Entry("a prefix wildcard that matches", "alt*", "alt1", true),
		ginkgo.Entry("a prefix wildcard that does not match", "alt*", "upstream", false),
		ginkgo.Entry("an empty pattern", "", "x", true),
		ginkgo.Entry("a lone star", "*", "x", true),
		ginkgo.Entry("an identical literal", "t1", "t1", true),
	)
})

var _ = ginkgo.Describe("Valid", func() {
	ginkgo.It("rejects an unclosed bracket", func() {
		Expect(Valid("acme/[x")).NotTo(Succeed())
	})

	ginkgo.It("accepts a double star and a star with a suffix", func() {
		Expect(Valid("acme/**/*-debug")).To(Succeed())
	})
})

var _ = ginkgo.Describe("Literal", func() {
	ginkgo.DescribeTable("telling literals from patterns",
		func(s string, want bool) {
			Expect(Literal(s)).To(Equal(want))
		},
		ginkgo.Entry("an empty string", "", false),
		ginkgo.Entry("a plain label", "t1", true),
		ginkgo.Entry("a repository path", repoFoo, true),
		ginkgo.Entry("a star", "a*", false),
		ginkgo.Entry("a question mark", "a?", false),
		ginkgo.Entry("a character class", "[ab]", false),
		ginkgo.Entry("a backslash", `a\b`, false),
	)
})
