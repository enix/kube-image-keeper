package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// purgeAllBackends starts n in-memory backends.
func purgeAllBackends(n int) []*httptest.Server {
	GinkgoHelper()
	out := make([]*httptest.Server, n)
	for i := range out {
		out[i] = testutil.NewBackend()
		DeferCleanup(out[i].Close)
	}
	return out
}

func purgeAllConfig(proxies ...testerconfig.ProxyHost) *testerconfig.Config {
	return &testerconfig.Config{BaseDomain: baseDomain, Proxies: proxies}
}

var _ = Describe("Service PurgeAll", func() {
	ctx := context.Background()

	It("empties every list and map in one write and counts what it removed", func() {
		s, st, _ := newService(nil)
		Expect(s.Import([]byte(purgeFixture))).To(Succeed())
		Expect(s.ImportSet("extra", []byte("rules:\n- {id: r2, enabled: true, effects: {status: 500}}\n"))).To(Succeed())
		a := state.RefKey(upstreamHost, "acme/a", "t1")
		Expect(st.Update(func(x *state.State) error {
			x.Seen[a] = time.Now()
			x.Generations[a] = 2
			x.Generations[state.RefKey(upstreamHost, "acme/b", "t1")] = 1
			x.Digests["sha256:x"] = state.DigestEntry{}
			return nil
		})).To(Succeed())

		res, err := s.PurgeAll(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(api.PurgeAllResult{Rules: 2, Aliases: 1, Divergents: 1, Grants: 1, Platforms: 1, Users: 1, HostAuth: 1, Quotas: 1, Images: 2}))

		snap := st.Snapshot()
		Expect(snap.Seen).To(BeEmpty())
		Expect(snap.Digests).To(BeEmpty())
		Expect(snap.Generations).To(BeEmpty())
		Expect(s.Sets()).To(BeEmpty())
		b, err := json.Marshal(snap)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring("null"))
		Expect(string(b)).To(Equal(`{"rules":[],"aliases":[],"divergents":[],"users":[],"grants":[],"hostAuth":[],"quotas":[],"platforms":[],"generations":{},"digests":{},"seen":{}}`))
	})

	It("answers zeros on an empty state", func() {
		s, _, _ := newService(nil)
		res, err := s.PurgeAll(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(api.PurgeAllResult{}))
	})

	It("resets every quota counter", func() {
		s, _, q := newService(nil)
		a := api.QuotaDef{Name: "a", Limit: 5, WindowSec: 60, Key: keyGlobal}
		b := api.QuotaDef{Name: "b", Limit: 5, WindowSec: 60, Key: keyGlobal}
		Expect(s.PutQuota(a)).To(Succeed())
		Expect(s.PutQuota(b)).To(Succeed())
		q.Take(a, "", time.Now())
		q.Take(b, "", time.Now())
		_, err := s.PurgeAll(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(q.Snapshot(time.Now())).To(BeEmpty())
	})

	It("leaves the backends alone without the flag, and reports none", func() {
		s, _, _ := newService(nil)
		Expect(s.DestPushSynthetic(ctx, mirrorHost, "acme/a", "t1", "", 0)).To(Succeed())
		res, err := s.PurgeAll(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Backends).To(BeEmpty())
		repos, err := s.Inventory(ctx, mirrorHost)
		Expect(err).NotTo(HaveOccurred())
		Expect(repos).To(HaveLen(1))
	})

	It("purges each distinct backend once, however many proxy names share it", func() {
		be := purgeAllBackends(2)
		s, _, _ := newService(purgeAllConfig(
			testerconfig.ProxyHost{Name: mirrorHost, Backend: be[0].URL},
			testerconfig.ProxyHost{Name: "mirror-*", Backend: be[0].URL},
			testerconfig.ProxyHost{Name: "nodelete", Backend: be[0].URL},
			testerconfig.ProxyHost{Name: other, Backend: be[1].URL},
		))
		Expect(s.DestPushSynthetic(ctx, "mirror", "acme/a", "t1", "", 0)).To(Succeed())
		Expect(s.DestPushSynthetic(ctx, "mirror-x", "acme/a", "t2", "", 0)).To(Succeed())
		Expect(s.DestPushSynthetic(ctx, "other", "acme/b", "t1", "", 0)).To(Succeed())

		res, err := s.PurgeAll(ctx, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Backends).To(HaveLen(2))
		byName := map[string]api.BackendPurge{}
		for _, b := range res.Backends {
			byName[b.Backend] = b
		}
		first, second := byName[be[0].URL], byName[be[1].URL]
		Expect(first.DeletedTags).To(Equal(2))
		Expect(first.Repositories).To(ConsistOf("acme/a"))
		Expect(first.Error).To(BeEmpty())
		Expect(second.DeletedTags).To(Equal(1))
		Expect(second.Repositories).To(ConsistOf("acme/b"))
		for _, h := range []string{"mirror", "other"} {
			repos, err := s.Inventory(ctx, h)
			Expect(err).NotTo(HaveOccurred())
			for _, r := range repos {
				Expect(r.Tags).To(BeEmpty(), h+"/"+r.Name)
			}
		}
	})

	It("reports a failing backend and still purges the others and the state", func() {
		be := purgeAllBackends(2)
		dead := testutil.NewBackend()
		deadURL := dead.URL
		dead.Close()
		s, _, _ := newService(purgeAllConfig(
			testerconfig.ProxyHost{Name: "dead", Backend: deadURL},
			testerconfig.ProxyHost{Name: mirrorHost, Backend: be[0].URL},
		))
		_, err := s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.DestPushSynthetic(ctx, "mirror", "acme/a", "t1", "", 0)).To(Succeed())

		res, err := s.PurgeAll(ctx, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Rules).To(Equal(1))
		Expect(s.ListRules()).To(BeEmpty())
		Expect(res.Backends).To(HaveLen(2))
		for _, b := range res.Backends {
			if b.Backend == deadURL {
				Expect(b.Error).NotTo(BeEmpty())
				Expect(b.Repositories).NotTo(BeNil())
			} else {
				Expect(b.Error).To(BeEmpty())
				Expect(b.DeletedTags).To(Equal(1))
			}
		}
	})

	It("purgeAll also clears the request log and resets its stats", func() {
		s, _, _ := newService(nil)
		l := reqlog.New(10)
		for range 3 {
			l.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Kind: api.KindPing, Status: 200})
		}
		Expect(l.Stats()).NotTo(BeEmpty())
		res, err := purgeAll(ctx, s, l, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Requests).To(Equal(3))
		Expect(l.Stats()).To(BeEmpty())
		Expect(l.Clear()).To(BeZero())
	})
})
