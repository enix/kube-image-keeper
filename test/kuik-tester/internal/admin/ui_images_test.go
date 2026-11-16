package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// uiImage is the form that names acme/foo:t1 on the upstream host.
func uiImage(action string) url.Values {
	return url.Values{formHost: {upstreamHost}, formRepo: {repoFoo}, formTag: {uiTagT1}, formAction: {action}}
}

const (
	uiAltHost   = "alt1"
	uiAltRef    = "alt1/acme/foo:t1"
	uiBarRef    = "upstream/other/bar:t1"
	uiFilterXSS = `"><script>x</script>`
	uiCapNotice = `id="images-capped"`
	uiFormRepo  = "?repo="
)

// uiThreeImages resolves the three images the filter specs tell apart.
func uiThreeImages(e *uiEnv) {
	GinkgoHelper()
	for _, img := range [][2]string{{upstreamHost, repoFoo}, {upstreamHost, uiRepoBar}, {uiAltHost, repoFoo}} {
		_, err := e.svc.Resolve(img[0], img[1], uiTagT1)
		Expect(err).NotTo(HaveOccurred())
	}
}

var _ = Describe("UI images", func() {
	It("lists a seen image", func() {
		e := newUIEnv()
		_, err := e.svc.Resolve(upstreamHost, repoFoo, uiTagT1)
		Expect(err).NotTo(HaveOccurred())
		rec := e.get("/")
		uiExpectPage(rec)
		Expect(uiAttrs(rec.Body.String(), "data-ref")).To(ContainElement(uiRef))
	})

	It("applies an image action and answers the row back", func() {
		e := newUIEnv()
		rec := e.post(uiPathImages+"action", uiImage(uiNumber503))
		Expect(uiAttrs(rec.Body.String(), "data-ref")).To(ContainElement(uiRef))
		Expect(rec.Header()).NotTo(HaveKey(uiHdrRetarget))
		rs := e.svc.ListRules()
		Expect(rs).To(HaveLen(1))
		Expect(rs[0].Effects.Status).To(Equal(503))
	})

	It("reports an unknown action and creates no rule", func() {
		e := newUIEnv()
		uiExpectError(e.post(uiPathImages+"action", uiImage("bogus")))
		Expect(e.svc.ListRules()).To(BeEmpty())
	})

	It("pins what it resolves", func() {
		e := newUIEnv()
		rec := e.post(uiPathImages+"resolve", url.Values{formHost: {upstreamHost}, formRepo: {repoFoo}, formTag: {uiTagT1}})
		Expect(rec.Code).To(Equal(200))
		var pinned string
		rows, _ := e.svc.Images(ImageFilter{})
		for _, row := range rows {
			if row.Repo == repoFoo && row.Tag == uiTagT1 {
				pinned = row.Pinned
			}
		}
		Expect(pinned).NotTo(BeEmpty())
		Expect(uiAttrs(rec.Body.String(), "data-pinned")).To(ConsistOf(pinned))
	})

	It("offers the drift form and reports what drifted", func() {
		e := newUIEnv()
		_, err := e.svc.Resolve(upstreamHost, repoFoo, uiTagT1)
		Expect(err).NotTo(HaveOccurred())
		Expect(uiAttrs(e.get("/").Body.String(), "hx-post")).To(ContainElement(uiPathImages + "drift"))
		rec := e.post(uiPathImages+"drift", url.Values{formHost: {upstreamHost}, formRepo: {repoGlob}, formTag: {"*"}})
		Expect(uiAttrs(rec.Body.String(), "data-count")).To(ConsistOf("1"))
		Expect(uiAttrs(rec.Body.String(), "data-ref")).To(ConsistOf(uiRef))
		var gen int
		rows, _ := e.svc.Images(ImageFilter{})
		for _, row := range rows {
			if row.Repo == repoFoo {
				gen = row.Generation
			}
		}
		Expect(gen).To(Equal(2))
	})

	It("reports a drift that matched nothing", func() {
		rec := newUIEnv().post(uiPathImages+"drift", url.Values{formHost: {upstreamHost}, formRepo: {"nomatch*"}, formTag: {"x*"}})
		Expect(uiAttrs(rec.Body.String(), "data-count")).To(ConsistOf("0"))
	})

	It("renders one button per shown action, Drift only with an index digest", func() {
		e := newUIEnv()
		_, err := e.svc.ImageAction(mirrorHost, repoFoo, uiTagT1, uiNumber503)
		Expect(err).NotTo(HaveOccurred())
		_, err = e.svc.Resolve(upstreamHost, repoFoo, uiTagT1)
		Expect(err).NotTo(HaveOccurred())
		body := e.get("/").Body.String()
		rows := strings.Split(body, "<tr data-ref=")
		Expect(rows).To(HaveLen(3), "one row per image")
		for _, row := range rows[1:] {
			buttons := uiTags(row, `name="action"`)
			if strings.HasPrefix(row, `"`+mirrorHost+"/") {
				Expect(row).NotTo(ContainSubstring(`value="drift"`), "proxy row")
				Expect(buttons).To(HaveLen(len(imageActions)-2), "proxy row has neither Drift nor Exempt")
			} else {
				Expect(row).To(ContainSubstring(`value="drift"`), "synthetic row")
				Expect(buttons).To(HaveLen(len(imageActions)-1), "synthetic row has no Exempt")
			}
		}
	})

	It("offers Exempt only for an image matched by a broader rule", func() {
		e := newUIEnv()
		_, err := e.svc.Resolve(upstreamHost, repoFoo, uiTagT1)
		Expect(err).NotTo(HaveOccurred())
		Expect(e.get("/").Body.String()).NotTo(ContainSubstring(`value="exempt"`))
		_, err = e.svc.CreateRule(api.Rule{Enabled: true, Target: api.Target{Host: upstreamHost, Repo: repoDeep}, Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		Expect(e.get("/").Body.String()).To(ContainSubstring(`value="exempt"`))
	})

	DescribeTable("filters the images by host and repo",
		func(query string, refs ...string) {
			e := newUIEnv()
			uiThreeImages(e)
			Expect(uiAttrs(e.get("/"+query).Body.String(), "data-ref")).To(ConsistOf(refs))
		},
		Entry("a host substring", "?host=alt", uiAltRef),
		Entry("a repo substring", uiFormRepo+"bar", uiBarRef),
		Entry("host and repo together", "?host=upstream&repo=foo", uiRef),
		Entry("no filter", "", uiRef, uiBarRef, uiAltRef),
		Entry("no match", uiFormRepo+"nomatch"),
	)

	It("caps the rows and says how many matched", func() {
		e := newUIEnv()
		uiThreeImages(e)
		rec := httptest.NewRecorder()
		newUI(e.svc, e.log, 2).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		body := rec.Body.String()
		Expect(uiAttrs(body, "data-ref")).To(HaveLen(2))
		Expect(body).To(ContainSubstring(uiCapNotice))
		Expect(uiAttrs(body, "data-total")).To(ConsistOf("3"))
		Expect(uiAttrs(body, "data-shown")).To(ConsistOf("2"))
	})

	It("shows no cap notice under the cap", func() {
		e := newUIEnv()
		uiThreeImages(e)
		Expect(e.get("/").Body.String()).NotTo(ContainSubstring(uiCapNotice))
	})

	It("does not echo the filter values raw", func() {
		body := newUIEnv().get("/" + uiFormRepo + url.QueryEscape(uiFilterXSS)).Body.String()
		Expect(body).NotTo(ContainSubstring("<script>x"))
		Expect(uiAttrs(body, "value")).To(ContainElement(uiFilterXSS))
	})

	DescribeTable("tells an empty list from a filter that matches nothing",
		func(query string, filtered string) {
			e := newUIEnv()
			uiThreeImages(e)
			body := e.get("/" + query).Body.String()
			Expect(uiAttrs(body, "data-ref")).To(BeEmpty())
			Expect(uiAttrs(body, "data-filtered")).To(ConsistOf(filtered))
		},
		Entry("a host filter", "?host=nomatch", "true"),
		Entry("a repo filter", uiFormRepo+"nomatch", "true"),
	)

	It("marks the empty list as not filtered when no image was seen", func() {
		Expect(uiAttrs(newUIEnv().get("/").Body.String(), "data-filtered")).To(ConsistOf("false"))
	})

	It("gives every quick action a title and offers the expected actions", func() {
		e := newUIEnv()
		_, err := e.svc.Resolve(upstreamHost, repoFoo, uiTagT1)
		Expect(err).NotTo(HaveOccurred())
		tags := uiTags(e.get("/").Body.String(), `name="action"`)
		values := make([]string, 0, len(tags))
		for _, tag := range tags {
			a := uiTagAttrs(tag)
			Expect(a["title"]).NotTo(BeEmpty(), "no title on %s", tag)
			values = append(values, a["value"])
		}
		Expect(values).To(Equal([]string{"ok", "404", "401", "403", "429", "ratelimit0", "503", "latency", "reset", actionDrift}))
	})
})
