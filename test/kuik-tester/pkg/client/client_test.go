package client_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/client"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/client/clienttest"
)

const (
	hostUpstream = "upstream"
	repoFoo      = "acme/x/foo"
	userBob      = "bob"
	setT         = "kt-t"
	setX         = "kt-x"
	repoD        = "acme/d"
	destMirror   = "mirror"
	tagT1        = "t1"
	quotaQ       = "q"
	contentJSON  = "application/json"
	pathRules    = "/api/v1/rules"
)

// captured is the last request a capture server received.
type captured struct {
	mu     sync.Mutex
	method string
	path   string
	body   string
}

func (c *captured) get() (method, path, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method, c.path, c.body
}

// capture serves a fixed JSON answer and records the last request.
func capture(status int, answer string) (*httptest.Server, *captured) {
	GinkgoHelper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.method, got.path, got.body = r.Method, r.URL.Path, string(b)
		got.mu.Unlock()
		w.Header().Set("Content-Type", contentJSON)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	DeferCleanup(srv.Close)
	return srv, got
}

// newAdmin serves the real admin API in process, with a mirror proxy backed
// by an in-memory registry.
func newAdmin() *clienttest.Server {
	GinkgoHelper()
	srv := clienttest.NewServer("mirror")
	DeferCleanup(srv.Close)
	return srv
}

