package admin

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	uiHxPost    = `hx-post="`
	uiHxConfirm = "hx-confirm="
	uiLead      = `class="lead"`
	uiDocsURL   = "https://github.com/enix/kube-image-keeper/blob/main/test/kuik-tester/README.md"
	uiRulesHelp = `<details id="rules-help">`
	uiPathDelS  = "/ui/settings/%s/delete"
)

// uiFixture creates one object of every kind the pages offer an action on.
func uiFixture(e *uiEnv) {
	GinkgoHelper()
	Expect(e.svc.PutUser(api.User{Name: bob, Password: "pw"})).To(Succeed())
	_, err := e.svc.CreateGrant(api.Grant{Host: upstreamHost, Repo: repoGlob, Actions: []api.Action{"pull"}, Users: []string{bob}})
	Expect(err).NotTo(HaveOccurred())
	Expect(e.svc.PutHostAuth(api.HostAuth{Host: upstreamHost, Scheme: uiSchemeBasic})).To(Succeed())
	_, err = e.svc.CreateAlias(api.Alias{Host: upstreamHost, From: "alt/" + repoFoo, To: repoFoo})
	Expect(err).NotTo(HaveOccurred())
	_, err = e.svc.CreateDivergent(api.Divergent{Host: upstreamHost, Repo: repoFoo, Salt: "s"})
	Expect(err).NotTo(HaveOccurred())
	_, err = e.svc.CreatePlatform(api.PlatformRule{Host: upstreamHost, Repo: repoFoo, Platforms: []string{platAMD64}})
	Expect(err).NotTo(HaveOccurred())
	Expect(e.svc.PutQuota(api.QuotaDef{Name: uiQuotaName, Limit: 5, WindowSec: 60, Key: keyGlobal})).To(Succeed())
	_, err = e.svc.CreateRule(api.Rule{Enabled: true, Target: api.Target{Host: upstreamHost}, Effects: api.Effects{Quota: uiQuotaName, Pass: true}})
	Expect(err).NotTo(HaveOccurred())
	Expect(e.svc.ImportSet(setBase, []byte(uiSetYAML))).To(Succeed())
	_, err = e.svc.Resolve(upstreamHost, repoFoo, uiTagT1)
	Expect(err).NotTo(HaveOccurred())
	Expect(e.svc.DestPushSynthetic(ctx, mirrorHost, repoFoo, uiTagT1, "", 1)).To(Succeed())
	e.log.Add(api.LogEntry{Host: upstreamHost, Method: "GET", Kind: api.KindManifestByTag, Repo: repoFoo, Status: 200})
}

var _ = Describe("UI explanations", func() {
	DescribeTable("asks for a confirmation before a destructive action",
		func(page, route string) {
			e := newUIEnv()
			uiFixture(e)
			tags := uiTags(e.get(page).Body.String(), uiHxPost+route+`"`)
			Expect(tags).NotTo(BeEmpty(), "no form posts to %s", route)
			for _, t := range tags {
				Expect(t).To(ContainSubstring(uiHxConfirm))
			}
		},
		Entry("rule delete", "/rules", "/ui/rules/delete"),
		Entry("user delete", "/settings", strings.Replace(uiPathDelS, "%s", "users", 1)),
		Entry("grant delete", "/settings", strings.Replace(uiPathDelS, "%s", "grants", 1)),
		Entry("host auth delete", "/settings", strings.Replace(uiPathDelS, "%s", "hostauth", 1)),
		Entry("alias delete", "/settings", strings.Replace(uiPathDelS, "%s", "aliases", 1)),
		Entry("divergent delete", "/settings", strings.Replace(uiPathDelS, "%s", "divergents", 1)),
		Entry("platform rule delete", "/settings", strings.Replace(uiPathDelS, "%s", "platforms", 1)),
		Entry("quota delete", "/settings", strings.Replace(uiPathDelS, "%s", "quotas", 1)),
		Entry("destination tag delete", "/destinations", uiPathDest+"delete-tag"),
		Entry("destination manifest delete", "/destinations", uiPathDest+"delete-manifest"),
		Entry("destination purge", "/destinations", uiPathDest+uiOpPurge),
		Entry("stats reset", requestsPath, "/ui/stats/reset"),
		Entry("set delete", "/state", uiPathSetDelete),
		Entry("whole state replace", "/state", uiPathStateImp),
	)

	It("names the object a settings delete confirmation is about", func() {
		e := newUIEnv()
		uiFixture(e)
		confirms := make([]string, 0, 2)
		for _, t := range uiTags(e.get("/settings").Body.String(), uiHxPost+"/ui/settings/users/delete") {
			confirms = append(confirms, uiTagAttrs(t)[strings.TrimSuffix(uiHxConfirm, "=")])
		}
		Expect(confirms).To(ContainElement(ContainSubstring(bob)))
	})

	DescribeTable("gives every visible form field a hint",
		func(page string) {
			e := newUIEnv()
			uiFixture(e)
			body := e.get(page).Body.String()
			var fields []string
			for _, t := range uiInputRe.FindAllString(body, -1) {
				if !strings.Contains(t, `type="hidden"`) {
					fields = append(fields, t)
				}
			}
			for _, m := range uiTextareaRe.FindAllStringSubmatch(body, -1) {
				fields = append(fields, "<textarea"+m[1]+">")
			}
			for _, m := range uiSelectRe.FindAllStringSubmatch(body, -1) {
				fields = append(fields, "<select"+m[1]+">")
			}
			Expect(fields).NotTo(BeEmpty())
			for _, f := range fields {
				Expect(uiTagAttrs(f)["title"]).NotTo(BeEmpty(), "no title on %s", f)
			}
		},
		Entry("images", "/"),
		Entry("rules", "/rules"),
		Entry("settings", "/settings"),
		Entry("destinations", "/destinations"),
		Entry("requests", requestsPath),
		Entry("state", "/state"),
	)

	DescribeTable("opens every page and section with a lead paragraph",
		func(page string, leads int) {
			Expect(strings.Count(newUIEnv().get(page).Body.String(), uiLead)).To(BeNumerically(">=", leads))
		},
		Entry("images", "/", 3),
		Entry("rules", "/rules", 1),
		Entry("settings: the page and its seven sections", "/settings", 8),
		Entry("destinations", "/destinations", 1),
		Entry("requests", requestsPath, 1),
		Entry("state: the page, the sets and the import", "/state", 3),
	)

	It("explains how the rules are evaluated on the rules page", func() {
		Expect(newUIEnv().get("/rules").Body.String()).To(ContainSubstring(uiRulesHelp))
	})

	It("links the documentation from the navigation, in a new tab", func() {
		body := newUIEnv().get("/").Body.String()
		links := uiTags(body, `href="`+uiDocsURL+`"`)
		Expect(links).To(HaveLen(1))
		a := uiTagAttrs(links[0])
		Expect(a["target"]).To(Equal("_blank"))
		Expect(a["rel"]).To(ContainSubstring("noopener"))
	})
})
