package rules

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

func st(rs ...api.Rule) *state.State { return &state.State{Rules: rs} }

var _ = Describe("Evaluate", func() {
	It("returns a copy of the first rule that matches", func() {
		s := st(
			api.Rule{ID: "a", Enabled: true, Target: api.Target{Repo: repoGlob}},
			api.Rule{ID: "b", Enabled: true},
		)
		got := Evaluate(s, Request{Host: upstreamHost, Repo: repoFoo})
		Expect(got).NotTo(BeNil())
		Expect(got.ID).To(Equal("a"))
		got.ID = "x"
		Expect(s.Rules[0].ID).To(Equal("a"), "Evaluate must return a copy")
		Expect(Evaluate(s, Request{Repo: "other/x"}).ID).To(Equal("b"), "fallthrough to b expected")
	})

	It("skips disabled rules", func() {
		s := st(api.Rule{ID: "a", Enabled: false}, api.Rule{ID: "b", Enabled: true})
		got := Evaluate(s, Request{})
		Expect(got).NotTo(BeNil())
		Expect(got.ID).To(Equal("b"))
		Expect(Evaluate(st(api.Rule{ID: "a"}), Request{})).To(BeNil(), "disabled-only must not match")
	})
})

var _ = Describe("Matches", func() {
	DescribeTable("does not leak to a sibling repo",
		func(repo string) {
			Expect(Matches(api.Target{Repo: repoFoo}, Request{Repo: repo})).To(BeFalse())
		},
		Entry("with a longer last segment", "acme/foo-bar"),
		Entry("with a nested repo", "acme/foo/bar"),
	)

	It("matches the exact repo", func() {
		Expect(Matches(api.Target{Repo: repoFoo}, Request{Repo: repoFoo})).To(BeTrue())
	})

	It("tells a digest request from a tag rule", func() {
		byDigest := Request{Kind: api.KindManifestByDigest, Repo: "r"}
		byTag := Request{Kind: api.KindManifestByTag, Repo: "r", Tag: "t1"}
		plain := api.Target{Tag: "t1"}
		Expect(Matches(plain, byDigest)).To(BeFalse(), "plain tag rule vs digest request")
		Expect(Matches(plain, byTag)).To(BeTrue(), "plain tag rule vs tag request")
		kinds := api.Target{Tag: "t1", Kinds: []api.Kind{api.KindManifestByDigest}}
		Expect(Matches(kinds, byDigest)).To(BeTrue(), "explicit manifest-by-digest kind must match a digest request")
		star := api.Target{Tag: "*"}
		Expect(Matches(star, byDigest)).To(BeTrue(), "tag * matches digest")
		Expect(Matches(star, byTag)).To(BeTrue(), "tag * matches tag")
	})

	It("matches the anonymous caller and named callers exactly", func() {
		anon := api.Target{Caller: "anonymous"}
		alice := api.Target{Caller: userAlice}
		Expect(Matches(anon, Request{})).To(BeTrue(), "anonymous matches no user")
		Expect(Matches(anon, Request{User: userAlice})).To(BeFalse(), "anonymous vs alice")
		Expect(Matches(alice, Request{User: userAlice})).To(BeTrue(), userAlice)
		Expect(Matches(alice, Request{})).To(BeFalse(), "alice vs anonymous")
		Expect(Matches(alice, Request{User: userBob})).To(BeFalse(), "alice vs bob")
	})

	It("matches kinds and methods, ignoring the method case", func() {
		tg := api.Target{Kinds: []api.Kind{api.KindBlob}, Methods: []string{"head"}}
		Expect(Matches(tg, Request{Kind: api.KindBlob, Method: http.MethodHead})).To(BeTrue(), "head vs HEAD")
		Expect(Matches(tg, Request{Kind: api.KindBlob, Method: http.MethodGet})).To(BeFalse(), "wrong method")
		Expect(Matches(tg, Request{Kind: api.KindPing, Method: http.MethodHead})).To(BeFalse(), "wrong kind")
	})
})
