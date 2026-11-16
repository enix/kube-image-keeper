package testerconfig

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// zotBackend is a proxy backend that parses.
const zotBackend = "http://zot:5000"

func write(s string) string {
	GinkgoHelper()
	p := filepath.Join(GinkgoT().TempDir(), "config.yaml")
	Expect(os.WriteFile(p, []byte(s), 0o600)).To(Succeed())
	return p
}

var _ = Describe("Load", func() {
	It("keeps the defaults for the keys the file omits or sets to null", func() {
		c, err := Load(write("baseDomain: kt.test\nlogSize: null\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.BaseDomain).To(Equal("kt.test"))
		Expect(c.RegistryAddr).To(Equal(":8080"))
		Expect(c.AdminAddr).To(Equal(":8081"))
		Expect(c.PlainHTTPAddr).To(BeEmpty())
		Expect(c.PlainHTTPHost).To(Equal("http"))
		Expect(c.LogSize).To(Equal(10000))
		Expect(c.ExpireAfter.Std()).To(Equal(7 * 24 * time.Hour))
	})

	DescribeTable("reads expireAfter",
		func(value string, want time.Duration) {
			c, err := Load(write("baseDomain: kt.test\nexpireAfter: " + value + "\n"))
			Expect(err).NotTo(HaveOccurred())
			Expect(c.ExpireAfter.Std()).To(Equal(want))
		},
		Entry("days", `7d`, 7*24*time.Hour),
		Entry("quoted days", `"2d"`, 48*time.Hour),
		Entry("hours", `168h`, 168*time.Hour),
		Entry("minutes", `90m`, 90*time.Minute),
		Entry("the number 0", `0`, time.Duration(0)),
		Entry("the string 0", `"0"`, time.Duration(0)),
		Entry("null keeps the default", `null`, 7*24*time.Hour),
	)

	It("loads a full configuration", func() {
		c, err := Load(write(`
baseDomain: KT.Test.
proxies:
- name: mirror
  backend: http://zot:5000
trustedProxies: ["10.0.0.0/8"]
statePath: /var/lib/kuik-tester/state.json
`))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.BaseDomain).To(Equal("kt.test"))
		Expect(c.Proxies).To(HaveLen(1))
		Expect(c.Proxies[0].Backend).To(Equal(zotBackend))
		Expect(c.StatePath).NotTo(BeEmpty())
	})

	DescribeTable("rejects an invalid document and names the key",
		func(doc, key string) {
			_, err := Load(write(doc))
			Expect(err).To(MatchError(ContainSubstring(key)))
		},
		Entry("missing baseDomain", "logSize: 3\n", "baseDomain"),
		Entry("duplicate proxies", "baseDomain: kt.test\nproxies: [{name: m, backend: 'http://a'}, {name: m, backend: 'http://b'}]\n", "proxies[1].name"),
		Entry("bad backend", "baseDomain: kt.test\nproxies: [{name: m, backend: 'zot:5000'}]\n", "proxies[0].backend"),
		Entry("bad CIDR", "baseDomain: kt.test\ntrustedProxies: ['10.0.0.0/33']\n", "trustedProxies[0]"),
		Entry("negative expireAfter", "baseDomain: kt.test\nexpireAfter: -1h\n", "expireAfter"),
		Entry("negative days in expireAfter", "baseDomain: kt.test\nexpireAfter: -2d\n", "expireAfter"),
		Entry("garbage expireAfter", "baseDomain: kt.test\nexpireAfter: soon\n", "expireAfter"),
		Entry("unknown key", "baseDomain: kt.test\nfoo: bar\n", "foo"),
		Entry("a zero logSize", "baseDomain: kt.test\nlogSize: 0\n", "logSize"),
		Entry("an empty registryAddr", "baseDomain: kt.test\nregistryAddr: \"\"\n", "registryAddr"),
		Entry("an empty adminAddr", "baseDomain: kt.test\nadminAddr: \"\"\n", "adminAddr"),
		Entry("an empty plainHTTPHost", "baseDomain: kt.test\nplainHTTPHost: \"\"\n", "plainHTTPHost"),
		Entry("a dotted plainHTTPHost", "baseDomain: kt.test\nplainHTTPHost: a.b\n", "plainHTTPHost"),
		Entry("a certificate without a key", "baseDomain: kt.test\ntlsCertFile: /tls.crt\n", "tlsCertFile"),
	)

	It("reports every problem of a document at once", func() {
		_, err := Load(write("logSize: 0\nproxies: [{name: m, backend: 'http://a'}, {name: n, backend: 'zot:5000'}]\ntrustedProxies: ['10.0.0.0/33']\n"))
		Expect(err).To(HaveOccurred())
		for _, key := range []string{"baseDomain", "logSize", "proxies[1].backend", "trustedProxies[0]"} {
			Expect(err).To(MatchError(ContainSubstring(key)))
		}
	})

	It("wraps a read error with the stage and the path", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "absent.yaml")
		_, err := Load(missing)
		Expect(err).To(MatchError(fs.ErrNotExist))
		Expect(err).To(MatchError(ContainSubstring("reading config " + missing)))
	})

	It("parses the backends and the trusted proxies once", func() {
		c, err := Load(write("baseDomain: kt.test\nproxies:\n- name: mirror\n  backend: http://zot:5000\ntrustedProxies: ['10.0.0.0/8']\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Proxies[0].URL.Host).To(Equal("zot:5000"))
		p, ok := c.Proxy("mirror")
		Expect(ok).To(BeTrue())
		Expect(p.URL).To(BeIdenticalTo(c.Proxies[0].URL))
		Expect(c.TrustedNets).To(HaveLen(1))
		Expect(c.TrustedNets[0].String()).To(Equal("10.0.0.0/8"))
	})

	Describe("Resolve", func() {
		It("parses a configuration built in code", func() {
			c := &Config{
				Proxies:        []ProxyHost{{Name: "Mirror", Backend: zotBackend}},
				TrustedProxies: []string{"10.0.0.0/8"},
			}
			Expect(c.Resolve()).To(Succeed())
			Expect(c.Proxies[0].Name).To(Equal("mirror"))
			Expect(c.Proxies[0].URL.Host).To(Equal("zot:5000"))
			Expect(c.TrustedNets).To(HaveLen(1))
			_, ok := c.Proxy("mirror")
			Expect(ok).To(BeTrue())
		})

		It("reports a backend or a CIDR that does not parse, with its key", func() {
			c := &Config{
				Proxies:        []ProxyHost{{Name: "m", Backend: "zot:5000"}},
				TrustedProxies: []string{"nope"},
			}
			err := c.Resolve()
			Expect(err).To(MatchError(ContainSubstring("proxies[0].backend")))
			Expect(err).To(MatchError(ContainSubstring("trustedProxies[0]")))
		})

		It("can run twice", func() {
			c := &Config{
				PlainHTTPHost:  " Plain ",
				Proxies:        []ProxyHost{{Name: " Mirror ", Backend: zotBackend}},
				TrustedProxies: []string{"10.0.0.0/8"},
			}
			Expect(c.Resolve()).To(Succeed())
			first := *c
			first.Proxies = slices.Clone(c.Proxies)
			first.TrustedNets = slices.Clone(c.TrustedNets)
			Expect(c.Resolve()).To(Succeed())
			Expect(*c).To(Equal(first))
			Expect(c.PlainHTTPHost).To(Equal("plain"))
			Expect(c.Proxies[0].Name).To(Equal("mirror"))
			Expect(c.Proxies[0].URL.Host).To(Equal("zot:5000"))
			Expect(c.TrustedNets).To(HaveLen(1))
		})

		It("trims the plain HTTP host and the proxy names", func() {
			c := &Config{PlainHTTPHost: " Plain ", Proxies: []ProxyHost{{Name: " Mirror ", Backend: zotBackend}}}
			Expect(c.Resolve()).To(Succeed())
			Expect(c.PlainHTTPHost).To(Equal("plain"))
			Expect(c.Proxies[0].Name).To(Equal("mirror"))
		})
	})

	It("lower-cases the proxy names", func() {
		c, err := Load(write("baseDomain: kt.test\nproxies:\n- name: Mirror\n  backend: http://zot:5000\n"))
		Expect(err).NotTo(HaveOccurred())
		_, ok := c.Proxy("mirror")
		Expect(ok).To(BeTrue(), "proxies: %+v", c.Proxies)
		_, err = Load(write("baseDomain: kt.test\nproxies:\n- name: Mirror\n  backend: http://zot:5000\n- name: mirror\n  backend: http://zot:5000\n"))
		Expect(err).To(MatchError(ContainSubstring("duplicate")))
	})

	DescribeTable("rejects a proxy backend with more than scheme and host",
		func(b string) {
			_, err := Load(write("baseDomain: kt.test\nproxies:\n- name: m\n  backend: " + b + "\n"))
			Expect(err).To(MatchError(ContainSubstring("proxies[0].backend")))
		},
		Entry("a path", "http://zot:5000/v2"),
		Entry("a query", "http://zot:5000?x=1"),
		Entry("user info", "http://u:p@zot:5000"),
		Entry("a fragment", "http://zot:5000#x"),
	)

	It("accepts a proxy backend with a trailing slash", func() {
		_, err := Load(write("baseDomain: kt.test\nproxies:\n- name: m\n  backend: http://zot:5000/\n"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("lower-cases and validates plainHTTPHost", func() {
		c, err := Load(write("baseDomain: kt.test\nplainHTTPHost: Mirror\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.PlainHTTPHost).To(Equal("mirror"))
		_, err = Load(write("baseDomain: kt.test\nplainHTTPHost: a.b\n"))
		Expect(err).To(MatchError(ContainSubstring("plainHTTPHost")), "a dotted plainHTTPHost")
	})

	Context("with proxy name patterns", func() {
		DescribeTable("resolves a label against literal and wildcard names",
			func(label, backend string) {
				c, err := Load(write("baseDomain: kt.test\nproxies:\n- name: mirror\n  backend: http://a:5000\n- name: mirror-*\n  backend: http://b:5000\n"))
				Expect(err).NotTo(HaveOccurred())
				p, ok := c.Proxy(label)
				Expect(ok).To(BeTrue(), "Proxy(%q)", label)
				Expect(p.Backend).To(Equal(backend), "Proxy(%q)", label)
			},
			Entry("the literal name", "mirror", "http://a:5000"),
			Entry("the wildcard name", "mirror-b", "http://b:5000"),
		)

		DescribeTable("finds no proxy for a label that matches no name",
			func(label string) {
				c, err := Load(write("baseDomain: kt.test\nproxies:\n- name: mirror\n  backend: http://a:5000\n- name: mirror-*\n  backend: http://b:5000\n"))
				Expect(err).NotTo(HaveOccurred())
				_, ok := c.Proxy(label)
				Expect(ok).To(BeFalse(), "Proxy(%q) found", label)
			},
			Entry("a longer name", "mirrorx"),
			Entry("a suffix match", "upstream-mirror"),
		)

		DescribeTable("rejects an invalid pattern",
			func(name string) {
				_, err := Load(write("baseDomain: kt.test\nproxies:\n- name: '" + name + "'\n  backend: http://a:5000\n"))
				Expect(err).To(MatchError(ContainSubstring("proxies[0].name")))
			},
			Entry("a dot", "mirror.*"),
			Entry("an unclosed bracket", "mirror-["),
		)
	})
})
