package rules

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("ContentKey", func() {
	Context("with an alias", func() {
		var s *state.State

		BeforeEach(func() {
			s = &state.State{Aliases: []api.Alias{{ID: "a", Host: hostAlt, From: "mirror/acme", To: acmeNS}}}
		})

		DescribeTable("rewrites the repo prefix",
			func(host, repo, want string) {
				Expect(ContentKey(s, host, repo)).To(Equal(want), "ContentKey(%s,%s)", host, repo)
			},
			Entry("rewrites a path below the prefix", hostAlt, repoMirrored, "acme/foo/bar"),
			Entry("rewrites the prefix itself", hostAlt, "mirror/acme", acmeNS),
			Entry("leaves another host alone", "alt2", repoMirrored, repoMirrored),
			Entry("does not match a sibling that only shares the prefix text", hostAlt, "mirror/acmex/foo", "mirror/acmex/foo"),
		)
	})

	It("applies the first alias that matches", func() {
		s := &state.State{Aliases: []api.Alias{
			{ID: "a", Host: hostAlt, From: "m", To: "first"},
			{ID: "b", Host: hostAlt, From: "m", To: "second"},
		}}
		Expect(ContentKey(s, hostAlt, "m/foo")).To(Equal("first/foo"))
	})

	Context("with a divergent rule", func() {
		It("adds the salt on the matching host only", func() {
			s := &state.State{Divergents: []api.Divergent{{ID: "d", Host: hostAlt, Repo: repoGlob, Salt: "x"}}}
			Expect(ContentKey(s, hostAlt, repoFoo)).To(Equal("acme/foo#x"), "alt1")
			Expect(ContentKey(s, upstreamHost, repoFoo)).To(Equal(repoFoo), "upstream")
		})

		It("matches the request path, not the aliased one", func() {
			s := &state.State{
				Aliases:    []api.Alias{{ID: "a", Host: hostAlt, From: mirrorHost, To: acmeNS}},
				Divergents: []api.Divergent{{ID: "d", Host: hostAlt, Repo: "mirror/**", Salt: "x"}},
			}
			Expect(ContentKey(s, hostAlt, "mirror/foo")).To(Equal("acme/foo#x"))
			Expect(ContentKey(s, hostAlt, repoFoo)).To(Equal(repoFoo), "unaliased path must not match divergent")
		})
	})
})

var _ = Describe("Platforms", func() {
	It("uses the first rule that matches", func() {
		s := &state.State{Platforms: []api.PlatformRule{
			{ID: "a", Host: "*", Repo: "acme/plat/*", Platforms: []string{"linux/arm64"}},
			{ID: "b", Host: "*", Repo: "acme/**", Platforms: []string{"linux/amd64"}},
		}}
		got := Platforms(s, upstreamHost, "acme/plat/foo")
		Expect(got).To(HaveLen(1), "Platforms(plat) = %+v", got)
		Expect(got[0].Architecture).To(Equal("arm64"))
		got = Platforms(s, upstreamHost, "acme/other/foo")
		Expect(got).To(HaveLen(1), "Platforms(other) = %+v", got)
		Expect(got[0].Architecture).To(Equal("amd64"))
		Expect(Platforms(s, upstreamHost, "other/foo")).To(BeNil(), "no match")
	})
})
