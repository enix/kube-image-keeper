package main

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const sampleSuite = `package sample

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ImageMirror", func() {
	BeforeEach(func() {})

	Context("defaults", func() {
		It("defaults rewritePolicy to OnFailure", func() {
			By("creating the resource")
			Expect(true).To(BeTrue())
		})
		PIt("keeps an explicit cleanup.enabled false", func() {})
	})

	DescribeTable("destination paths",
		func(path string) {},
		Entry("accepts a path with a trailing slash", "registry.tld/mirror/"),
		Entry("rejects a path carrying a tag", "registry.tld/mirror:latest"),
	)

	for _, f := range []string{"a", "b"} {
		Entry(f, f)
	}
})
`

const plainTest = `package sample

import "testing"

func TestSomething(t *testing.T) {}
`

func outlineOf(src string) []*node {
	nodes, err := parse("sample_test.go", []byte(src))
	Expect(err).NotTo(HaveOccurred())
	return nodes
}

var _ = Describe("parse", func() {
	It("keeps the containers and specs, and drops the setup nodes", func() {
		var b bytes.Buffer
		writeTree(&b, outlineOf(sampleSuite), 0)
		Expect(b.String()).To(Equal(`ImageMirror
  defaults
    - defaults rewritePolicy to OnFailure
    - keeps an explicit cleanup.enabled false [pending]
  destination paths
    - accepts a path with a trailing slash
    - rejects a path carrying a tag
  - Entry (dynamic text)
`))
	})

	It("returns no node for a test file that does not use Ginkgo", func() {
		Expect(outlineOf(plainTest)).To(BeEmpty())
	})

	It("fails on a file that does not parse", func() {
		_, err := parse("broken_test.go", []byte("package sample\nfunc {"))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("cases", func() {
	It("flattens each spec with its containers", func() {
		Expect(cases(outlineOf(sampleSuite))).To(Equal([]string{
			"ImageMirror / defaults / defaults rewritePolicy to OnFailure",
			"ImageMirror / defaults / keeps an explicit cleanup.enabled false [pending]",
			"ImageMirror / destination paths / accepts a path with a trailing slash",
			"ImageMirror / destination paths / rejects a path carrying a tag",
			"ImageMirror / Entry (dynamic text)",
		}))
	})
})

var _ = Describe("diffCases", func() {
	const one, two, same = "A / one", "A / two", "A / same"

	It("reports a renamed case as one removed and one added", func() {
		added, removed := diffCases(
			[]string{"A / accepts a Go duration", "A / rejects two rings"},
			[]string{"A / accepts any Go duration", "A / rejects two rings"},
		)
		Expect(removed).To(Equal([]string{"A / accepts a Go duration"}))
		Expect(added).To(Equal([]string{"A / accepts any Go duration"}))
	})

	It("reports nothing when the cases only moved", func() {
		added, removed := diffCases([]string{one, two}, []string{two, one})
		Expect(added).To(BeEmpty())
		Expect(removed).To(BeEmpty())
	})

	It("counts duplicated case texts separately", func() {
		added, removed := diffCases([]string{same, same}, []string{same})
		Expect(added).To(BeEmpty())
		Expect(removed).To(Equal([]string{same}))
	})

	It("treats a file present on one side only as entirely added or removed", func() {
		added, removed := diffCases(nil, []string{one})
		Expect(added).To(Equal([]string{one}))
		Expect(removed).To(BeEmpty())
	})
})
