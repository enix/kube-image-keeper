package fakeregistry

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const anon = "anon"

var _ = Describe("Quotas", func() {
	It("counts anonymous manifest requests, answers 429 over the limit and restarts after a reset", func() {
		q := quota.New()
		srv, _, _ := newServer(withQuotas(q), withState(func(s *state.State) {
			s.Users = []api.User{{Name: alice, Password: "pw"}}
			s.Quotas = []api.QuotaDef{{Name: anon, Limit: 2, WindowSec: 60, Key: "ip"}}
			s.Rules = []api.Rule{{ID: "q", Enabled: true,
				Target:  api.Target{Caller: "anonymous"},
				Effects: api.Effects{Quota: anon}}}
		}))
		step := func(method string, hdr map[string]string, status int, remaining string) []byte {
			GinkgoHelper()
			resp, body := raw(srv, method, upstreamFQDN, manifest1, hdr)
			Expect(resp.StatusCode).To(Equal(status), method)
			Expect(resp.Header.Get(headerRemaining)).To(Equal(remaining), "%s RateLimit-Remaining", method)
			if remaining != "" {
				Expect(resp.Header.Get("RateLimit-Limit")).To(Equal("2;w=60"))
			}
			return body
		}
		step(http.MethodHead, nil, 200, "2;w=60")
		step(http.MethodGet, nil, 200, "1;w=60")
		step(http.MethodGet, nil, 200, "0;w=60")
		body := step(http.MethodGet, nil, 429, "0;w=60")
		Expect(errorCode(body)).To(Equal("TOOMANYREQUESTS"))
		step(http.MethodHead, nil, 200, "0;w=60")
		step(http.MethodGet, basic(alice, "pw"), 200, "")
		q.Reset(anon)
		step(http.MethodGet, nil, 200, "1;w=60")
	})

	It("refills a quota once its window has elapsed", func() {
		c := newFakeClock(time.Now())
		srv, _, _ := newServer(withClock(c), withState(func(s *state.State) {
			s.Quotas = []api.QuotaDef{{Name: "g", Limit: 1, WindowSec: 60}}
			s.Rules = []api.Rule{{ID: "q", Enabled: true, Effects: api.Effects{Quota: "g"}}}
		}))
		get := func(status int) {
			GinkgoHelper()
			resp, _ := raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
			Expect(resp.StatusCode).To(Equal(status))
			if status == 200 {
				Expect(resp.Header.Get(headerRemaining)).To(Equal("0;w=60"))
			}
		}
		get(200)
		get(429)
		c.Advance(60 * time.Second)
		get(200)
	})

	It("does not count blob requests", func() {
		q := quota.New()
		srv, _, _ := newServer(withQuotas(q), withState(func(s *state.State) {
			s.Quotas = []api.QuotaDef{{Name: anon, Limit: 2, WindowSec: 60, Key: "ip"}}
			s.Rules = []api.Rule{{ID: "q", Enabled: true, Effects: api.Effects{Quota: anon}}}
		}))
		resp, body := raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/blobs/sha256:"+
			"0000000000000000000000000000000000000000000000000000000000000000", nil)
		Expect(resp.StatusCode).To(Equal(404), "body %s", body)
		Expect(string(body)).To(ContainSubstring("BLOB_UNKNOWN"))
		Expect(resp.Header.Get("RateLimit-Limit")).To(BeEmpty(), "headers %v", resp.Header)
		Expect(resp.Header.Get(headerRemaining)).To(BeEmpty(), "headers %v", resp.Header)
		Expect(q.Snapshot(time.Now())).To(BeEmpty())
	})

	It("does not count unauthorized requests", func() {
		srv, _, _ := newServer(withState(func(s *state.State) {
			s.Users = []api.User{{Name: alice, Password: "pw"}}
			s.Grants = []api.Grant{{ID: "g", Host: upstream, Repo: teamRepo,
				Actions: []api.Action{api.ActionPull}, Users: []string{alice}}}
			s.Quotas = []api.QuotaDef{{Name: "g", Limit: 2, WindowSec: 60, Key: "global"}}
			s.Rules = []api.Rule{{ID: "r", Enabled: true, Effects: api.Effects{Quota: "g"}}}
		}))
		const path = "/v2/team/app/manifests/t1"
		for range 3 {
			resp, _ := raw(srv, http.MethodGet, upstreamFQDN, path, nil)
			Expect(resp.StatusCode).To(Equal(401), "anonymous")
		}
		resp, _ := raw(srv, http.MethodGet, upstreamFQDN, path, basic(alice, "pw"))
		Expect(resp.StatusCode).To(Equal(200), "alice")
		Expect(resp.Header.Get(headerRemaining)).To(Equal("1;w=60"), "alice")
	})

	It("does not count requests denied with 403", func() {
		srv, _, _ := newServer(withState(func(s *state.State) {
			s.Users = []api.User{{Name: alice, Password: "pw"}, {Name: "bob", Password: "pw"}}
			s.Grants = []api.Grant{{ID: "g", Host: upstream, Repo: teamRepo,
				Actions: []api.Action{api.ActionPull}, Users: []string{alice}}}
			s.Quotas = []api.QuotaDef{{Name: "g", Limit: 2, WindowSec: 60, Key: "global"}}
			s.Rules = []api.Rule{{ID: "r", Enabled: true, Effects: api.Effects{Quota: "g"}}}
		}))
		const path = "/v2/team/app/manifests/t1"
		for range 3 {
			resp, _ := raw(srv, http.MethodGet, upstreamFQDN, path, basic("bob", "pw"))
			Expect(resp.StatusCode).To(Equal(403), "bob")
		}
		resp, _ := raw(srv, http.MethodGet, upstreamFQDN, path, basic(alice, "pw"))
		Expect(resp.StatusCode).To(Equal(200), "alice")
		Expect(resp.Header.Get(headerRemaining)).To(Equal("1;w=60"), "alice")
	})

	It("ignores a rule naming an unknown quota", func() {
		srv, _, _ := newServer(withState(func(s *state.State) {
			s.Rules = []api.Rule{{ID: "r", Enabled: true, Effects: api.Effects{Quota: "nope"}}}
		}))
		resp, _ := raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
		Expect(resp.StatusCode).To(Equal(200), "headers %v", resp.Header)
		Expect(resp.Header.Get("RateLimit-Limit")).To(BeEmpty(), "headers %v", resp.Header)
	})
})
