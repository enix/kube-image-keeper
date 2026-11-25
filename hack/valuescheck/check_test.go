package main

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// complete is a values file that follows the convention: three blocks, each repeating the
// root pod settings, left empty. The specs derive their fixtures from it by replacement,
// so that no fixture carries a duplicate key.
const complete = `
image:
  repository: quay.io/enix/kube-image-keeper
  tag: ""
replicas: 1
nodeSelector: {}
webhook:
  replicas: ~
  image: {}
  nodeSelector: {}
  serviceAccount:
    create: ~
    name: ""
    annotations: {}
reconciler:
  replicas: ~
  image:
    repository: ""
    tag: ""
  nodeSelector: {}
  serviceAccount: {}
secretSyncer:
  replicas: ~
  image: {}
  nodeSelector: {}
  serviceAccount: {}
rbac:
  create: true
secretAccess:
  mode: permissive
serviceAccount:
  create: true
  annotations: {}
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
		values := with("nodeSelector: {}\nwebhook:\n  replicas: ~\n",
			"nodeSelector: {}\ntolerations: []\nwebhook:\n  replicas: ~\n  tolerations: []\n")
		Expect(check(values)).To(ConsistOf(
			ContainSubstring("reconciler: tolerations is missing"),
			ContainSubstring("secretSyncer: tolerations is missing"),
		))
	})

	It("requires a block that spells a map out to spell out every key the root has", func() {
		values := with("secretSyncer:\n  replicas: ~\n  image: {}\n",
			"secretSyncer:\n  replicas: ~\n  image:\n    repository: \"\"\n")
		Expect(check(values)).To(ConsistOf(ContainSubstring("secretSyncer: image.tag is missing")))
	})

	It("reports a block that sets a root pod setting, which raising the root would no longer raise", func() {
		values := with("webhook:\n  replicas: ~\n", "webhook:\n  replicas: 2\n")
		Expect(check(values)).To(ConsistOf(ContainSubstring("webhook: replicas is set")))
	})

	It("reports a block that sets a key of a root pod setting it spells out", func() {
		values := with("reconciler:\n  replicas: ~\n  image:\n    repository: \"\"\n",
			"reconciler:\n  replicas: ~\n  image:\n    repository: quay.io/other/kuik\n")
		Expect(check(values)).To(ConsistOf(ContainSubstring("reconciler: image.repository is set")))
	})

	It("reports a block that sets a key the root map of a pod setting does not have", func() {
		values := with("webhook:\n  replicas: ~\n  image: {}\n  nodeSelector: {}\n",
			"webhook:\n  replicas: ~\n  image: {}\n  nodeSelector:\n    disktype: ssd\n")
		Expect(check(values)).To(ConsistOf(ContainSubstring("webhook: nodeSelector.disktype is set")))
	})

	It("reports a missing process block", func() {
		values := with("secretSyncer:\n  replicas: ~\n  image: {}\n  nodeSelector: {}\n  serviceAccount: {}\n",
			"secretSyncer: ~\n")
		Expect(check(values)).To(ConsistOf(ContainSubstring("secretSyncer: the process block is missing")))
	})

	It("refuses a file that is not YAML", func() {
		_, err := check([]byte("replicas: [unclosed"))
		Expect(err).To(MatchError(ContainSubstring("parsing the values")))
	})
})
