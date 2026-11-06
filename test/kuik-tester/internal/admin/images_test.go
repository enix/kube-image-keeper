package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/fakeregistry"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

func setRules(st *state.Store, rs ...api.Rule) {
	GinkgoHelper()
	Expect(st.Update(func(s *state.State) error { s.Rules = rs; return nil })).To(Succeed())
}

func exactRules(s *Service, host, repo, tag string) int {
	n := 0
	for _, r := range s.ListRules() {
		if isExact(r.Target, host, repo, tag) {
			n++
		}
	}
	return n
}

const (
	imgFooT2 = "upstream/acme/foo:t2"
	imgZedT1 = "upstream/acme/zed:t1"
)

var _ = Describe("Images", func() {
	Describe("ImageAction", func() {
		It("removes only the exact rule on ok and adds an exact pass on exempt", func() {
			s, st, _ := newService(nil)
			setRules(st,
				api.Rule{ID: "exact", Enabled: true, Target: api.Target{Host: upstreamHost, Repo: repoFoo, Tag: "t1"},
					Effects: api.Effects{Headers: map[string]string{"RateLimit-Remaining": "0;w=21600"}}},
				api.Rule{ID: "broad", Enabled: true, Target: api.Target{Host: upstreamHost, Repo: "**"}, Effects: api.Effects{Status: 503}},
			)
			row, err := s.ImageAction(upstreamHost, repoFoo, "t1", "ok")
			Expect(err).NotTo(HaveOccurred())
			Expect(row.Behaviour).To(Equal("503"), "row = %+v, want the broad 503 rule", row)
			Expect(row.Exact).To(BeFalse(), "row = %+v, want the broad 503 rule", row)
			Expect(row.RuleID).To(Equal("broad"), "row = %+v, want the broad 503 rule", row)
			Expect(s.ListRules()).To(HaveLen(1), "rules, want only the broad rule")
			row, err = s.ImageAction(upstreamHost, repoFoo, "t1", "exempt")
			Expect(err).NotTo(HaveOccurred())
			Expect(row.Behaviour).To(Equal("pass"), "row = %+v, want an exact pass", row)
			Expect(row.Exact).To(BeTrue(), "row = %+v, want an exact pass", row)
		})

		It("replaces the exact rule of an image and serves no digest for a proxy host", func() {
			s, _, _ := newService(nil)
			_, err := s.ImageAction(upstreamHost, repoFoo, "t1", "503")
			Expect(err).NotTo(HaveOccurred())
			row, err := s.ImageAction(upstreamHost, repoFoo, "t1", "429")
			Expect(err).NotTo(HaveOccurred())
			Expect(row.Behaviour).To(Equal("429"), "row = %+v, rules = %+v", row, s.ListRules())
			Expect(exactRules(s, upstreamHost, repoFoo, "t1")).To(Equal(1), "row = %+v, rules = %+v", row, s.ListRules())
			Expect(s.ListRules()).To(HaveLen(1), "row = %+v, rules = %+v", row, s.ListRules())
			row, err = s.ImageAction(mirrorHost, repoFoo, "t1", "503")
			Expect(err).NotTo(HaveOccurred(), "proxy host row = %+v", row)
			Expect(row.IndexDigest).To(BeEmpty(), "proxy host row = %+v", row)
			Expect(row.Pinned).To(BeEmpty(), "proxy host row = %+v", row)
		})

		DescribeTable("replaces the exact rule with the requested behaviour",
			func(action, want string) {
				s, _, _ := newService(nil)
				_, err := s.ImageAction(upstreamHost, repoFoo, "t1", "429")
				Expect(err).NotTo(HaveOccurred())
				row, err := s.ImageAction(upstreamHost, repoFoo, "t1", action)
				Expect(err).NotTo(HaveOccurred(), action)
				Expect(row.Behaviour).To(Equal(want), action)
			},
			Entry("ratelimit0", "ratelimit0", "RateLimit-Remaining: 0;w=21600"),
			Entry("latency", "latency", "latency 3000ms"),
			Entry("reset", "reset", "reset"),
			Entry("404", "404", "404"),
		)

		DescribeTable("checks the host label like the registry through ImageAction",
			func(host string, valid bool) {
				s, _, _ := newService(nil)
				_, err := s.ImageAction(host, repoFoo, "t1", "404")
				if valid {
					Expect(err).NotTo(HaveOccurred())
					return
				}
				wantInvalid(err)
			},
			Entry("63 bytes", strings.Repeat("a", 63), true),
			Entry("64 bytes", strings.Repeat("a", 64), false),
		)

		DescribeTable("refuses bad input",
			func(host, repo, tag, action string) {
				s, _, _ := newService(nil)
				wantInvalid(errOf(s.ImageAction(host, repo, tag, action)))
			},
			Entry("an unknown action", upstreamHost, repoFoo, "t1", "explode"),
			Entry("a host with an upper-case letter", "Up.stream", repoFoo, "t1", "ok"),
			Entry("a glob repo", upstreamHost, repoGlob, "t1", "ok"),
			Entry("a bad tag", upstreamHost, repoFoo, "-bad", "ok"),
		)

		It("drifts an image and refuses a proxy host", func() {
			s, _, _ := newService(nil)
			rows, _ := s.Images(ImageFilter{})
			Expect(rows).To(BeEmpty(), "expected no image")
			r1, err := s.Resolve(upstreamHost, repoFoo, "t1")
			Expect(err).NotTo(HaveOccurred())
			row, err := s.ImageAction(upstreamHost, repoFoo, "t1", "drift")
			Expect(err).NotTo(HaveOccurred())
			Expect(row.Generation).To(Equal(2), "row = %+v, previous digest %s", row, r1.IndexDigest)
			Expect(row.IndexDigest).NotTo(BeEmpty(), "row = %+v, previous digest %s", row, r1.IndexDigest)
			Expect(row.IndexDigest).NotTo(Equal(r1.IndexDigest), "row = %+v, previous digest %s", row, r1.IndexDigest)
			wantInvalid(errOf(s.ImageAction(mirrorHost, repoFoo, "t1", "drift")))
			wantInvalid(errOf(s.Drift(api.Target{Host: mirrorHost, Repo: repoFoo, Tag: "t1"})))
			wantInvalid(errOf(s.Drift(api.Target{Host: "up["})))
			got, err := s.Drift(api.Target{Host: "nothing", Repo: "**"})
			Expect(err).NotTo(HaveOccurred(), "Drift(no match)")
			Expect(got).NotTo(BeNil(), "Drift(no match), want an empty non-nil slice")
			Expect(got).To(BeEmpty(), "Drift(no match), want an empty non-nil slice")
		})

		It("keeps at most one exact rule under concurrent actions and pulls", func() {
			s, st, q := newService(nil)
			srv := httptest.NewServer(fakeregistry.New(s.cfg, s.proxies, st, reqlog.New(100), q).Handler())
			DeferCleanup(srv.Close)
			client := &http.Client{Transport: testutil.Transport(srv)}

			var wg sync.WaitGroup
			for i := range 20 {
				wg.Add(2)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					for range 10 {
						action := "503"
						if i%2 == 1 {
							action = "ok"
						}
						_, err := s.ImageAction(upstreamHost, repoFoo, "t1", action)
						Expect(err).NotTo(HaveOccurred())
						_, _ = s.Images(ImageFilter{})
					}
				}()
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					for range 10 {
						req, _ := http.NewRequest(http.MethodHead, "http://upstream.kt.test/v2/acme/foo/manifests/t1", nil)
						req.Host = "upstream.kt.test"
						resp, err := client.Do(req)
						Expect(err).NotTo(HaveOccurred())
						_ = resp.Body.Close()
						Expect(resp.StatusCode).To(BeElementOf(200, 503), "HEAD status")
					}
				}()
			}
			wg.Wait()
			Expect(exactRules(s, upstreamHost, repoFoo, "t1")).To(BeNumerically("<=", 1), "exact rules, want at most 1")
		})
	})

	Describe("behaviour", func() {
		It("lists the effects in a fixed order", func() {
			r := &api.Rule{Effects: api.Effects{
				Headers: map[string]string{"B": "2", "A": "1"}, LatencyMS: 5, Reset: true, Status: 503, Quota: "q", Pass: true,
			}}
			Expect(behaviour(r)).To(Equal("A: 1, B: 2, latency 5ms, reset, 503, quota q, pass"))
			Expect(behaviour(nil)).To(Equal("ok"), "nil rule")
		})
	})

	Describe("Service.Images", func() {
		It("accepts every action of the table", func() {
			s, _, _ := newService(nil)
			_, err := s.Resolve(upstreamHost, repoFoo, "t1")
			Expect(err).NotTo(HaveOccurred())
			for _, a := range imageActions {
				_, err := s.ImageAction(upstreamHost, repoFoo, "t1", a.Value)
				Expect(err).NotTo(HaveOccurred(), "action %q", a.Value)
				if a.Effects != nil {
					rs := s.ListRules()
					Expect(rs).NotTo(BeEmpty(), "action %q inserted no rule", a.Value)
					Expect(rs[0].Effects).To(Equal(*a.Effects), "action %q", a.Value)
				}
			}
		})

		DescribeTable("filters, orders and caps Images before building rows",
			func(f ImageFilter, want []string, wantTotal int) {
				s, st, _ := newService(nil)
				Expect(st.Update(func(x *state.State) error {
					for _, k := range [][3]string{{"alt1", repoFoo, "t1"}, {upstreamHost, "acme/zed", "t1"}, {upstreamHost, repoFoo, "t2"}} {
						x.Seen[state.RefKey(k[0], k[1], k[2])] = time.Now()
					}
					return nil
				})).To(Succeed())
				rows, total := s.Images(f)
				got := make([]string, 0, len(rows))
				for _, r := range rows {
					got = append(got, r.Host+"/"+r.Repo+":"+r.Tag)
				}
				Expect(got).To(Equal(want))
				Expect(total).To(Equal(wantTotal))
			},
			Entry("no filter lists every image in order", ImageFilter{},
				[]string{uiAltRef, imgFooT2, imgZedT1}, 3),
			Entry("a host substring keeps its images", ImageFilter{Host: "stream"},
				[]string{imgFooT2, imgZedT1}, 2),
			Entry("a repo substring keeps its images", ImageFilter{Repo: "zed"},
				[]string{imgZedT1}, 1),
			Entry("a limit cuts after counting", ImageFilter{Limit: 2},
				[]string{uiAltRef, imgFooT2}, 3),
			Entry("a zero limit builds every row", ImageFilter{Limit: 0},
				[]string{uiAltRef, imgFooT2, imgZedT1}, 3),
		)

		It("lists the seen, drifted and ruled images", func() {
			s, st, _ := newService(nil)
			Expect(st.Update(func(x *state.State) error {
				x.Seen[state.RefKey(upstreamHost, "acme/seen", "t1")] = time.Now()
				return nil
			})).To(Succeed())
			_, err := s.Drift(api.Target{Host: upstreamHost, Repo: "acme/drifted", Tag: "t1"})
			Expect(err).NotTo(HaveOccurred())
			_, err = s.CreateRule(api.Rule{Enabled: true, Target: api.Target{Host: mirrorHost, Repo: "acme/ruled", Tag: "t1"}, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
			_, err = s.CreateRule(api.Rule{Enabled: true, Target: api.Target{Host: upstreamHost, Repo: "acme/glob", Tag: "*"}, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
			rows, _ := s.Images(ImageFilter{})
			Expect(rows).To(HaveLen(3), "rows = %+v", rows)
			Expect(rows[0].Host).To(Equal(mirrorHost), "proxy row = %+v", rows[0])
			Expect(rows[0].IndexDigest).To(BeEmpty(), "proxy row = %+v", rows[0])
			Expect(rows[0].Behaviour).To(Equal("503"), "proxy row = %+v", rows[0])
			Expect(rows[0].Exact).To(BeTrue(), "proxy row = %+v", rows[0])
			Expect(rows[1].Repo).To(Equal("acme/drifted"), "drifted row = %+v", rows[1])
			Expect(rows[1].Generation).To(Equal(2), "drifted row = %+v", rows[1])
			Expect(rows[1].IndexDigest).NotTo(BeEmpty(), "drifted row = %+v", rows[1])
			Expect(rows[2].Repo).To(Equal("acme/seen"), "seen row = %+v", rows[2])
			Expect(rows[2].Generation).To(Equal(1), "seen row = %+v", rows[2])
			Expect(rows[2].Behaviour).To(Equal("ok"), "seen row = %+v", rows[2])
			Expect(rows[2].Pinned).To(HavePrefix("upstream.kt.test/acme/seen:t1@sha256:"), "seen row = %+v", rows[2])
			Expect(rows[2].Pinned).To(HaveSuffix(rows[2].IndexDigest), "seen row = %+v", rows[2])
		})
	})

	Describe("Resolve", func() {
		It("resolves an image when the state file cannot be written", func() {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			st, err := state.Open(p)
			Expect(err).NotTo(HaveOccurred())
			// A directory where the store writes its temporary file fails the write, root or not.
			Expect(os.Mkdir(p+".tmp", 0o700)).To(Succeed())
			Expect(st.Update(func(*state.State) error { return nil })).NotTo(Succeed(), "the injected write must fail")
			s, _, _ := newServiceOn(nil, st)
			got, err := s.Resolve(upstreamHost, repoFoo, "t1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.IndexDigest).NotTo(BeEmpty())
			Expect(st.Snapshot().Digests).To(HaveKey(got.IndexDigest), "the failed write must still publish the image")
			Expect(p).NotTo(BeAnExistingFile(), "the failed write must leave no state file")
		})

		It("enables a pinned reference that resolves by digest", func() {
			s, _, _ := newService(nil)
			res, err := s.Resolve(upstreamHost, repoFoo, "t1")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Platforms).To(HaveLen(2), "resolved = %+v", res)
			Expect(res.Platforms[platAMD64]).NotTo(BeEmpty(), "resolved = %+v", res)
			Expect(res.Pinned).To(HaveSuffix("@"+res.IndexDigest), "resolved = %+v", res)
			rows, _ := s.Images(ImageFilter{})
			Expect(rows).To(HaveLen(1), "image not seen after Resolve: %+v", rows)
			Expect(rows[0].Tag).To(Equal("t1"), "image not seen after Resolve: %+v", rows)

			// A fresh store: the digest resolves with no prior tag pull.
			s, st, q := newService(nil)
			res, _ = s.Resolve(upstreamHost, repoFoo, "t1")
			srv := httptest.NewServer(fakeregistry.New(s.cfg, s.proxies, st, reqlog.New(100), q).Handler())
			DeferCleanup(srv.Close)
			ref := testutil.Ref("upstream.kt.test/acme/foo@" + res.IndexDigest)
			_, err = remote.Index(ref, testutil.Opts(srv)...)
			Expect(err).NotTo(HaveOccurred(), "remote.Index by pinned digest")

			wantInvalid(errOf(s.Resolve(mirrorHost, repoFoo, "t1")))
			wantInvalid(errOf(s.Resolve(upstreamHost, repoFoo, "-bad")))
		})

		DescribeTable("checks the host label like the registry through Resolve",
			func(host string, valid bool) {
				s, _, _ := newService(nil)
				_, err := s.Resolve(host, repoFoo, "t1")
				if valid {
					Expect(err).NotTo(HaveOccurred())
					return
				}
				wantInvalid(err)
			},
			Entry("63 bytes", strings.Repeat("a", 63), true),
			Entry("64 bytes", strings.Repeat("a", 64), false),
		)
	})

	Describe("Drift", func() {
		It("lower-cases the host", func() {
			s, _, _ := newService(nil)
			_, err := s.Resolve(upstreamHost, repoFoo, "t1")
			Expect(err).NotTo(HaveOccurred())
			wantInvalid(errOf(s.Drift(api.Target{Host: "MIRROR", Repo: repoFoo, Tag: "t1"})))
			got, err := s.Drift(api.Target{Host: upstreamCap, Repo: repoFoo, Tag: "t1"})
			Expect(err).NotTo(HaveOccurred(), "Drift(Upstream)")
			Expect(got).To(Equal([]string{"upstream/acme/foo:t1"}), "Drift(Upstream)")
		})
	})
})
