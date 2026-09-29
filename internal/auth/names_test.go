package auth

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/validation"
)

// longestPrefix is `kuik-inject-` plus the longest kind that injects a Secret, and its
// separator: the 29 characters architecture.md counts.
const longestPrefix = "kuik-inject-imagealternative-"

var _ = Describe("InjectedSecretName", func() {
	It("derives kuik-inject-<kind lowercased>-<CR name>", func() {
		Expect(InjectedSecretName("ImageMirror", "prod-mirror")).To(Equal("kuik-inject-imagemirror-prod-mirror"))
	})

	It("keeps a CR name of 224 characters whole", func() {
		name := strings.Repeat("a", 224)
		Expect(InjectedSecretName("ImageAlternative", name)).To(Equal(longestPrefix + name))
	})

	It("truncates a longer CR name and appends a hash, into a valid Secret name", func() {
		// Two names shifted by one character, so that the cut falls on a dot in one of them.
		dotted := strings.Repeat("a.", 120) + "a"
		for _, name := range []string{dotted, "b" + dotted} {
			got := InjectedSecretName("ImageAlternative", name)
			Expect(got).To(HavePrefix(longestPrefix))
			Expect(got).NotTo(ContainSubstring(name))
			Expect(len(got)).To(BeNumerically("<=", validation.DNS1123SubdomainMaxLength))
			Expect(validation.IsDNS1123Subdomain(got)).To(BeEmpty(), got)
		}
	})

	It("gives different names to two CR names differing only past the cut", func() {
		base := strings.Repeat("a", 240)
		Expect(InjectedSecretName("ImageAlternative", base+"x")).
			NotTo(Equal(InjectedSecretName("ImageAlternative", base+"y")))
	})
})
