package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	uiFieldConfirm  = "confirm"
	uiPathSetDelete = "/ui/sets/delete"
	uiPathSetImport = "/ui/sets/import"
	uiPathStateImp  = "/ui/state/import"
	uiSetsID        = "sets"
	uiFieldSet      = "set"
	uiSwapOOB       = `hx-swap-oob="true"`
	uiSetYAML       = "rules:\n- {id: in-set, enabled: true, target: {host: upstream}, effects: {status: 503}}\nusers:\n- {name: alice, password: pw}\n"
)

// uiAddSet imports a rule and a user into the set and creates a rule outside
// it, whose id it returns.
func uiAddSet(e *uiEnv, set string) string {
	GinkgoHelper()
	loose, err := e.svc.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 429}})
	Expect(err).NotTo(HaveOccurred())
	Expect(e.svc.ImportSet(set, []byte(uiSetYAML))).To(Succeed())
	return loose.ID
}

var _ = Describe("UI state", func() {
	It("lists the sets with their object counts", func() {
		e := newUIEnv()
		Expect(e.svc.ImportSet(setBase, []byte(uiSetYAML))).To(Succeed())
		rec := e.get("/state")
		uiExpectPage(rec)
		body := rec.Body.String()
		Expect(uiAttrs(body, "data-set")).To(Equal([]string{setBase}))
		Expect(uiAttrs(body, "data-objects")).To(Equal([]string{"2"}))
		Expect(uiAttrs(body, "href")).To(ContainElement("/api/v1/export?set=" + setBase))
	})

	It("deletes a set and every object in it", func() {
		e := newUIEnv()
		loose := uiAddSet(e, setBase)
		rec := e.post(uiPathSetDelete, url.Values{formName: {setBase}})
		uiExpectFragment(rec, uiSetsID)
		Expect(uiAttrs(rec.Body.String(), "data-set")).To(BeEmpty())
		Expect(e.svc.Sets()).To(BeEmpty())
		Expect(ruleIDs(e.svc.ListRules())).To(Equal([]string{loose}))
		Expect(e.svc.ListUsers()).To(BeEmpty())
	})

	DescribeTable("refuses the set deletion and changes nothing",
		func(name string) {
			e := newUIEnv()
			uiAddSet(e, setBase)
			_, err := e.svc.CreateGrant(api.Grant{Host: upstreamHost, Repo: repoGlob, Actions: []api.Action{api.ActionPull}, Users: []string{"alice"}})
			Expect(err).NotTo(HaveOccurred())
			before := uiSnapshot(e.svc)
			uiExpectError(e.post(uiPathSetDelete, url.Values{formName: {name}}))
			Expect(uiSnapshot(e.svc)).To(Equal(before))
		},
		Entry("an unknown set", "nope"),
		Entry("a grant outside the set naming a user inside it", setBase),
	)

	It("merges a YAML document into a set", func() {
		e := newUIEnv()
		loose, err := e.svc.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 429}})
		Expect(err).NotTo(HaveOccurred())
		rec := e.post(uiPathSetImport, url.Values{uiFieldSet: {setAdd}, uiFieldYAML: {uiSetYAML}})
		Expect(rec.Code).To(Equal(200))
		Expect(rec.Header().Get(uiHdrRetarget)).To(Equal(uiMsgTarget))
		Expect(rec.Header().Get(uiHdrReswap)).To(Equal("innerHTML"))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(uiClassOK))
		Expect(body).To(ContainSubstring(`id="sets"`))
		Expect(body).To(ContainSubstring(uiSwapOOB))
		Expect(uiAttrs(body, "data-set")).To(Equal([]string{setAdd}))
		rules := e.svc.ListRules()
		Expect(ruleIDs(rules)).To(Equal([]string{"in-set", loose.ID}))
		Expect(rules[0].Set).To(Equal(setAdd))
		Expect(rules[1].Set).To(BeEmpty())
	})

	DescribeTable("refuses the merge and changes nothing",
		func(set, doc string) {
			e := newUIEnv()
			uiAddSet(e, setBase)
			before := uiSnapshot(e.svc)
			uiExpectError(e.post(uiPathSetImport, url.Values{uiFieldSet: {set}, uiFieldYAML: {doc}}))
			Expect(uiSnapshot(e.svc)).To(Equal(before))
		},
		Entry("an empty set name", "", uiSetYAML),
		Entry("a set name with a space", "bad name!", uiSetYAML),
		Entry("unreadable YAML", setAdd, "rules: [not, valid"),
		Entry("a grant naming an unknown user", setAdd, "grants:\n- {host: upstream, repo: '**', actions: [pull], users: [ghost]}\n"),
	)

	DescribeTable("refuses the whole-state replace and changes nothing",
		func(doc string) {
			e := newUIEnv()
			uiAddSet(e, setAdd)
			before := uiSnapshot(e.svc)
			uiExpectError(e.post(uiPathStateImp, url.Values{uiFieldYAML: {doc}}))
			Expect(uiSnapshot(e.svc)).To(Equal(before))
		},
		Entry("unreadable YAML", "rules: [not, valid"),
		Entry("an empty document", ""),
		Entry("a blank document", " \n\t\n"),
	)

	It("validates the replace button before it posts", func() {
		tags := uiTags(newUIEnv().get("/state").Body.String(), `hx-post="`+uiPathStateImp+`"`)
		Expect(tags).To(HaveLen(1))
		Expect(uiTagAttrs(tags[0])).To(HaveKeyWithValue("hx-validate", "true"))
	})

	Describe("purge everything", func() {
		const path = "/ui/state/purge"

		It("offers a Reset section with an unchecked backends box, a word field and no hx-confirm", func() {
			body := newUIEnv().get("/state").Body.String()
			Expect(body).To(ContainSubstring("<h2>Reset</h2>"))
			forms := uiTags(body, uiHxPost+path+`"`)
			Expect(forms).To(HaveLen(1))
			Expect(uiTagAttrs(forms[0])).NotTo(HaveKey("hx-confirm"))
			boxes := uiTags(body, `name="backends"`)
			Expect(boxes).To(HaveLen(1))
			attrs := uiTagAttrs(boxes[0])
			Expect(attrs).To(HaveKeyWithValue("type", "checkbox"))
			Expect(attrs).NotTo(HaveKey("checked"))
			Expect(uiTags(body, `name="confirm"`)).To(HaveLen(1))
			Expect(body).To(ContainSubstring("Purge everything"))
		})

		DescribeTable("refuses a missing or wrong word and changes nothing",
			func(form url.Values) {
				e := newUIEnv()
				uiAddSet(e, setBase)
				e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Kind: api.KindPing, Status: 200})
				before := uiSnapshot(e.svc)
				uiExpectError(e.post(path, form))
				Expect(uiSnapshot(e.svc)).To(Equal(before))
				Expect(e.log.Stats()).NotTo(BeEmpty())
				Expect(e.log.Clear()).To(Equal(1))
			},
			Entry("no word", url.Values{}),
			Entry("an empty word", url.Values{uiFieldConfirm: {""}}),
			Entry("a wrong word", url.Values{uiFieldConfirm: {"yes"}}),
			Entry("the word in another case", url.Values{uiFieldConfirm: {"PURGE"}}),
		)

		It("purges everything, tells so in #msg and refreshes the sets out of band", func() {
			e := newUIEnv()
			uiAddSet(e, setBase)
			e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Kind: api.KindPing, Status: 200})
			rec := e.post(path, url.Values{uiFieldConfirm: {" purge "}})
			Expect(rec.Code).To(Equal(200))
			Expect(rec.Header().Get(uiHdrRetarget)).To(Equal(uiMsgTarget))
			Expect(rec.Header().Get(uiHdrReswap)).To(Equal("innerHTML"))
			body := rec.Body.String()
			Expect(body).To(ContainSubstring(uiClassOK))
			Expect(body).To(ContainSubstring("Purged 2 rules"))
			Expect(body).To(ContainSubstring("1 requests"))
			Expect(body).To(ContainSubstring(`id="sets"`))
			Expect(body).To(ContainSubstring(uiSwapOOB))
			Expect(uiAttrs(body, "data-set")).To(BeEmpty())
			Expect(body).To(ContainSubstring(`id="purge-word"`))
			Expect(e.svc.ListRules()).To(BeEmpty())
			Expect(e.svc.Sets()).To(BeEmpty())
			Expect(e.log.Stats()).To(BeEmpty())
		})

		It("purges the backends only when the box is ticked, and shows a failed backend", func() {
			e := newUIEnv()
			ctx := context.Background()
			Expect(e.svc.DestPushSynthetic(ctx, mirrorHost, "acme/a", "t1", "", 0)).To(Succeed())
			rec := e.post(path, url.Values{uiFieldConfirm: {purgeWord}})
			Expect(rec.Body.String()).NotTo(ContainSubstring("backends"))
			repos, err := e.svc.Inventory(ctx, mirrorHost)
			Expect(err).NotTo(HaveOccurred())
			Expect(repos[0].Tags).NotTo(BeEmpty())

			rec = e.post(path, url.Values{uiFieldConfirm: {purgeWord}, "backends": {"1"}})
			Expect(rec.Body.String()).To(ContainSubstring("Deleted 1 tags on 1 backends"))
			repos, err = e.svc.Inventory(ctx, mirrorHost)
			Expect(err).NotTo(HaveOccurred())
			Expect(repos[0].Tags).To(BeEmpty())
		})

		It("warns about a failing backend under the summary", func() {
			dead := testutil.NewBackend()
			deadURL := dead.URL
			dead.Close()
			svc, _, _ := newService(purgeAllConfig(testerconfig.ProxyHost{Name: mirrorHost, Backend: deadURL}))
			h := NewUI(svc, reqlog.New(10))
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(url.Values{uiFieldConfirm: {purgeWord}, "backends": {"1"}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(200))
			Expect(rec.Body.String()).To(ContainSubstring(uiClassWarn))
			Expect(rec.Body.String()).To(ContainSubstring("Backend " + deadURL))
		})
	})

	It("replaces the whole settings and refreshes the sets", func() {
		e := newUIEnv()
		r, err := e.svc.CreateRule(api.Rule{Enabled: true, Target: api.Target{Host: upstreamHost}, Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		exported, err := e.svc.Export()
		Expect(err).NotTo(HaveOccurred())
		Expect(e.svc.DeleteRule(r.ID)).To(Succeed())
		Expect(e.svc.ImportSet(setBase, []byte(uiSetYAML))).To(Succeed())
		rec := e.post(uiPathStateImp, url.Values{uiFieldYAML: {string(exported)}})
		Expect(rec.Code).To(Equal(200))
		Expect(rec.Header().Get(uiHdrRetarget)).To(Equal(uiMsgTarget))
		Expect(rec.Body.String()).To(ContainSubstring(uiClassOK))
		Expect(rec.Body.String()).To(ContainSubstring(uiSwapOOB))
		Expect(rec.Body.String()).To(ContainSubstring(`id="sets"`))
		Expect(uiAttrs(rec.Body.String(), "data-set")).To(BeEmpty())
		Expect(ruleIDs(e.svc.ListRules())).To(Equal([]string{r.ID}))
		Expect(e.svc.Sets()).To(BeEmpty())
	})
})
