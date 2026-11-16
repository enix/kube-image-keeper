package admin

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// uiThreeRules stores three rules on the upstream host and returns them in
// order: 503, 429, 404.
func uiThreeRules(e *uiEnv) []api.Rule {
	GinkgoHelper()
	for _, c := range []int{404, 429, 503} { // created on top
		_, err := e.svc.CreateRule(api.Rule{Enabled: true, Target: api.Target{Host: upstreamHost}, Effects: api.Effects{Status: c}})
		Expect(err).NotTo(HaveOccurred())
	}
	return e.svc.ListRules()
}

const (
	uiSetName = "kt-ui"
	uiBadSet  = "bad name!"
)

// uiRoundTrip stores the rule (user bob and quota q1 exist), opens its edit
// form and saves it unchanged; it returns the rules before and after.
func uiRoundTrip(rule api.Rule) (e *uiEnv, id string, before []api.Rule) {
	GinkgoHelper()
	e = newUIEnv()
	uiAddBob(e)
	uiAddQuota(e)
	created, err := e.svc.CreateRule(rule)
	Expect(err).NotTo(HaveOccurred())
	before = e.svc.ListRules()
	return e, created.ID, before
}

func uiRuleForm(status string) url.Values {
	return url.Values{formHost: {upstreamHost}, formStatus: {status}, fieldEnabled: {"on"}}
}

