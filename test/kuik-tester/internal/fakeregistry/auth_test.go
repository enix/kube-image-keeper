package fakeregistry

import (
	"net/http"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("Basic authentication", func() {
	It("serves a pull of a granted repo to its user and logs the user", func() {
		srv, _, log := newServer(withState(authState))
		_, err := remote.Image(testutil.Ref("private.kt.test/team/app:t1"), authOpts(srv, alice, "pw")...)
		Expect(err).NotTo(HaveOccurred())
		e := waitEntry(log, func(e api.LogEntry) bool { return e.Kind == api.KindManifestByTag })
		Expect(e.User).To(Equal(alice))
	})

	It("challenges an anonymous client on a granted repo", func() {
		srv, _, _ := newServer(withState(authState))
		_, err := remote.Head(testutil.Ref("private.kt.test/team/app:t1"), testutil.Opts(srv)...)
		Expect(statusOf(err)).To(Equal(401), "err = %v", err)
		resp, _ := raw(srv, http.MethodGet, privateFQDN, "/v2/team/app/manifests/t1", nil)
		wantChallenge(resp, basicChallenge)
	})

	It("challenges an anonymous catalog request", func() {
		srv, _, _ := newServer(withState(authState))
		resp, _ := raw(srv, http.MethodGet, privateFQDN, "/v2/_catalog", nil)
		wantChallenge(resp, basicChallenge)
	})

	It("denies a user the grant does not name", func() {
		srv, _, _ := newServer(withState(authState))
		_, err := remote.Head(testutil.Ref("private.kt.test/team/app:t1"), authOpts(srv, "bob", "pw")...)
		Expect(statusOf(err)).To(Equal(403), "err = %v", err)
		resp, body := raw(srv, http.MethodGet, privateFQDN, "/v2/team/app/manifests/t1", basic("bob", "pw"))
		Expect(resp.StatusCode).To(Equal(403), "body %s", body)
		Expect(string(body)).To(ContainSubstring(`"DENIED"`))
	})

	It("lets an anonymous client pull a repo no grant covers but challenges the ping", func() {
		srv, _, _ := newServer(withState(authState))
		_, err := remote.Image(testutil.Ref("private.kt.test/public/x:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		resp, _ := raw(srv, http.MethodGet, privateFQDN, "/v2/", nil)
		Expect(resp.StatusCode).To(Equal(401), "anonymous ping on private")
		resp, _ = raw(srv, http.MethodGet, privateFQDN, "/v2/", basic(alice, "pw"))
		Expect(resp.StatusCode).To(Equal(200), "alice ping on private")
		resp, _ = raw(srv, http.MethodGet, upstreamFQDN, "/v2/", nil)
		Expect(resp.StatusCode).To(Equal(200), "anonymous ping on upstream")
	})

	It("rejects a wrong password or a malformed header before any rule applies", func() {
		srv, st, log := newServer(withState(authState))
		addRule(st, api.Rule{ID: "r", Target: api.Target{Host: private, Kinds: []api.Kind{api.KindManifestByTag}}, Effects: api.Effects{Status: 503}})
		_, err := remote.Head(testutil.Ref("private.kt.test/team/app:t1"), authOpts(srv, alice, "nope")...)
		Expect(statusOf(err)).To(Equal(401), "err = %v", err)
		e := waitEntry(log, func(e api.LogEntry) bool { return e.User != "" })
		Expect(e.User).To(Equal("alice (invalid)"))
		Expect(e.RuleID).To(BeEmpty())
		resp, _ := raw(srv, http.MethodGet, privateFQDN, "/v2/", map[string]string{headerAuthorization: "Basic !!!"})
		Expect(resp.StatusCode).To(Equal(401), "malformed Basic")
		waitEntry(log, func(e api.LogEntry) bool { return e.User == "(invalid)" })
	})

	It("lets a rule target the authenticated caller", func() {
		srv, st, _ := newServer(withState(authState))
		addRule(st, api.Rule{ID: "r", Target: api.Target{Host: private, Caller: alice}, Effects: api.Effects{Status: 503}})
		_, err := remote.Head(testutil.Ref("private.kt.test/team/app:t1"), authOpts(srv, alice, "pw")...)
		Expect(statusOf(err)).To(Equal(503), "alice: err = %v", err)
		_, err = remote.Head(testutil.Ref("private.kt.test/public/x:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred(), "anonymous")
	})

	It("answers an injected 401 with the host challenge", func() {
		srv, st, _ := newServer(withState(authState))
		addRule(st, api.Rule{ID: "r", Target: api.Target{Host: upstream}, Effects: api.Effects{Status: 401}})
		resp, _ := raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
		wantChallenge(resp, basicChallenge)
	})

	DescribeTable("applies rules before authorization on a granted repo",
		func(rule *api.Rule, hdr map[string]string, want int) {
			srv, st, _ := newServer(withState(authState))
			if rule != nil {
				addRule(st, *rule)
			}
			resp, body := raw(srv, http.MethodGet, privateFQDN, "/v2/team/app/manifests/t1", hdr)
			Expect(resp.StatusCode).To(Equal(want), "body %s", body)
		},
		Entry("answers an anonymous client 401 with no rule", nil, nil, 401),
		Entry("answers bob 403 with no rule", nil, basic("bob", "pw"), 403),
		Entry("answers alice 200 with no rule", nil, basic(alice, "pw"), 200),
		Entry("answers an anonymous client 503 with a 503 rule", &api.Rule{ID: "r", Target: api.Target{Host: private}, Effects: api.Effects{Status: 503}}, nil, 503),
		Entry("answers bob 503 with a 503 rule", &api.Rule{ID: "r", Target: api.Target{Host: private}, Effects: api.Effects{Status: 503}}, basic("bob", "pw"), 503),
		Entry("answers alice 503 with a 503 rule", &api.Rule{ID: "r", Target: api.Target{Host: private}, Effects: api.Effects{Status: 503}}, basic(alice, "pw"), 503),
		Entry("answers an anonymous client 401 with a pass rule", &api.Rule{ID: "r", Target: api.Target{Host: private}, Effects: api.Effects{Pass: true}}, nil, 401),
		Entry("answers bob 403 with a pass rule", &api.Rule{ID: "r", Target: api.Target{Host: private}, Effects: api.Effects{Pass: true}}, basic("bob", "pw"), 403),
		Entry("answers alice 200 with a pass rule", &api.Rule{ID: "r", Target: api.Target{Host: private}, Effects: api.Effects{Pass: true}}, basic(alice, "pw"), 200),
	)
})
