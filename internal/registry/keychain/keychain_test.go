package keychain

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Authenticators", func() {
	PIt("returns the credential of a Secret whose registry covers the image", func() {})
	PIt("returns no credential when no Secret covers the image", func() {})
	PIt("orders the matching credentials from the longest path to the shortest", func() {})
	PIt("matches a registry key with a glob in its host", func() {})
	PIt("applies an index.docker.io credential to a short Docker Hub name such as nginx", func() {})
	PIt("returns one credential per Secret when two Secrets cover the same registry", func() {})
	PIt("reads a legacy kubernetes.io/dockercfg Secret", func() {})
	PIt("returns a MalformedSecretError for a Secret whose content is not a docker config", func() {})
})
