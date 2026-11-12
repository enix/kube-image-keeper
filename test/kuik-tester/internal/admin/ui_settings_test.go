package admin

import (
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// uiMsgNotFound is the error of an operation on an unknown object.
const uiMsgNotFound = "not found"

// uiSettingsCase is one POST to /ui/settings/<path>: setup prepares the state
// and returns the key the form needs, check asserts the effect.
type uiSettingsCase struct {
	path  string
	setup func(e *uiEnv) string
	form  func(key string) url.Values
	check func(e *uiEnv)
	// wantMsg is a substring of the error a refused operation shows.
	wantMsg string
}

func uiForm(kv ...string) func(string) url.Values {
	return func(string) url.Values {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Add(kv[i], kv[i+1])
		}
		return v
	}
}

func uiKeyForm(key string) url.Values { return url.Values{formKey: {key}} }

func uiAddBob(e *uiEnv) string {
	GinkgoHelper()
	Expect(e.svc.PutUser(api.User{Name: bob, Password: "pw"})).To(Succeed())
	return bob
}

func uiAddGrant(e *uiEnv) string {
	GinkgoHelper()
	uiAddBob(e)
	g, err := e.svc.CreateGrant(api.Grant{Host: upstreamHost, Repo: repoGlob, Actions: []api.Action{api.ActionPull}, Users: []string{bob}})
	Expect(err).NotTo(HaveOccurred())
	return g.ID
}

func uiAddQuota(e *uiEnv) string {
	GinkgoHelper()
	Expect(e.svc.PutQuota(uiQuotaDef())).To(Succeed())
	return uiQuotaName
}

func uiQuotaDef() api.QuotaDef {
	return api.QuotaDef{Name: uiQuotaName, Limit: 5, WindowSec: 60, Key: keyGlobal}
}

func uiAddAlias(e *uiEnv) string {
	GinkgoHelper()
	a, err := e.svc.CreateAlias(api.Alias{From: "mirror/acme", To: "acme"})
	Expect(err).NotTo(HaveOccurred())
	return a.ID
}

func uiAddDivergent(e *uiEnv) string {
	GinkgoHelper()
	d, err := e.svc.CreateDivergent(api.Divergent{Host: upstreamHost, Salt: "s1"})
	Expect(err).NotTo(HaveOccurred())
	return d.ID
}

func uiAddHostAuth(e *uiEnv) string {
	GinkgoHelper()
	Expect(e.svc.PutHostAuth(api.HostAuth{Host: upstreamHost, Scheme: uiSchemeBasic})).To(Succeed())
	return upstreamHost
}

func uiAddPlatform(e *uiEnv) string {
	GinkgoHelper()
	p, err := e.svc.CreatePlatform(api.PlatformRule{Host: upstreamHost, Repo: repoGlob, Platforms: []string{platAMD64}})
	Expect(err).NotTo(HaveOccurred())
	return p.ID
}

func uiUsedQuota(e *uiEnv) string {
	GinkgoHelper()
	uiAddQuota(e)
	_, ok := e.q.Take(uiQuotaDef(), "", time.Now())
	Expect(ok).To(BeTrue())
	Expect(e.svc.QuotaUsage()).To(HaveKey(uiQuotaName))
	return uiQuotaName
}

