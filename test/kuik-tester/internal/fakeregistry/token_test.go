package fakeregistry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// tokenOf extracts access_token from a token response body.
func tokenOf(body []byte) string {
	GinkgoHelper()
	var v struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	Expect(json.Unmarshal(body, &v)).To(Succeed(), "token body %q", body)
	Expect(v.AccessToken).NotTo(BeEmpty(), "token body %q", body)
	Expect(v.AccessToken).To(Equal(v.Token), "token body %q", body)
	Expect(v.ExpiresIn).To(Equal(300), "token body %q", body)
	return v.AccessToken
}

var _ = Describe("Bearer authentication", func() {
	It("never reflects a quoted repo in the bearer challenge", func() {
		srv, _, _ := newServer(withState(bearerState))
		resp, _ := raw(srv, http.MethodGet, bearFQDN, "/v2/a%22,realm=%22x/manifests/t", nil)
		Expect(resp.StatusCode).To(Equal(404))
		Expect(resp.Header.Values("WWW-Authenticate")).To(BeEmpty())
	})

	It("issues a token to the user and serves the pull with it", func() {
		srv, _, log := newServer(withState(bearerState))
		_, err := remote.Image(testutil.Ref("bear.kt.test/team/app:t1"), authOpts(srv, alice, "pw")...)
		Expect(err).NotTo(HaveOccurred())
		e := waitEntry(log, func(e api.LogEntry) bool { return e.Kind == api.KindToken })
		Expect(e.User).To(Equal(alice), "token entry")
		Expect(e.Status).To(Equal(200), "token entry")
		m := waitEntry(log, func(e api.LogEntry) bool { return e.Kind == api.KindManifestByTag && e.Status == 200 })
		Expect(m.User).To(Equal(alice), "manifest user")
	})

	It("lets an anonymous client pull an ungranted repo with an anonymous token", func() {
		srv, _, log := newServer(withState(bearerState))
		_, err := remote.Image(testutil.Ref("bear.kt.test/public/x:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		e := waitEntry(log, func(e api.LogEntry) bool { return e.Kind == api.KindToken })
		Expect(e.User).To(BeEmpty())
	})

	It("refuses an anonymous client a granted repo", func() {
		srv, _, _ := newServer(withState(bearerState))
		_, err := remote.Head(testutil.Ref("bear.kt.test/team/app:t1"), testutil.Opts(srv)...)
		Expect(statusOf(err)).To(Equal(401), "err = %v", err)
	})

	It("challenges with a realm and, on a repo path, a scope", func() {
		srv, _, _ := newServer(withState(bearerState))
		resp, _ := raw(srv, http.MethodGet, bearFQDN, "/v2/", nil)
		wantChallenge(resp, bearChallenge)
		resp, _ = raw(srv, http.MethodGet, bearFQDN, "/v2/team/app/manifests/t1", nil)
		wantChallenge(resp, bearChallenge+`,scope="repository:team/app:pull"`)
	})

	It("builds the realm from X-Forwarded-Proto only behind a trusted proxy", func() {
		hdr := map[string]string{headerForwardProto: schemeHTTPS}
		srv, _, _ := newServer(withConfig(trustLoopback), withState(bearerState))
		resp, _ := raw(srv, http.MethodGet, bearFQDN, "/v2/", hdr)
		Expect(resp.Header.Get("WWW-Authenticate")).To(Equal(strings.Replace(bearChallenge, "http:", "https:", 1)), "trusted")
		srv2, _, _ := newServer(withState(bearerState))
		resp, _ = raw(srv2, http.MethodGet, bearFQDN, "/v2/", hdr)
		Expect(resp.Header.Get("WWW-Authenticate")).To(Equal(bearChallenge), "untrusted")
	})

	It("challenges an invalid token and logs it", func() {
		srv, _, log := newServer(withState(bearerState))
		resp, _ := raw(srv, http.MethodGet, bearFQDN, "/v2/team/app/manifests/t1", map[string]string{headerAuthorization: "Bearer nope"})
		wantChallenge(resp, bearChallenge+`,scope="repository:team/app:pull"`)
		waitEntry(log, func(e api.LogEntry) bool { return e.User == "(invalid token)" })
	})

	It("lets a rule fault the token endpoint", func() {
		srv, st, _ := newServer(withState(bearerState))
		addRule(st, api.Rule{ID: "r", Target: api.Target{Host: bear, Kinds: []api.Kind{api.KindToken}}, Effects: api.Effects{Status: 503}})
		_, err := remote.Image(testutil.Ref("bear.kt.test/team/app:t1"), authOpts(srv, alice, "pw")...)
		Expect(statusOf(err)).To(Equal(503), "err = %v", err)
	})

	DescribeTable("answers 404 to a token request on a basic host",
		func(method string) {
			srv, _, _ := newServer(withState(bearerState))
			resp, _ := raw(srv, method, upstreamFQDN, "/token", nil)
			Expect(resp.StatusCode).To(Equal(404), method)
		},
		Entry("GET", http.MethodGet),
		Entry("POST", http.MethodPost),
		Entry("PUT", http.MethodPut),
	)

	It("answers an injected 401 with the bearer challenge", func() {
		srv, st, _ := newServer(withState(bearerState))
		addRule(st, api.Rule{ID: "r", Target: api.Target{Host: bear}, Effects: api.Effects{Status: 401}})
		resp, _ := raw(srv, http.MethodGet, bearFQDN, "/v2/team/app/manifests/t1", nil)
		wantChallenge(resp, bearChallenge+`,scope="repository:team/app:pull"`)
	})

	It("serves parallel bearer pulls", func() {
		srv, _, _ := newServer(withState(bearerState))
		ref := testutil.Ref("bear.kt.test/team/app:t1")
		var wg sync.WaitGroup
		errs := make(chan error, 10)
		for range 10 {
			wg.Go(func() {
				_, err := remote.Image(ref, authOpts(srv, alice, "pw")...)
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("grants a token for a password grant and logs it", func() {
		srv, _, log := newServer(withState(bearerState))
		resp, body := rawForm(srv, bearFQDN, "/token",
			"grant_type=password&username=alice&password=pw&service=bear.kt.test&scope=repository%3Ateam%2Fapp%3Apull&client_id=containerd-client")
		Expect(resp.StatusCode).To(Equal(200), "type %q", resp.Header.Get("Content-Type"))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"), "status %d", resp.StatusCode)
		tok := tokenOf(body)
		resp, _ = raw(srv, http.MethodHead, bearFQDN, "/v2/team/app/manifests/t1", bearerHdr(tok))
		Expect(resp.StatusCode).To(Equal(200), "manifest status")
		waitEntry(log, func(e api.LogEntry) bool {
			return e.Kind == api.KindToken && e.Method == http.MethodPost && e.User == alice && e.Status == 200
		})
	})

	It("challenges a password grant with a wrong password and logs it", func() {
		srv, _, log := newServer(withState(bearerState))
		resp, _ := rawForm(srv, bearFQDN, "/token", "grant_type=password&username=alice&password=nope")
		wantChallenge(resp, bearChallenge)
		waitEntry(log, func(e api.LogEntry) bool {
			return e.Kind == api.KindToken && e.User == "alice (invalid)" && e.Status == 401
		})
	})

	DescribeTable("answers 400 to a token request that is not a password grant",
		func(form string) {
			srv, _, _ := newServer(withState(bearerState))
			resp, _ := rawForm(srv, bearFQDN, "/token", form)
			Expect(resp.StatusCode).To(Equal(400), "form %q", form)
		},
		Entry("refresh_token grant", "grant_type=refresh_token&refresh_token=x"),
		Entry("empty form", ""),
		Entry("client_credentials grant", "grant_type=client_credentials"),
	)

	It("answers 405 to a PUT on the token endpoint", func() {
		srv, _, _ := newServer(withState(bearerState))
		resp, _ := raw(srv, http.MethodPut, bearFQDN, "/token", nil)
		Expect(resp.StatusCode).To(Equal(405), "PUT")
	})

	It("requires a token on a bearer host, anonymous or not", func() {
		srv, _, _ := newServer(withState(bearerState))
		resp, _ := raw(srv, http.MethodHead, bearFQDN, "/v2/public/x/manifests/t1", nil)
		wantChallenge(resp, bearChallenge+`,scope="repository:public/x:pull"`)
		_, body := raw(srv, http.MethodGet, bearFQDN, "/token", nil)
		hdr := bearerHdr(tokenOf(body))
		resp, _ = raw(srv, http.MethodHead, bearFQDN, "/v2/public/x/manifests/t1", hdr)
		Expect(resp.StatusCode).To(Equal(200), "anonymous token, ungranted")
		resp, _ = raw(srv, http.MethodGet, bearFQDN, "/v2/", hdr)
		Expect(resp.StatusCode).To(Equal(200), "anonymous token, ping")
		resp, _ = raw(srv, http.MethodHead, bearFQDN, "/v2/team/app/manifests/t1", hdr)
		Expect(resp.StatusCode).To(Equal(401), "anonymous token, granted")
	})

	It("accepts Basic credentials on a bearer host", func() {
		srv, _, _ := newServer(withState(bearerState))
		resp, _ := raw(srv, http.MethodGet, bearFQDN, "/v2/team/app/manifests/t1", basic(alice, "pw"))
		Expect(resp.StatusCode).To(Equal(200))
	})

	It("refuses invalid Basic credentials on the token endpoint", func() {
		srv, _, _ := newServer(withState(bearerState))
		resp, _ := raw(srv, http.MethodGet, bearFQDN, "/token", basic(alice, "nope"))
		Expect(resp.StatusCode).To(Equal(401))
	})

	It("accepts a token for 300 seconds and challenges it after", func() {
		c := newFakeClock(time.Now())
		srv, _, _ := newServer(withClock(c), withState(bearerState))
		_, body := raw(srv, http.MethodGet, bearFQDN, "/token", basic(alice, "pw"))
		hdr := bearerHdr(tokenOf(body))
		c.Advance(299 * time.Second)
		resp, _ := raw(srv, http.MethodHead, bearFQDN, "/v2/team/app/manifests/t1", hdr)
		Expect(resp.StatusCode).To(Equal(200), "at 299s")
		c.Advance(time.Second)
		resp, _ = raw(srv, http.MethodHead, bearFQDN, "/v2/team/app/manifests/t1", hdr)
		wantChallenge(resp, bearChallenge+`,scope="repository:team/app:pull"`)
	})

	Describe("token store", func() {
		var (
			c   *fakeClock
			s   *Server
			srv *httptest.Server
		)
		issue := func() string {
			GinkgoHelper()
			resp, body := raw(srv, http.MethodGet, bearFQDN, "/token", nil)
			Expect(resp.StatusCode).To(Equal(200), "token status")
			return tokenOf(body)
		}
		BeforeEach(func() {
			c = newFakeClock(time.Now())
			srv, _, _ = newServer(withClock(c), withState(bearerState), withServer(func(x *Server) { s = x; x.maxTokens = 3 }))
		})

		It("drops expired tokens when it issues one after the sweep interval", func() {
			issue()
			issue()
			Expect(liveTokens(s)).To(Equal(2))
			c.Advance(301 * time.Second)
			issue()
			Expect(liveTokens(s)).To(Equal(1))
		})

		It("keeps an expired token until the next sweep is due", func() {
			issue()
			c.Advance(290 * time.Second)
			issue()
			c.Advance(15 * time.Second)
			issue()
			Expect(liveTokens(s)).To(Equal(3))
		})

		It("keeps a token issued right after a sweep", func() {
			issue()
			c.Advance(10 * time.Second)
			second := issue()
			c.Advance(290 * time.Second)
			issue()
			resp, _ := raw(srv, http.MethodGet, bearFQDN, "/v2/", bearerHdr(second))
			Expect(resp.StatusCode).To(Equal(200))
		})

		It("evicts a token rather than refusing one when the cap is reached", func() {
			for range 4 {
				issue()
			}
			Expect(liveTokens(s)).To(Equal(3))
		})
	})
})

var _ = Describe("Realm scheme", func() {
	DescribeTable("builds the realm scheme from X-Forwarded-Proto",
		func(hdr, want string) {
			cfg := testCfg()
			trustLoopback(cfg)
			r := httptest.NewRequest(http.MethodGet, "http://bear.kt.test/v2/", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			if hdr != "" {
				r.Header.Set(headerForwardProto, hdr)
			}
			w, _ := serveDirect(cfg, bearerState, r)
			Expect(w.Header().Get(headerChallenge)).To(HavePrefix(`Bearer realm="`+want+`://bear.kt.test/token"`), "header %q", hdr)
		},
		Entry("https", schemeHTTPS, schemeHTTPS),
		Entry("upper-case HTTPS", "HTTPS", schemeHTTPS),
		Entry("a list starting with https", " https , http", schemeHTTPS),
		Entry("http", schemeHTTP, schemeHTTP),
		Entry("an unknown scheme", "ftp", schemeHTTP),
		Entry("no header", "", schemeHTTP),
	)
})
