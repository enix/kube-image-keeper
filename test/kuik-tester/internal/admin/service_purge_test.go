package admin

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// purgeFixture holds two objects of every kind, one of them in a set.
const purgeFixture = `rules:
- {id: r1, enabled: true, target: {host: upstream}, effects: {status: 503}}
aliases:
- {id: a1, host: upstream, from: a, to: b}
divergents:
- {id: d1, host: upstream, repo: acme/foo, salt: s}
grants:
- {id: g1, host: private, repo: '**', actions: [pull], users: [u1]}
platforms:
- {id: p1, host: upstream, repo: acme/foo, platforms: [linux/amd64]}
users:
- {name: u1, password: pw}
hostAuth:
- {host: upstream, scheme: bearer}
quotas:
- {name: q1, limit: 5, windowSec: 60, key: global}
`

var _ = Describe("Service purge", func() {
	var s *Service

	BeforeEach(func() {
		s, _, _ = newService(nil)
		Expect(s.Import([]byte(purgeFixture))).To(Succeed())
		Expect(s.ImportSet("extra", []byte("rules:\n- {id: r2, enabled: true, effects: {status: 500}}\naliases:\n- {id: a2, host: upstream, from: c, to: d}\ndivergents:\n- {id: d2, host: upstream, repo: acme/bar, salt: t}\nplatforms:\n- {id: p2, host: upstream, repo: acme/bar, platforms: [linux/arm64]}\nhostAuth:\n- {host: other, scheme: basic}\nquotas:\n- {name: q2, limit: 5, windowSec: 60, key: global}\nusers:\n- {name: u2, password: pw}\n"))).To(Succeed())
	})

	count := func(kind string) int {
		GinkgoHelper()
		switch kind {
		case kindRules:
			return len(s.ListRules())
		case kindAliases:
			return len(s.ListAliases())
		case kindDivergents:
			return len(s.ListDivergents())
		case kindGrants:
			return len(s.ListGrants())
		case kindPlatforms:
			return len(s.ListPlatforms())
		case kindUsers:
			return len(s.ListUsers())
		case kindHostAuth:
			return len(s.ListHostAuth())
		case kindQuotas:
			return len(s.ListQuotas())
		}
		Fail("unknown kind " + kind)
		return 0
	}

	DescribeTable("PurgeColl empties one list, sets included, and counts",
		func(kind string, want int) {
			if kind == kindUsers {
				_, err := s.PurgeColl(kindGrants) // a grant names u1
				Expect(err).NotTo(HaveOccurred())
			}
			before := map[string]int{}
			for _, k := range purgeKinds {
				before[k] = count(k)
			}
			Expect(before[kind]).To(Equal(want))
			n, err := s.PurgeColl(kind)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(want), kind)
			Expect(count(kind)).To(BeZero(), kind)
			for k, c := range before {
				if k != kind {
					Expect(count(k)).To(Equal(c), "%s must be left alone by a purge of %s", k, kind)
				}
			}
		},
		Entry("rules", kindRules, 2),
		Entry("aliases", kindAliases, 2),
		Entry("divergents", kindDivergents, 2),
		Entry("grants", kindGrants, 1),
		Entry("platforms", kindPlatforms, 2),
		Entry("users", kindUsers, 2),
		Entry("hostauth", kindHostAuth, 2),
		Entry("quotas", kindQuotas, 2),
	)

	It("refuses an unknown kind as invalid", func() {
		_, err := s.PurgeColl("nope")
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(&ValidationError{}))
		Expect(count("rules")).To(Equal(2))
	})

	It("refuses to purge users a grant still names, and changes nothing", func() {
		_, err := s.PurgeColl(kindUsers)
		Expect(err).To(BeAssignableToTypeOf(&ValidationError{}))
		Expect(count("users")).To(Equal(2))
	})

	It("resets the counters of the purged quotas", func() {
		s2, _, q := newService(nil)
		def := api.QuotaDef{Name: "q", Limit: 5, WindowSec: 60, Key: keyGlobal}
		Expect(s2.PutQuota(def)).To(Succeed())
		q.Take(def, "", time.Now())
		Expect(s2.QuotaUsage()["q"]).NotTo(BeEmpty())
		n, err := s2.PurgeColl(kindQuotas)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		Expect(q.Snapshot(time.Now())["q"]).To(BeEmpty())
	})

	It("PurgeImages empties Seen, Digests and Generations and counts the distinct images", func() {
		s, st, _ := newService(nil)
		a, b := state.RefKey(upstreamHost, "acme/a", "t1"), state.RefKey(upstreamHost, "acme/b", "t1")
		Expect(st.Update(func(x *state.State) error {
			x.Seen[a] = time.Now()
			x.Seen[b] = time.Now()
			x.Generations[a] = 3
			x.Generations[state.RefKey(upstreamHost, "acme/c", "t1")] = 2
			x.Digests["sha256:x"] = state.DigestEntry{}
			return nil
		})).To(Succeed())
		n, err := s.PurgeImages()
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
		snap := st.Snapshot()
		Expect(snap.Seen).To(BeEmpty())
		Expect(snap.Digests).To(BeEmpty())
		Expect(snap.Generations).To(BeEmpty())
		Expect(snap.Seen).NotTo(BeNil())
		rows, total := s.Images(ImageFilter{})
		Expect(rows).To(BeEmpty())
		Expect(total).To(BeZero())
	})

	It("PurgeImages keeps listing an image a rule names literally", func() {
		s, st, _ := newService(nil)
		_, err := s.CreateRule(api.Rule{Enabled: true, Target: api.Target{Host: upstreamHost, Repo: "acme/ruled", Tag: "t1"}, Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Update(func(x *state.State) error {
			x.Seen[state.RefKey(upstreamHost, "acme/ruled", "t1")] = time.Now()
			return nil
		})).To(Succeed())
		n, err := s.PurgeImages()
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		rows, _ := s.Images(ImageFilter{})
		Expect(rows).To(HaveLen(1))
	})

	It("DestPurgeAll deletes every repository of a proxy host and reports a missing host", func() {
		ctx := context.Background()
		s, _, _ := newService(nil)
		Expect(s.DestPushSynthetic(ctx, mirrorHost, "acme/a", "t1", "", 0)).To(Succeed())
		Expect(s.DestPushSynthetic(ctx, mirrorHost, "other/b", "t1", "", 0)).To(Succeed())
		res, err := s.DestPurgeAll(ctx, mirrorHost)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.DeletedTags).To(Equal(2))
		Expect(res.Repositories).To(ConsistOf("acme/a", "other/b"))
		_, err = s.DestPurgeAll(ctx, "nope")
		Expect(err).To(MatchError(ErrNotFound))
	})
})
