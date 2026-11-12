package dest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
)

const (
	backendA  = "http://zot-a:5000"
	backendB  = "http://zot-b:5000"
	mirror    = "mirror"
	nodelete  = "nodelete"
	other     = "other"
	mirrorAll = "mirror-*"
)

// proxiesOf builds the proxies of a resolved configuration with one proxy
// per name/backend pair.
func proxiesOf(pairs ...[2]string) *Proxies {
	GinkgoHelper()
	cfg := &testerconfig.Config{BaseDomain: "kt.test"}
	for _, p := range pairs {
		cfg.Proxies = append(cfg.Proxies, testerconfig.ProxyHost{Name: p[0], Backend: p[1]})
	}
	Expect(cfg.Resolve()).To(Succeed())
	p, err := NewProxies(cfg)
	Expect(err).NotTo(HaveOccurred())
	return p
}

var _ = Describe("Proxies", func() {
	It("resolves a label by exact name, then by pattern", func() {
		p := proxiesOf([2]string{mirror, backendA}, [2]string{mirrorAll, backendB})

		exact, ok := p.Lookup(mirror)
		Expect(ok).To(BeTrue())
		Expect(exact.Backend.String()).To(Equal(backendA))

		pattern, ok := p.Lookup("mirror-b")
		Expect(ok).To(BeTrue())
		Expect(pattern.Backend.String()).To(Equal(backendB))
		Expect(pattern.Name).To(Equal(mirrorAll))

		_, ok = p.Lookup("mirrorx")
		Expect(ok).To(BeFalse())
	})

	It("shares one client between the proxies of one backend", func() {
		p := proxiesOf([2]string{mirror, backendA}, [2]string{nodelete, backendA}, [2]string{other, backendB})
		first, _ := p.Lookup(mirror)
		second, _ := p.Lookup(nodelete)
		third, _ := p.Lookup(other)
		Expect(first.Client).NotTo(BeNil())
		Expect(first.Client).To(BeIdenticalTo(second.Client))
		Expect(third.Client).NotTo(BeIdenticalTo(first.Client))
	})

	It("lists the configured names, sorted", func() {
		p := proxiesOf([2]string{nodelete, backendA}, [2]string{mirrorAll, backendB}, [2]string{mirror, backendA})
		Expect(p.Names()).To(Equal([]string{mirror, mirrorAll, nodelete}))
	})

	It("lists no name, as an empty list, without proxies", func() {
		Expect(proxiesOf().Names()).To(BeEmpty())
		Expect(proxiesOf().Names()).NotTo(BeNil())
	})

	It("lists each distinct backend once, sorted, whatever the proxies on it", func() {
		p := proxiesOf([2]string{nodelete, backendB}, [2]string{mirrorAll, backendA}, [2]string{mirror, backendA})
		bs := p.Backends()
		Expect(bs).To(HaveLen(2))
		Expect(bs[0].Name).To(Equal(backendA))
		Expect(bs[1].Name).To(Equal(backendB))
		m, _ := p.Lookup(mirror)
		Expect(bs[0].Client).To(BeIdenticalTo(m.Client))
	})

	It("lists no backend, as an empty list, without proxies", func() {
		Expect(proxiesOf().Backends()).To(BeEmpty())
		Expect(proxiesOf().Backends()).NotTo(BeNil())
	})

	It("refuses a configuration that did not go through Resolve", func() {
		cfg := &testerconfig.Config{Proxies: []testerconfig.ProxyHost{{Name: mirror, Backend: backendA}}}
		_, err := NewProxies(cfg)
		Expect(err).To(MatchError(ContainSubstring("proxies[0]")))
		Expect(err).To(MatchError(ContainSubstring(mirror)))
	})
})