var _ = Describe("Client", func() {
	It("carries the set on the rules it creates", func() {
		srv := newAdmin()
		c := client.New(srv.URL, "").WithSet("kt-x")
		target := api.Target{Host: hostUpstream, Repo: repoFoo, Tag: "t-X"}
		r, err := c.CreateRule(ctx, api.Rule{Target: target, Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.ID).NotTo(BeEmpty())
		Expect(r.Enabled).To(BeTrue())
		Expect(r.Set).To(Equal("kt-x"))
		sets, err := c.ListSets(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(sets).To(HaveLen(1))
		Expect(sets[0].Name).To(Equal("kt-x"))
		Expect(sets[0].Objects).To(Equal(1))
		Expect(c.DeleteRule(ctx, r.ID)).To(Succeed())
		rs, _ := c.ListRules(ctx)
		Expect(rs).To(BeEmpty())
	})

	DescribeTable("sends the rule whole",
		func(clientSet string, update bool, in api.Rule, want string) {
			srv, got := capture(http.StatusOK, `{"rule":{},"warnings":[]}`)
			c := client.New(srv.URL, "").WithSet(clientSet)
			var err error
			if update {
				_, err = c.UpdateRule(ctx, in)
			} else {
				_, err = c.CreateRule(ctx, in)
			}
			Expect(err).NotTo(HaveOccurred())
			method, path, body := got.get()
			if update {
				Expect(method).To(Equal(http.MethodPut))
				Expect(path).To(Equal(pathRules + "/r1"))
			} else {
				Expect(method).To(Equal(http.MethodPost))
				Expect(path).To(Equal(pathRules))
			}
			Expect(body).To(MatchJSON(want))
		},
		Entry("create forces the client's set and enabled", setX, false,
			api.Rule{Set: "other", Target: api.Target{Host: "up"}, Effects: api.Effects{Status: 503}, Note: "n"},
			`{"id":"","set":"kt-x","enabled":true,"target":{"host":"up"},"effects":{"status":503},"note":"n"}`),
		Entry("create without a set sends none", "", false,
			api.Rule{Effects: api.Effects{Status: 503}},
			`{"id":"","enabled":true,"target":{},"effects":{"status":503}}`),
		Entry("update fills an empty set with the client's", setX, true,
			api.Rule{ID: "r1", Effects: api.Effects{Status: 503}},
			`{"id":"r1","set":"kt-x","enabled":false,"target":{},"effects":{"status":503}}`),
		Entry("update keeps the rule's own set", setX, true,
			api.Rule{ID: "r1", Set: "mine", Enabled: true},
			`{"id":"r1","set":"mine","enabled":true,"target":{},"effects":{}}`),
	)

	DescribeTable("tags what it creates with the set of the client",
		func(create func(ctx context.Context, base, c *client.Client) error) {
			srv := newAdmin()
			c := srv.Client.WithSet(setT)
			Expect(create(ctx, srv.Client, c)).To(Succeed())
			Expect(c.ListSets(ctx)).To(Equal([]api.SetInfo{{Name: setT, Objects: 1}}))
			Expect(c.DeleteSet(ctx, setT)).To(Succeed())
			Expect(c.ListSets(ctx)).To(BeEmpty())
			Expect(c.DeleteSet(ctx, setT)).To(Succeed())
		},
		Entry("CreateRule", func(ctx context.Context, _, c *client.Client) error {
			_, err := c.CreateRule(ctx, api.Rule{Effects: api.Effects{Status: 503}})
			return err
		}),
		Entry("UpdateRule", func(ctx context.Context, base, c *client.Client) error {
			r, err := base.CreateRule(ctx, api.Rule{Effects: api.Effects{Status: 503}})
			if err != nil {
				return err
			}
			r.Set = ""
			_, err = c.UpdateRule(ctx, r)
			return err
		}),
		Entry("PutUser", func(ctx context.Context, _, c *client.Client) error {
			return c.PutUser(ctx, userBob, "pw")
		}),
		Entry("CreateGrant", func(ctx context.Context, base, c *client.Client) error {
			if err := base.PutUser(ctx, userBob, "pw"); err != nil {
				return err
			}
			_, err := c.CreateGrant(ctx, api.Grant{Host: hostUpstream, Repo: "acme/**",
				Actions: []api.Action{api.ActionPull}, Users: []string{userBob}})
			return err
		}),
		Entry("CreateAlias", func(ctx context.Context, _, c *client.Client) error {
			_, err := c.CreateAlias(ctx, api.Alias{Host: hostUpstream, From: "m/x", To: "acme/x"})
			return err
		}),
		Entry("CreateDivergent", func(ctx context.Context, _, c *client.Client) error {
			_, err := c.CreateDivergent(ctx, api.Divergent{Host: hostUpstream, Repo: repoFoo, Salt: "s"})
			return err
		}),
		Entry("PutQuota", func(ctx context.Context, _, c *client.Client) error {
			_, err := c.PutQuota(ctx, api.QuotaDef{Name: quotaQ, Limit: 1, WindowSec: 60, Key: api.QuotaKeyGlobal})
			return err
		}),
		Entry("CreatePlatform", func(ctx context.Context, _, c *client.Client) error {
			_, err := c.CreatePlatform(ctx,
				api.PlatformRule{Host: hostUpstream, Repo: repoFoo, Platforms: []string{"linux/amd64"}})
			return err
		}),
		Entry("ImportSet", func(ctx context.Context, _, c *client.Client) error {
			return c.ImportSet(ctx, setT, []byte("aliases:\n- host: upstream\n  from: m/y\n  to: acme/y\n"))
		}),
	)

	It("treats the deletion of an unknown set as done", func() {
		srv := newAdmin()
		Expect(srv.Client.DeleteSet(ctx, "kt-none")).To(Succeed())
	})

	It("reports any other error of a set deletion", func() {
		err := client.New("http://127.0.0.1:1", "").DeleteSet(ctx, setT)
		Expect(err).To(HaveOccurred())
		var ae *client.APIError
		Expect(errors.As(err, &ae)).To(BeFalse())
	})

	It("deletes users and grants", func() {
		srv := newAdmin()
		c := srv.Client
		Expect(c.PutUser(ctx, userBob, "pw")).To(Succeed())
		g, err := c.CreateGrant(ctx, api.Grant{Host: hostUpstream, Repo: "acme/**",
			Actions: []api.Action{api.ActionPull}, Users: []string{userBob}})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.DeleteGrant(ctx, g.ID)).To(Succeed())
		Expect(c.DeleteUser(ctx, userBob)).To(Succeed())
		var ae *client.APIError
		Expect(errors.As(c.DeleteUser(ctx, userBob), &ae)).To(BeTrue())
		Expect(ae.Status).To(Equal(http.StatusNotFound))
	})

	It("deletes a platform rule, and the tag serves every platform again", func() {
		srv := newAdmin()
		c := srv.Client
		full, err := c.Resolve(ctx, hostUpstream, repoFoo, tagT1)
		Expect(err).NotTo(HaveOccurred())
		p, err := c.CreatePlatform(ctx, api.PlatformRule{Host: hostUpstream, Repo: repoFoo, Platforms: []string{"linux/amd64"}})
		Expect(err).NotTo(HaveOccurred())
		narrowed, err := c.Resolve(ctx, hostUpstream, repoFoo, tagT1)
		Expect(err).NotTo(HaveOccurred())
		Expect(narrowed.Platforms).To(HaveLen(1))
		Expect(narrowed.IndexDigest).NotTo(Equal(full.IndexDigest))
		Expect(c.DeletePlatform(ctx, p.ID)).To(Succeed())
		again, err := c.Resolve(ctx, hostUpstream, repoFoo, tagT1)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.IndexDigest).To(Equal(full.IndexDigest))
		var ae *client.APIError
		Expect(errors.As(c.DeletePlatform(ctx, p.ID), &ae)).To(BeTrue())
		Expect(ae.Status).To(Equal(http.StatusNotFound))
	})

	It("reads and resets quota usage", func() {
		srv := newAdmin()
		c := srv.Client
		_, err := c.PutQuota(ctx, api.QuotaDef{Name: quotaQ, Limit: 1, WindowSec: 60, Key: api.QuotaKeyGlobal})
		Expect(err).NotTo(HaveOccurred())
		usage, err := c.QuotaUsage(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(usage).NotTo(HaveKey(quotaQ))
		Expect(c.ResetQuota(ctx, quotaQ)).To(Succeed())
		var ae *client.APIError
		Expect(errors.As(c.ResetQuota(ctx, "nope"), &ae)).To(BeTrue())
		Expect(ae.Status).To(Equal(http.StatusNotFound))
	})

	It("reads the quota counters the admin reports", func() {
		srv, got := capture(http.StatusOK, `{"q":{"global":1}}`)
		usage, err := client.New(srv.URL, "").QuotaUsage(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(usage).To(HaveKeyWithValue(quotaQ, HaveKeyWithValue("global", 1)))
		method, path, _ := got.get()
		Expect(method).To(Equal(http.MethodGet))
		Expect(path).To(Equal("/api/v1/quotas/usage"))
	})

	It("drifts a resolved image", func() {
		srv := newAdmin()
		c := srv.Client
		first, err := c.Resolve(ctx, hostUpstream, repoFoo, tagT1)
		Expect(err).NotTo(HaveOccurred())
		changed, err := c.Drift(ctx, api.Target{Host: hostUpstream, Repo: repoFoo, Tag: tagT1})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(Equal([]string{"upstream/acme/x/foo:t1"}))
		second, err := c.Resolve(ctx, hostUpstream, repoFoo, tagT1)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Generation).To(Equal(2))
		Expect(second.IndexDigest).NotTo(Equal(first.IndexDigest))
	})

	It("returns the quota as the admin stored it", func() {
		srv := newAdmin()
		c := srv.Client.WithSet(setT)
		q, err := c.PutQuota(ctx,
			api.QuotaDef{Name: quotaQ, Limit: 1, WindowSec: 60, Key: api.QuotaKeyGlobal, Methods: []string{"get"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(q.Methods).To(Equal([]string{"GET"}))
		Expect(q.Set).To(Equal(setT))
	})

	It("lists the destinations", func() {
		srv := newAdmin()
		Expect(srv.Client.ListDestinations(ctx)).To(Equal([]string{destMirror}))
	})

	It("tags, deletes a tag and deletes a manifest on a destination", func() {
		srv := newAdmin()
		c := srv.Client
		tags := func() map[string]string {
			GinkgoHelper()
			inv, err := c.InventoryPrefix(ctx, destMirror, repoD)
			Expect(err).NotTo(HaveOccurred())
			out := map[string]string{}
			for _, r := range inv {
				for _, t := range r.Tags {
					out[t.Tag] = t.Digest
				}
			}
			return out
		}
		Expect(c.Push(ctx, destMirror, repoD, tagT1)).To(Succeed())
		Expect(c.Tag(ctx, destMirror, repoD, tagT1, "t2")).To(Succeed())
		got := tags()
		Expect(got).To(HaveLen(2))
		Expect(got).To(HaveKey(tagT1))
		Expect(got).To(HaveKey("t2"))
		Expect(got[tagT1]).To(Equal(got["t2"]))
		Expect(c.DeleteTag(ctx, destMirror, repoD, tagT1)).To(Succeed())
		after := tags()
		Expect(after).To(HaveLen(1))
		Expect(after).To(HaveKey("t2"))
		Expect(c.DeleteManifest(ctx, destMirror, repoD, got["t2"])).To(Succeed())
		Expect(tags()).To(BeEmpty())
	})

	It("purges everything, the backends only on request", func() {
		srv := newAdmin()
		c := srv.Client
		_, err := c.CreateRule(ctx, api.Rule{Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		srv.AddRequest(api.LogEntry{Host: hostUpstream, Kind: api.KindBlob, Method: http.MethodGet, Status: 200})
		Expect(c.Push(ctx, destMirror, repoD, tagT1)).To(Succeed())

		res, err := c.PurgeEverything(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Rules).To(Equal(1))
		Expect(res.Requests).To(Equal(1))
		Expect(res.Backends).To(BeEmpty())
		Expect(c.ListRules(ctx)).To(BeEmpty())
		Expect(c.ListStats(ctx)).To(BeEmpty())
		Expect(c.InventoryPrefix(ctx, destMirror, repoD)).To(HaveLen(1))

		res, err = c.PurgeEverything(ctx, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Backends).To(HaveLen(1))
		Expect(res.Backends[0].DeletedTags).To(Equal(1))
		Expect(res.Backends[0].Repositories).To(ConsistOf(repoD))
	})

	It("purges a list, the images, the log and a destination", func() {
		srv := newAdmin()
		c := srv.Client
		_, err := c.CreateRule(ctx, api.Rule{Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		_, err = c.CreateRule(ctx, api.Rule{Effects: api.Effects{Status: 429}})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.PurgeColl(ctx, "rules")).To(Equal(2))
		Expect(c.ListRules(ctx)).To(BeEmpty())
		_, err = c.PurgeColl(ctx, "nope")
		Expect(err).To(HaveOccurred())

		_, err = c.Resolve(ctx, hostUpstream, repoFoo, tagT1)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.PurgeImages(ctx)).To(Equal(1))

		srv.AddRequest(api.LogEntry{Host: hostUpstream, Kind: api.KindBlob, Method: http.MethodGet, Status: 200})
		Expect(c.PurgeLog(ctx)).To(Equal(1))
		Expect(c.ListStats(ctx)).To(HaveLen(1))

		Expect(c.Push(ctx, destMirror, repoD, tagT1)).To(Succeed())
		res, err := c.PurgeAll(ctx, destMirror)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.DeletedTags).To(Equal(1))
		Expect(res.Repositories).To(ConsistOf(repoD))
		_, err = c.PurgeAll(ctx, "nope")
		Expect(err).To(HaveOccurred())
	})

	It("lists and resets the request counters", func() {
		srv := newAdmin()
		srv.AddRequest(api.LogEntry{Host: hostUpstream, Kind: api.KindBlob, Method: http.MethodGet, Status: 200})
		stats, err := srv.Client.ListStats(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(stats).To(HaveLen(1))
		Expect(stats[0].Count).To(BeEquivalentTo(1))
		Expect(srv.Client.ResetStats(ctx)).To(Succeed())
		Expect(srv.Client.ListStats(ctx)).To(BeEmpty())
	})

	It("reports the set it tags objects with", func() {
		srv := newAdmin()
		Expect(srv.Client.Set()).To(BeEmpty())
		Expect(srv.Client.WithSet(setT).Set()).To(Equal(setT))
		Expect(srv.Client.Set()).To(BeEmpty())
	})

	DescribeTable("maps an answer to an error",
		func(status int, contentType, body string, want *client.APIError) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if contentType != "" {
					w.Header().Set("Content-Type", contentType)
				}
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte(body))
				}
			}))
			DeferCleanup(srv.Close)
			_, err := client.New(srv.URL, "").ListRules(ctx)
			if want == nil {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			var ae *client.APIError
			Expect(errors.As(err, &ae)).To(BeTrue(), "err = %v", err)
			Expect(*ae).To(Equal(*want))
		},
		Entry("a JSON error", 404, contentJSON, `{"error":"rule \"x\": not found"}`,
			&client.APIError{Status: 404, Msg: `rule "x": not found`}),
		Entry("a text error", 502, "text/plain", "bad gateway\n",
			&client.APIError{Status: 502, Msg: "bad gateway"}),
		Entry("a JSON error without the error key", 400, contentJSON, `{"message":"m"}`,
			&client.APIError{Status: 400, Msg: `{"message":"m"}`}),
		Entry("an empty 200", 200, contentJSON, "", nil),
		Entry("a 204", 204, "", "", nil),
	)

	It("fails on a 2xx answer that is not JSON", func() {
		srv, _ := capture(http.StatusOK, "{")
		_, err := client.New(srv.URL, "").ListRules(ctx)
		Expect(err).To(HaveOccurred())
		var ae *client.APIError
		Expect(errors.As(err, &ae)).To(BeFalse())
	})

	It("renders the status and the message of the admin", func() {
		Expect((&client.APIError{Status: 404, Msg: "m"}).Error()).To(Equal("kuik-tester admin: 404 m"))
	})

	It("ends a call waiting for its answer when the context expires", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		DeferCleanup(srv.Close)
		dctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		DeferCleanup(cancel)
		start := time.Now()
		_, err := client.New(srv.URL, "").ListRules(dctx)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})

	Describe("Log", func() {
		It("filters the entries", func() {
			srv := newAdmin()
			now := time.Now()
			for i, e := range []api.LogEntry{
				{Host: hostUpstream, Repo: repoFoo, Method: http.MethodHead, User: userBob,
					Kind: api.KindManifestByTag, Status: 200},
				{Host: hostUpstream, Repo: "acme/x/bar/baz", Method: http.MethodGet, User: "alice",
					Kind: api.KindManifestByTag, Status: 200},
				{Host: hostUpstream, Repo: "acme/y/foo", Method: http.MethodGet, User: userBob,
					Kind: api.KindManifestByTag, Status: 503},
				{Host: "alt1", Repo: repoFoo, Method: http.MethodGet, Kind: api.KindBlob, Status: 200, Digest: "sha256:abc"},
			} {
				e.Time = now.Add(time.Duration(i) * time.Millisecond)
				srv.AddRequest(e)
			}
			c := client.New(srv.URL, "")
			got, err := c.Log(ctx, client.LogFilter{Host: hostUpstream, RepoGlob: "acme/x/**"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(2), "RepoGlob")
			got, _ = c.Log(ctx, client.LogFilter{User: userBob})
			Expect(got).To(HaveLen(2), "User")
			got, _ = c.Log(ctx, client.LogFilter{Method: http.MethodHead})
			Expect(got).To(HaveLen(1), "Method")
			Expect(got[0].Repo).To(Equal(repoFoo), "Method")
			st := 503
			got, _ = c.Log(ctx, client.LogFilter{Status: &st})
			Expect(got).To(HaveLen(1), "Status")
			got, _ = c.Log(ctx, client.LogFilter{Digest: "sha256:abc"})
			Expect(got).To(HaveLen(1), "Digest")
			Expect(got[0].Host).To(Equal("alt1"), "Digest")
			got, _ = c.Log(ctx, client.LogFilter{Since: now.Add(2 * time.Millisecond)})
			Expect(got).To(HaveLen(2), "Since")
		})
	})

	Describe("Purge and InventoryPrefix", func() {
		It("only touch the repositories under the prefix", func() {
			srv := newAdmin()
			c := client.New(srv.URL, "")
			for _, r := range []string{"mir02/a", "mir02/b", "mir020/x"} {
				Expect(c.Push(ctx, "mirror", r, "t1")).To(Succeed())
			}
			inv, err := c.InventoryPrefix(ctx, "mirror", "mir02")
			Expect(err).NotTo(HaveOccurred())
			Expect(inv).To(HaveLen(2))
			Expect(inv[0].Name).To(Equal("mir02/a"))
			Expect(inv[1].Name).To(Equal("mir02/b"))
			res, err := c.Purge(ctx, "mirror", "mir02")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedTags).To(Equal(2))
			_, err = c.Resolve(ctx, hostUpstream, repoFoo, "t1")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	It("sends a Basic Authorization header and no Origin", func() {
		var auth, origin string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			origin = r.Header.Get("Origin")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		}))
		DeferCleanup(srv.Close)
		c := client.New(srv.URL, "u:p")
		_, err := c.ListRules(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(auth).To(HavePrefix("Basic "))
		Expect(origin).To(BeEmpty())
	})

	It("returns an APIError carrying the status and message", func() {
		srv := newAdmin()
		c := client.New(srv.URL, "")
		_, err := c.CreateRule(ctx, api.Rule{Effects: api.Effects{Status: 42}})
		var ae *client.APIError
		Expect(errors.As(err, &ae)).To(BeTrue(), "err = %v, want an APIError about the status", err)
		Expect(ae.Status).To(Equal(400))
		Expect(ae.Msg).To(ContainSubstring("status"))
	})

	// The admin URL may carry userinfo: no part of it reaches an error.
	It("never carries the userinfo of the admin URL in its errors", func() {
		c := client.New("http://alice:s3cret@127.0.0.1:1", "")
		_, err := c.ListSets(ctx)
		Expect(err).To(HaveOccurred(), "want a connection error")
		for _, leak := range []string{"alice", "s3cret"} {
			Expect(err.Error()).NotTo(ContainSubstring(leak))
		}
		Expect(err.Error()).To(ContainSubstring("127.0.0.1:1"), "the error lost the host")
	})
})

var _ = Describe("MatchRepo", func() {
	DescribeTable("matching a repository against a pattern",
		func(pattern, repo string, want bool) {
			Expect(client.MatchRepo(pattern, repo)).To(Equal(want))
		},
		Entry("an empty pattern", "", "acme/x", true),
		Entry("a double star on none", "acme/**", "acme", true),
		Entry("a double star on several segments", "acme/**", "acme/x/y", true),
		Entry("a single star on two segments", "acme/*", "acme/x/y", false),
		Entry("a star with a prefix", "acme/x*", "acme/xy", true),
	)
})
