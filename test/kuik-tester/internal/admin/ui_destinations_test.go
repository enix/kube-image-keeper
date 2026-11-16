package admin

import (
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	uiDestSection = "dest-mirror"
	uiFieldPrefix = "prefix"
	uiPrefixAcme  = "acme"
	uiRepoNew     = "acme/new"
	uiRepoBar     = "other/bar"
	uiTagT2       = "t2"
	uiTagT9       = "t9"
	uiRefFooT1    = "acme/foo:t1"
	uiRefFooT2    = "acme/foo:t2"
	uiRefBarT1    = "other/bar:t1"
	uiOpPush      = "push"
	uiOpDeleteTag = "delete-tag"
	uiOpPurge     = "purge"
)

// uiDestRefs lists the repo:tag pairs the mirror backend holds.
func uiDestRefs(e *uiEnv) []string {
	GinkgoHelper()
	repos, err := e.svc.Inventory(ctx, mirrorHost)
	Expect(err).NotTo(HaveOccurred())
	var refs []string
	for _, r := range repos {
		for _, t := range r.Tags {
			refs = append(refs, r.Name+":"+t.Tag)
		}
	}
	return refs
}

// uiDestEnv is a UI whose mirror holds acme/foo:t1, acme/foo:t2 and other/bar:t1.
func uiDestEnv() *uiEnv {
	GinkgoHelper()
	e := newUIEnv()
	for _, ref := range [][2]string{{repoFoo, uiTagT1}, {repoFoo, uiTagT2}, {uiRepoBar, uiTagT1}} {
		Expect(e.svc.DestPushSynthetic(ctx, mirrorHost, ref[0], ref[1], "", 1)).To(Succeed())
	}
	return e
}

// uiDigestOf returns the digest of repo:tag on the mirror.
func uiDigestOf(e *uiEnv, repo, tag string) string {
	GinkgoHelper()
	repos, err := e.svc.Inventory(ctx, mirrorHost)
	Expect(err).NotTo(HaveOccurred())
	for _, r := range repos {
		for _, t := range r.Tags {
			if r.Name == repo && t.Tag == tag {
				return t.Digest
			}
		}
	}
	Fail("no " + repo + ":" + tag)
	return ""
}

var _ = Describe("UI destinations", func() {
	It("lists the pushed repositories on the page", func() {
		e := uiDestEnv()
		rec := e.get("/destinations")
		uiExpectPage(rec)
		Expect(uiAttrs(rec.Body.String(), "data-ref")).To(ConsistOf(uiRefFooT1, uiRefFooT2, uiRefBarT1))
	})

	It("pushes a tag and answers the section of the host", func() {
		e := newUIEnv()
		rec := e.post(uiPathDest+uiOpPush, url.Values{formRepo: {repoFoo}, formTag: {uiTagT1}, "generation": {"1"}})
		uiExpectFragment(rec, uiDestSection)
		Expect(uiAttrs(rec.Body.String(), "data-ref")).To(ContainElement(uiRefFooT1))
		repos, err := e.svc.Inventory(ctx, mirrorHost)
		Expect(err).NotTo(HaveOccurred())
		Expect(repos).To(HaveLen(1))
		Expect(repos[0].Tags).To(HaveLen(1))
		Expect(repos[0].Tags[0].Tag).To(Equal(uiTagT1))
	})

	DescribeTable("applies the destination action",
		func(op string, form func(digest string) url.Values, want []string) {
			e := uiDestEnv()
			rec := e.post(uiPathDest+op, form(uiDigestOf(e, repoFoo, uiTagT1)))
			Expect(rec.Code).To(Equal(200), "body = %s", rec.Body)
			Expect(rec.Header()).NotTo(HaveKey(uiHdrRetarget))
			Expect(rec.Body.String()).To(ContainSubstring(`id="` + uiDestSection + `"`))
			Expect(rec.Body.String()).To(ContainSubstring(uiClassOK))
			Expect(uiDestRefs(e)).To(ConsistOf(want))
		},
		Entry("push", uiOpPush, func(string) url.Values {
			return url.Values{formRepo: {uiRepoNew}, formTag: {uiTagT1}}
		}, []string{uiRefFooT1, uiRefFooT2, uiRefBarT1, "acme/new:t1"}),
		Entry("tag", "tag", func(string) url.Values {
			return url.Values{formRepo: {repoFoo}, "src": {uiTagT1}, "newTag": {uiTagT9}}
		}, []string{uiRefFooT1, uiRefFooT2, uiRefBarT1, "acme/foo:t9"}),
		Entry("delete-tag", uiOpDeleteTag, func(string) url.Values {
			return url.Values{formRepo: {repoFoo}, formTag: {uiTagT2}}
		}, []string{uiRefFooT1, uiRefBarT1}),
		Entry("delete-manifest", "delete-manifest", func(d string) url.Values {
			return url.Values{formRepo: {repoFoo}, "digest": {d}}
		}, []string{uiRefBarT1}),
		Entry("purge", uiOpPurge, func(string) url.Values {
			return url.Values{uiFieldPrefix: {uiPrefixAcme}}
		}, []string{uiRefBarT1}),
	)

	DescribeTable("reports a refused destination action inside its section",
		func(op string, form url.Values) {
			e := uiDestEnv()
			rec := e.post(uiPathDest+op, form)
			Expect(rec.Code).To(Equal(200), "body = %s", rec.Body)
			Expect(rec.Header()).NotTo(HaveKey(uiHdrRetarget))
			Expect(rec.Body.String()).To(ContainSubstring(`id="` + uiDestSection + `"`))
			Expect(rec.Body.String()).To(ContainSubstring(uiClassErr))
			Expect(uiDestRefs(e)).To(ConsistOf(uiRefFooT1, uiRefFooT2, uiRefBarT1))
		},
		Entry("a bad repo", uiOpDeleteTag, url.Values{formRepo: {"Bad Repo"}, formTag: {uiTagT1}}),
		Entry("a glob prefix", uiOpPurge, url.Values{uiFieldPrefix: {"acme/*"}}),
		Entry("an empty prefix", uiOpPurge, url.Values{uiFieldPrefix: {""}}),
		Entry("a bad generation", uiOpPush, url.Values{formRepo: {uiRepoNew}, formTag: {uiTagT1}, "generation": {"two"}}),
		Entry("an unknown operation", "rename", url.Values{formRepo: {repoFoo}}),
	)

	It("reports an unknown destination in the message area", func() {
		uiExpectError(newUIEnv().post("/ui/destinations/nope/"+uiOpPurge, url.Values{uiFieldPrefix: {uiPrefixAcme}}))
	})
})
