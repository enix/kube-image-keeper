package main

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// complete is a values file that follows the convention: three blocks, each repeating the
// root pod settings, empty or not. The specs derive their fixtures from it by replacement,
// so that no fixture carries a duplicate key.
const complete = `
image:
  repository: quay.io/enix/kube-image-keeper
  tag: ""
replicas: 1
nodeSelector: {}
webhook:
  replicas: 2
  image: {}
  nodeSelector: {}
reconciler:
  replicas: ~
  image:
    repository: ""
    tag: ""
  nodeSelector: {}
secretSyncer:
  replicas: ~
  image: {}
  nodeSelector: {}
rbac:
  create: true
serviceAccount:
  create: true
`

// with returns the fixture with one exact replacement applied, and fails the spec when the
// text to replace is absent, so a stale fixture cannot pass silently.
func with(old, replacement string) []byte {
	GinkgoHelper()
	Expect(complete).To(ContainSubstring(old))
	return []byte(strings.Replace(complete, old, replacement, 1))
}

var _ = Describe("Chart values", func() {
	It("accepts a file whose three blocks repeat every root pod setting", func() {
		Expect(check([]byte(complete))).To(BeEmpty())
	})

	It("accepts the chart's own values", func() {
		values, err := os.ReadFile("../../helm/kube-image-keeper/values.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(check(values)).To(BeEmpty())
	})

	It("reports a root pod setting a block does not repeat, naming the block and the key", func() {
		values := with("nodeSelector: {}\nwebhook:\n  replicas: 2\n",
			"nodeSelector: {}\ntolerations: []\nwebhook:\n  replicas: 2\n  tolerations: []\n")
		Expect(check(values)).To(ConsistOf(
			ContainSubstring("reconciler: tolerations is missing"),
			ContainSubstring("secretSyncer: tolerations is missing"),
		))
	})

	It("accepts a key repeated with a null value, the way replicas is", func() {
		Expect(check([]byte(complete))).To(BeEmpty(), "reconciler.replicas is ~ in the fixture")
	})

	It("requires a block that spells a map out to spell out every key the root has", func() {
		values := with("secretSyncer:\n  replicas: ~\n  image: {}\n",
			"secretSyncer:\n  replicas: ~\n  image:\n    repository: \"\"\n")
		Expect(check(values)).To(ConsistOf(ContainSubstring("secretSyncer: image.tag is missing")))
	})

	It("ignores the chart-wide root keys, which no block repeats", func() {
		Expect(check([]byte(complete))).To(BeEmpty(), "rbac and serviceAccount are not repeated")
	})

	It("reports a missing process block", func() {
		values := with("secretSyncer:\n  replicas: ~\n  image: {}\n  nodeSelector: {}\n", "secretSyncer: ~\n")
		Expect(check(values)).To(ConsistOf(ContainSubstring("secretSyncer: the process block is missing")))
	})

	It("refuses a file that is not YAML", func() {
		_, err := check([]byte("replicas: [unclosed"))
		Expect(err).To(MatchError(ContainSubstring("parsing the values")))
	})
})