var _ = Describe("UI rules", func() {
	It("refuses an invalid rule and creates no rule", func() {
		e := newUIEnv()
		uiExpectError(e.post(uiPathRules, uiRuleForm(uiNumber42)))
		Expect(e.svc.ListRules()).To(BeEmpty())
	})

	DescribeTable("refuses a rule with a non-numeric value",
		func(field, value string) {
			e := newUIEnv()
			form := uiRuleForm(uiNumber503)
			form.Set(field, value)
			uiExpectError(e.post(uiPathRules, form))
			Expect(e.svc.ListRules()).To(BeEmpty())
		},
		Entry("status", formStatus, "abc"),
		Entry("latency", uiFieldLatency, "1.5"),
		Entry("set name", uiFieldSet, uiBadSet),
	)

	It("creates a rule in a set and shows it in the table", func() {
		e := newUIEnv()
		form := uiRuleForm(uiNumber503)
		form.Set(uiFieldSet, uiSetName)
		uiExpectFragment(e.post(uiPathRules, form), uiRulesList)
		Expect(e.svc.Sets()).To(Equal([]api.SetInfo{{Name: uiSetName, Objects: 1}}))
		Expect(uiAttrs(e.get("/rules").Body.String(), "data-set")).To(ConsistOf(uiSetName))
	})

	DescribeTable("saves an unchanged edit form as the same rule",
		func(rule api.Rule) {
			e, id, before := uiRoundTrip(rule)
			form := uiFormValues(e.get("/ui/rules/" + id + "/edit").Body.String())
			uiExpectFragment(e.post(uiPathRules+"/"+id, form), uiRulesList)
			Expect(e.svc.ListRules()).To(Equal(before))
		},
		Entry("a rule of a set with every target field and effect", api.Rule{
			Set: setBase, Enabled: true, Note: "kept",
			Target:  api.Target{Host: upstreamHost, Repo: repoFoo, Tag: uiTagT1, Caller: bob, Kinds: []api.Kind{api.KindPing, api.KindBlob}, Methods: []string{http.MethodGet, http.MethodHead}},
			Effects: api.Effects{Status: 503, Headers: map[string]string{"X-A": "b", "X-C": "d"}},
		}),
		Entry("a disabled rule with latency and a quota", api.Rule{
			Target: api.Target{Host: upstreamHost}, Effects: api.Effects{LatencyMS: 20, Quota: uiQuotaName},
		}),
		Entry("a pass rule", api.Rule{Enabled: true, Target: api.Target{Host: upstreamHost}, Effects: api.Effects{Pass: true}}),
		Entry("a reset rule without a set", api.Rule{Enabled: true, Target: api.Target{Repo: repoGlob}, Effects: api.Effects{Reset: true}}),
	)

	It("keeps an edited rule in its set, so deleting the set removes it", func() {
		e, id, _ := uiRoundTrip(api.Rule{Set: setBase, Enabled: true, Target: api.Target{Host: upstreamHost}, Effects: api.Effects{Status: 503}})
		form := uiFormValues(e.get("/ui/rules/" + id + "/edit").Body.String())
		form.Set(formHost, "alt1")
		uiExpectFragment(e.post(uiPathRules+"/"+id, form), uiRulesList)
		Expect(e.svc.ListRules()[0].Target.Host).To(Equal("alt1"))
		Expect(e.svc.DeleteSet(setBase)).To(Succeed())
		Expect(e.svc.ListRules()).To(BeEmpty())
	})

	It("takes a rule out of its set when the set field is cleared", func() {
		e, id, _ := uiRoundTrip(api.Rule{Set: setBase, Enabled: true, Target: api.Target{Host: upstreamHost}, Effects: api.Effects{Status: 503}})
		form := uiFormValues(e.get("/ui/rules/" + id + "/edit").Body.String())
		form.Set(uiFieldSet, "")
		uiExpectFragment(e.post(uiPathRules+"/"+id, form), uiRulesList)
		Expect(e.svc.ListRules()[0].Set).To(BeEmpty())
		Expect(e.svc.Sets()).To(BeEmpty())
	})

	It("creates a valid rule and answers the rules list", func() {
		e := newUIEnv()
		form := uiRuleForm(uiNumber503)
		form[formKind] = []string{uiKindPing, uiKindBlob}
		form.Set(uiFieldHeaders, uiHeaderLines)
		uiExpectFragment(e.post(uiPathRules, form), uiRulesList)
		rs := e.svc.ListRules()
		Expect(rs).To(HaveLen(1))
		Expect(rs[0].Target.Kinds).To(HaveLen(2))
		Expect(rs[0].Effects.Headers).To(HaveKeyWithValue("X-C", "d"))
		Expect(rs[0].Enabled).To(BeTrue())
	})

	It("moves a rule and answers the rules list", func() {
		e := newUIEnv()
		rs := uiThreeRules(e)
		uiExpectFragment(e.post(uiPathRules+"/move", url.Values{formID: {rs[2].ID}, "dir": {"up"}}), uiRulesList)
		Expect(ruleIDs(e.svc.ListRules())).To(Equal([]string{rs[0].ID, rs[2].ID, rs[1].ID}))
	})

	It("toggles a rule and answers the rules list", func() {
		e := newUIEnv()
		rs := uiThreeRules(e)
		uiExpectFragment(e.post(uiPathRules+"/toggle", url.Values{formID: {rs[0].ID}, fieldEnabled: {"false"}}), uiRulesList)
		Expect(e.svc.ListRules()[0].Enabled).To(BeFalse())
	})

	It("deletes a rule and answers the rules list", func() {
		e := newUIEnv()
		rs := uiThreeRules(e)
		uiExpectFragment(e.post(uiPathRules+"/delete", url.Values{formID: {rs[0].ID}}), uiRulesList)
		Expect(ruleIDs(e.svc.ListRules())).To(Equal([]string{rs[1].ID, rs[2].ID}))
	})

	It("answers a table action without the draft form", func() {
		e := newUIEnv()
		rs := uiThreeRules(e)
		body := e.post(uiPathRules+"/toggle", url.Values{formID: {rs[0].ID}, fieldEnabled: {"false"}}).Body.String()
		Expect(body).NotTo(ContainSubstring(uiRuleFormID))
	})

	It("warns about a rule that targets the anonymous caller", func() {
		e := newUIEnv()
		_, err := e.svc.CreateRule(api.Rule{Enabled: true, Target: api.Target{Caller: anonymous}, Effects: api.Effects{Status: 503}})
		Expect(err).NotTo(HaveOccurred())
		id := e.svc.ListRules()[0].ID
		row := regexp.MustCompile(`(?s)<tr data-rule="` + id + `".*?</tr>`).FindString(e.get("/rules").Body.String())
		Expect(row).NotTo(BeEmpty())
		Expect(strings.Count(row, uiClassWarn)).To(Equal(1))
	})

	It("offers an empty create form", func() {
		rec := newUIEnv().get("/ui/rules/form")
		Expect(rec.Code).To(Equal(200))
		Expect(uiTags(rec.Body.String(), `data-mode="new"`)).To(HaveLen(1))
	})

	It("shows a rule back in its edit form", func() {
		e := newUIEnv()
		rs := uiThreeRules(e)
		id := rs[1].ID
		body := e.get("/ui/rules/" + id + "/edit").Body.String()
		Expect(uiTags(body, `data-mode="edit"`)).To(HaveLen(1))
		Expect(uiAttrs(body, "data-rule")).To(ConsistOf(id))
		Expect(uiAttrs(body, "hx-post")).To(ContainElement(uiPathRules + "/" + id))
		form := uiFormValues(body)
		Expect(form.Get(formHost)).To(Equal(upstreamHost))
		Expect(form.Get(formStatus)).To(Equal("429"))
	})

	It("edits a rule in place and answers the list with the create form", func() {
		e := newUIEnv()
		rs := uiThreeRules(e)
		id := rs[1].ID
		rec := e.post(uiPathRules+"/"+id, url.Values{
			formHost: {"alt1"}, formRepo: {repoGlob}, formKind: {uiKindPing, uiKindBlob}, uiFieldMethods: {lowerGet}, formStatus: {"502"}, uiFieldLatency: {"20"},
			uiFieldHeaders: {uiHeaderLines}, uiFieldNote: {"edited"}, fieldEnabled: {"on"},
		})
		uiExpectFragment(rec, uiRulesList)
		forms := uiTags(rec.Body.String(), `data-mode="new"`)
		Expect(forms).To(HaveLen(1))
		Expect(forms[0]).To(ContainSubstring("hx-swap-oob"))
		got := e.svc.ListRules()
		Expect(ruleIDs(got)).To(Equal(ruleIDs(rs)))
		r := got[1]
		Expect(r.Target.Host).To(Equal("alt1"))
		Expect(r.Target.Repo).To(Equal(repoGlob))
		Expect(r.Target.Kinds).To(HaveLen(2))
		Expect(r.Target.Methods).To(Equal([]string{http.MethodGet}))
		Expect(r.Effects.Status).To(Equal(502))
		Expect(r.Effects.LatencyMS).To(Equal(20))
		Expect(r.Effects.Headers).To(HaveKeyWithValue("X-C", "d"))
		Expect(r.Note).To(Equal("edited"))
		Expect(r.Enabled).To(BeTrue())

		form := uiFormValues(e.get("/ui/rules/" + id + "/edit").Body.String())
		Expect(form.Get(uiFieldNote)).To(Equal("edited"))
		Expect(form.Get(uiFieldHeaders)).To(ContainSubstring("X-C: d"))
	})

	It("refuses an invalid edit and changes nothing", func() {
		e := newUIEnv()
		rs := uiThreeRules(e)
		before := e.svc.ListRules()
		uiExpectError(e.post(uiPathRules+"/"+rs[1].ID, uiRuleForm(uiNumber42)))
		Expect(e.svc.ListRules()).To(Equal(before))
	})

	It("reports an unknown rule and changes nothing", func() {
		e := newUIEnv()
		uiThreeRules(e)
		before := e.svc.ListRules()
		uiExpectError(e.post(uiPathRules+"/nope", uiRuleForm(uiNumber503)))
		Expect(e.svc.ListRules()).To(Equal(before))
	})
})
