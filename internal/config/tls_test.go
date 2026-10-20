package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("tls", func() {
	It("reads a caSecretRef entry and an insecureSkipVerify entry, each under its host", func() {
		cfg := mustLoad(minimal + `
tls:
  harbor.corp.tld:
    caSecretRef:
      name: harbor-ca
      key: bundle.pem
  registry.lab.tld:5000:
    insecureSkipVerify: true
`)
		Expect(cfg.TLS).To(Equal(TLS{
			"harbor.corp.tld":       {CASecretRef: &CASecretRef{Name: "harbor-ca", Key: "bundle.pem"}},
			"registry.lab.tld:5000": {InsecureSkipVerify: true},
		}))
	})

	It("reads ca.crt from a caSecretRef that names no key", func() {
		cfg := mustLoad(minimal + `
tls:
  harbor.corp.tld:
    caSecretRef:
      name: harbor-ca
`)
		Expect(cfg.TLS["harbor.corp.tld"].CASecretRef).To(Equal(&CASecretRef{Name: "harbor-ca", Key: "ca.crt"}))
	})

	It("keys an entry by its host as registries does: lowercased, index.docker.io as docker.io", func() {
		cfg := mustLoad(minimal + `
tls:
  Harbor.Corp.TLD:
    insecureSkipVerify: true
  index.docker.io:
    insecureSkipVerify: true
`)
		Expect(cfg.TLS).To(HaveKey("harbor.corp.tld"))
		Expect(cfg.TLS).To(HaveKey("docker.io"))
		Expect(cfg.TLS).To(HaveLen(2))
	})

	It("accepts an entry that sets nothing", func() {
		cfg := mustLoad(minimal + `
tls:
  harbor.corp.tld: {}
`)
		Expect(cfg.TLS).To(Equal(TLS{"harbor.corp.tld": {}}))
	})

	It("keeps host and host:443 as two keys", func() {
		cfg := mustLoad(minimal + `
tls:
  harbor.corp.tld:
    insecureSkipVerify: true
  harbor.corp.tld:443:
    caSecretRef:
      name: harbor-ca
`)
		Expect(cfg.TLS).To(HaveLen(2))
		Expect(cfg.TLS["harbor.corp.tld"].InsecureSkipVerify).To(BeTrue())
		Expect(cfg.TLS["harbor.corp.tld:443"].CASecretRef.Name).To(Equal("harbor-ca"))
	})

	DescribeTable("rejects the whole file",
		func(content string) {
			cfg, err := Load(writeFile(minimal + content))
			Expect(err).To(HaveOccurred())
			Expect(cfg).To(BeNil())
		},
		Entry("when an entry sets both caSecretRef and insecureSkipVerify", `
tls:
  harbor.corp.tld:
    caSecretRef:
      name: harbor-ca
    insecureSkipVerify: true
`),
		Entry("when a caSecretRef names no Secret", `
tls:
  harbor.corp.tld:
    caSecretRef:
      key: ca.crt
`),
		Entry("when a caSecretRef carries a namespace", `
tls:
  harbor.corp.tld:
    caSecretRef:
      name: harbor-ca
      namespace: other
`),
		Entry("when a key is not a registry host", `
tls:
  harbor.corp.tld/project:
    insecureSkipVerify: true
`),
		Entry("when a key is not a host name", `
tls:
  harbor:
    insecureSkipVerify: true
`),
		Entry("when an entry carries a key the schema does not define", `
tls:
  harbor.corp.tld:
    insecure: true
`),
		Entry("when two keys name the same host", `
tls:
  docker.io:
    insecureSkipVerify: true
  index.docker.io:
    insecureSkipVerify: true
`),
	)
})
