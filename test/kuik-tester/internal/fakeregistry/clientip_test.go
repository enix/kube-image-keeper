package fakeregistry

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
)

// The client, and a peer inside the trusted 10.0.0.0/8.
const (
	origin    = "1.2.3.4"
	proxyPeer = "10.0.0.1:1"
)

var _ = Describe("Client address", func() {
	DescribeTable("picks the client address",
		func(peer string, xff []string, want string) {
			cfg := &testerconfig.Config{BaseDomain: baseDomain, TrustedProxies: []string{"10.0.0.0/8"}}
			r := httptest.NewRequest(http.MethodGet, "http://upstream.kt.test/v2/", nil)
			r.RemoteAddr = peer
			for _, v := range xff {
				r.Header.Add(headerForwardedFor, v)
			}
			_, e := serveDirect(cfg, nil, r)
			Expect(e.ClientIP).To(Equal(want), "peer %s xff %q", peer, xff)
		},
		Entry("untrusted peer: header ignored", "5.6.7.8:1", []string{origin}, "5.6.7.8"),
		Entry("trusted peer: rightmost untrusted hop", proxyPeer, []string{"1.2.3.4, 10.0.0.2"}, origin),
		Entry("trusted peer: spoofed leftmost hop ignored", proxyPeer, []string{"9.9.9.9, 1.2.3.4"}, origin),
		Entry("trusted peer: only trusted hops", proxyPeer, []string{"10.0.0.3"}, "10.0.0.3"),
		Entry("trusted peer: no header", proxyPeer, []string(nil), "10.0.0.1"),
		Entry("trusted peer: several header lines", proxyPeer, []string{"9.9.9.9", "1.2.3.4, 10.0.0.2"}, origin),
		Entry("trusted peer: unparsable hop", proxyPeer, []string{"garbage"}, "10.0.0.1"),
		Entry("trusted peer: IPv4 hop with a port", proxyPeer, []string{"1.2.3.4:5678"}, origin),
		Entry("trusted peer: bracketed IPv6 hop with a port", proxyPeer, []string{"[2001:db8::1]:80"}, "2001:db8::1"),
		Entry("trusted peer: bracketed IPv6 hop", proxyPeer, []string{"[2001:db8::1]"}, "2001:db8::1"),
	)
})