var _ = Describe("UI settings", func() {
	DescribeTable("applies the settings operation",
		func(c uiSettingsCase) {
			e := newUIEnv()
			key := ""
			if c.setup != nil {
				key = c.setup(e)
			}
			kind, _, _ := strings.Cut(c.path, "/")
			uiExpectFragment(e.post(uiPathSettings+c.path, c.form(key)), "s-"+kind)
			c.check(e)
		},
		Entry("creates a user", uiSettingsCase{
			path: uiOpUsersPut, form: uiForm(formName, bob, uiFieldPass, "pw"),
			check: func(e *uiEnv) { Expect(e.svc.ListUsers()).To(ConsistOf(api.User{Name: bob, Password: "pw"})) },
		}),
		Entry("replaces the password of a user", uiSettingsCase{
			path: uiOpUsersPut, setup: uiAddBob, form: uiForm(formName, bob, uiFieldPass, "other"),
			check: func(e *uiEnv) { Expect(e.svc.ListUsers()).To(ConsistOf(api.User{Name: bob, Password: "other"})) },
		}),
		Entry("deletes a user", uiSettingsCase{
			path: uiOpUsersDelete, setup: uiAddBob, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.ListUsers()).To(BeEmpty()) },
		}),
		Entry("creates a grant", uiSettingsCase{
			path: uiOpGrantsCreate, setup: uiAddBob,
			form: uiForm(formHost, upstreamHost, formRepo, repoGlob, formAction, uiActionPull, formUsers, bob),
			check: func(e *uiEnv) {
				gs := e.svc.ListGrants()
				Expect(gs).To(HaveLen(1))
				Expect(gs[0].Actions).To(Equal([]api.Action{api.ActionPull}))
				Expect(gs[0].Users).To(Equal([]string{bob}))
			},
		}),
		Entry("deletes a grant", uiSettingsCase{
			path: "grants/delete", setup: uiAddGrant, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.ListGrants()).To(BeEmpty()) },
		}),
		Entry("sets a bearer host auth", uiSettingsCase{
			path: uiOpHostAuthPut, form: uiForm(formHost, upstreamHost, uiFieldScheme, "bearer"),
			check: func(e *uiEnv) {
				Expect(e.svc.ListHostAuth()).To(ConsistOf(api.HostAuth{Host: upstreamHost, Scheme: api.SchemeBearer}))
			},
		}),
		Entry("deletes a host auth", uiSettingsCase{
			path: "hostauth/delete", setup: uiAddHostAuth, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.ListHostAuth()).To(BeEmpty()) },
		}),
		Entry("creates an alias", uiSettingsCase{
			path: uiOpAliasesCreate, form: uiForm("from", "mirror/acme", "to", "acme"),
			check: func(e *uiEnv) {
				as := e.svc.ListAliases()
				Expect(as).To(HaveLen(1))
				Expect(as[0].From).To(Equal("mirror/acme"))
				Expect(as[0].To).To(Equal("acme"))
			},
		}),
		Entry("deletes an alias", uiSettingsCase{
			path: "aliases/delete", setup: uiAddAlias, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.ListAliases()).To(BeEmpty()) },
		}),
		Entry("creates a divergent", uiSettingsCase{
			path: uiOpDivergentsCreate, form: uiForm(formHost, upstreamHost, "salt", "s1"),
			check: func(e *uiEnv) {
				ds := e.svc.ListDivergents()
				Expect(ds).To(HaveLen(1))
				Expect(ds[0].Salt).To(Equal("s1"))
			},
		}),
		Entry("deletes a divergent", uiSettingsCase{
			path: "divergents/delete", setup: uiAddDivergent, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.ListDivergents()).To(BeEmpty()) },
		}),
		Entry("creates a platform rule", uiSettingsCase{
			path: uiOpPlatformsCreate,
			form: uiForm(formHost, upstreamHost, formRepo, repoGlob, uiFieldPlatform, platAMD64, uiFieldSet, uiSetName),
			check: func(e *uiEnv) {
				ps := e.svc.ListPlatforms()
				Expect(ps).To(HaveLen(1))
				Expect(ps[0].Platforms).To(Equal([]string{platAMD64}))
				Expect(ps[0].Set).To(Equal(uiSetName))
			},
		}),
		Entry("deletes a platform rule", uiSettingsCase{
			path: uiOpPlatformsDelete, setup: uiAddPlatform, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.ListPlatforms()).To(BeEmpty()) },
		}),
		Entry("sets a quota", uiSettingsCase{
			path: uiOpQuotasPut,
			form: func(string) url.Values {
				return url.Values{formName: {uiQuotaName}, uiFieldLimit: {"5"}, uiFieldWindow: {"60"}, formKey: {"ip"}, formKind: {uiKindPing, uiKindBlob}, uiFieldMethods: {"GET, HEAD"}}
			},
			check: func(e *uiEnv) {
				Expect(e.svc.ListQuotas()).To(ConsistOf(api.QuotaDef{
					Name: uiQuotaName, Limit: 5, WindowSec: 60, Key: "ip",
					Kinds: []api.Kind{api.KindPing, api.KindBlob}, Methods: []string{"GET", "HEAD"},
				}))
			},
		}),
		Entry("deletes a quota", uiSettingsCase{
			path: "quotas/delete", setup: uiAddQuota, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.ListQuotas()).To(BeEmpty()) },
		}),
		Entry("resets the counters of a quota", uiSettingsCase{
			path: "quotas/reset", setup: uiUsedQuota, form: uiKeyForm,
			check: func(e *uiEnv) { Expect(e.svc.QuotaUsage()).NotTo(HaveKey(uiQuotaName)) },
		}),
	)

	DescribeTable("refuses the settings operation and changes nothing",
		func(c uiSettingsCase) {
			e := newUIEnv()
			key := ""
			if c.setup != nil {
				key = c.setup(e)
			}
			before := uiSnapshot(e.svc)
			rec := e.post(uiPathSettings+c.path, c.form(key))
			uiExpectError(rec)
			Expect(c.wantMsg).NotTo(BeEmpty())
			Expect(rec.Body.String()).To(ContainSubstring(c.wantMsg))
			Expect(uiSnapshot(e.svc)).To(Equal(before))
		},
		Entry("an unknown operation", uiSettingsCase{path: "users/rename", form: uiForm(formKey, bob), wantMsg: "unknown operation"}),
		Entry("an unknown kind", uiSettingsCase{path: "nope/put", form: uiForm(formKey, bob), wantMsg: "unknown operation"}),
		Entry("a user with a bad set name", uiSettingsCase{path: uiOpUsersPut, form: uiForm(formName, bob, uiFieldPass, "pw", uiFieldSet, uiBadSet), wantMsg: "set name"}),
		Entry("a user without password", uiSettingsCase{path: uiOpUsersPut, form: uiForm(formName, bob), wantMsg: "empty password"}),
		Entry("the delete of an unknown user", uiSettingsCase{path: uiOpUsersDelete, form: uiForm(formKey, "nobody"), wantMsg: uiMsgNotFound}),
		Entry("the delete of a user a grant names", uiSettingsCase{
			path: uiOpUsersDelete, setup: func(e *uiEnv) string { uiAddGrant(e); return bob }, form: uiKeyForm,
			wantMsg: "unknown user",
		}),
		Entry("a grant for an unknown user", uiSettingsCase{
			path: uiOpGrantsCreate, form: uiForm(formHost, upstreamHost, formAction, uiActionPull, formUsers, "nobody"),
			wantMsg: "unknown user",
		}),
		Entry("a grant without action", uiSettingsCase{
			path: uiOpGrantsCreate, setup: uiAddBob, form: uiForm(formHost, upstreamHost, formUsers, bob),
			wantMsg: "actions and users must not be empty",
		}),
		Entry("a grant without users", uiSettingsCase{
			path: uiOpGrantsCreate, form: uiForm(formHost, upstreamHost, formAction, uiActionPull),
			wantMsg: "actions and users must not be empty",
		}),
		Entry("a host auth with an unknown scheme", uiSettingsCase{
			path: uiOpHostAuthPut, form: uiForm(formHost, upstreamHost, uiFieldScheme, "digest"),
			wantMsg: "scheme must be basic or bearer",
		}),
		Entry("an alias with a glob source", uiSettingsCase{
			path: uiOpAliasesCreate, form: uiForm("from", repoGlob, "to", "acme"),
			wantMsg: "literal repository paths",
		}),
		Entry("a divergent without salt", uiSettingsCase{
			path: uiOpDivergentsCreate, form: uiForm(formHost, upstreamHost),
			wantMsg: "empty salt",
		}),
		Entry("a platform rule without platform", uiSettingsCase{
			path: uiOpPlatformsCreate, form: uiForm(formHost, upstreamHost, formRepo, repoGlob),
			wantMsg: "at least one platform is required",
		}),
		Entry("a platform rule with an unknown platform", uiSettingsCase{
			path: uiOpPlatformsCreate, form: uiForm(formHost, upstreamHost, uiFieldPlatform, "linux/s390x"),
			wantMsg: "unknown platform",
		}),
		Entry("the delete of an unknown platform rule", uiSettingsCase{path: uiOpPlatformsDelete, form: uiForm(formKey, "nobody"), wantMsg: uiMsgNotFound}),
		Entry("a quota with a non-numeric limit", uiSettingsCase{
			path: uiOpQuotasPut, form: uiForm(formName, uiQuotaName, uiFieldLimit, "abc", uiFieldWindow, "60", formKey, keyGlobal),
			wantMsg: "is not a number",
		}),
		Entry("a quota with a fractional window", uiSettingsCase{
			path: uiOpQuotasPut, form: uiForm(formName, uiQuotaName, uiFieldLimit, "5", uiFieldWindow, "1.5", formKey, keyGlobal),
			wantMsg: "is not a number",
		}),
		Entry("the delete of a quota a rule names", uiSettingsCase{
			path: "quotas/delete", form: uiKeyForm,
			setup: func(e *uiEnv) string {
				uiAddQuota(e)
				_, err := e.svc.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Quota: uiQuotaName}})
				Expect(err).NotTo(HaveOccurred())
				return uiQuotaName
			},
			wantMsg: "unknown quota",
		}),
		Entry("the reset of an unknown quota", uiSettingsCase{path: "quotas/reset", form: uiForm(formKey, "nobody"), wantMsg: uiMsgNotFound}),
	)

	DescribeTable("tags the created settings object with the set",
		func(c uiSettingsCase) {
			e := newUIEnv()
			key := ""
			if c.setup != nil {
				key = c.setup(e)
			}
			form := c.form(key)
			form.Set(uiFieldSet, uiSetName)
			kind, _, _ := strings.Cut(c.path, "/")
			uiExpectFragment(e.post(uiPathSettings+c.path, form), "s-"+kind)
			c.check(e)
		},
		Entry("a user", uiSettingsCase{
			path: uiOpUsersPut, form: uiForm(formName, bob, uiFieldPass, "pw"),
			check: func(e *uiEnv) { Expect(e.svc.ListUsers()[0].Set).To(Equal(uiSetName)) },
		}),
		Entry("a grant", uiSettingsCase{
			path: uiOpGrantsCreate, setup: uiAddBob, form: uiForm(formHost, upstreamHost, formAction, uiActionPull, formUsers, bob),
			check: func(e *uiEnv) { Expect(e.svc.ListGrants()[0].Set).To(Equal(uiSetName)) },
		}),
		Entry("a host auth", uiSettingsCase{
			path: uiOpHostAuthPut, form: uiForm(formHost, upstreamHost, uiFieldScheme, uiSchemeBasic),
			check: func(e *uiEnv) { Expect(e.svc.ListHostAuth()[0].Set).To(Equal(uiSetName)) },
		}),
		Entry("an alias", uiSettingsCase{
			path: uiOpAliasesCreate, form: uiForm("from", "mirror/acme", "to", "acme"),
			check: func(e *uiEnv) { Expect(e.svc.ListAliases()[0].Set).To(Equal(uiSetName)) },
		}),
		Entry("a divergent", uiSettingsCase{
			path: uiOpDivergentsCreate, form: uiForm(formHost, upstreamHost, "salt", "s1"),
			check: func(e *uiEnv) { Expect(e.svc.ListDivergents()[0].Set).To(Equal(uiSetName)) },
		}),
		Entry("a quota", uiSettingsCase{
			path: uiOpQuotasPut, form: uiForm(formName, uiQuotaName, uiFieldLimit, "5", uiFieldWindow, "60", formKey, keyGlobal),
			check: func(e *uiEnv) { Expect(e.svc.ListQuotas()[0].Set).To(Equal(uiSetName)) },
		}),
	)

	It("polls the quota usage with a fragment", func() {
		e := newUIEnv()
		uiAddQuota(e)
		Expect(uiAttrs(e.get("/settings").Body.String(), "hx-get")).To(ContainElement("/ui/settings/quota-usage"))
		rec := e.get("/ui/settings/quota-usage")
		uiExpectFragment(rec, "s-quotas")
		Expect(uiAttrs(rec.Body.String(), "data-key")).To(ConsistOf(uiQuotaName))
		Expect(uiAttrs(rec.Body.String(), "hx-trigger")).To(ContainElement("every 2s"))
	})

	It("offers one checkbox per platform of the synthetic images", func() {
		want := make([]string, 0, len(synth.Platforms))
		for _, p := range synth.Platforms {
			want = append(want, synth.PlatformName(p))
		}
		got := make([]string, 0, len(want))
		for _, tag := range uiTags(newUIEnv().get("/settings").Body.String(), `name="`+uiFieldPlatform+`"`) {
			got = append(got, uiTagAttrs(strings.TrimPrefix(tag, "<input"))["value"])
		}
		Expect(got).To(Equal(want))
	})

	It("answers an action without the create forms", func() {
		rec := newUIEnv().post(uiPathSettings+uiOpUsersPut, url.Values{formName: {bob}, uiFieldPass: {"pw"}})
		Expect(uiAttrs(rec.Body.String(), "hx-post")).NotTo(ContainElement(uiPathSettings + uiOpUsersPut))
	})

	It("lists every object of every kind", func() {
		e := newUIEnv()
		keys := []string{uiAddBob(e), uiAddGrant(e), uiAddHostAuth(e), uiAddAlias(e), uiAddDivergent(e), uiAddPlatform(e), uiAddQuota(e)}
		Expect(uiAttrs(e.get("/settings").Body.String(), "data-key")).To(ConsistOf(keys))
	})
})
