package admin

import (
	"context"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// uiConfirmsOf returns the hx-confirm messages of the forms that post to route.
func uiConfirmsOf(body, route string) []string {
	tags := uiTags(body, uiHxPost+route+`"`)
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, uiTagAttrs(t)["hx-confirm"])
	}
	return out
}

var _ = Describe("UI purge", func() {
	Describe("rules", func() {
		It("offers a confirmed button with the count, and none when empty", func() {
			e := newUIEnv()
			Expect(e.get("/rules").Body.String()).NotTo(ContainSubstring("/ui/rules/purge"))
			for range 2 {
				_, err := e.svc.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(uiConfirmsOf(e.get("/rules").Body.String(), "/ui/rules/purge")).To(ConsistOf("Delete all 2 rules?"))
			_, err := e.svc.PurgeColl(kindRules)
			Expect(err).NotTo(HaveOccurred())
			_, err = e.svc.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
			Expect(uiConfirmsOf(e.get("/rules").Body.String(), "/ui/rules/purge")).To(ConsistOf("Delete all 1 rule?"))
		})

		It("purges and answers the rule table", func() {
			e := newUIEnv()
			for range 2 {
				_, err := e.svc.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
				Expect(err).NotTo(HaveOccurred())
			}
			rec := e.post("/ui/rules/purge", url.Values{})
			uiExpectFragment(rec, uiRulesList)
			Expect(rec.Body.String()).NotTo(ContainSubstring(uiRuleFormID))
			Expect(e.svc.ListRules()).To(BeEmpty())
		})
	})

	Describe("settings", func() {
		DescribeTable("each section offers a confirmed button and purges only its list",
			func(kind, confirm string, add func(e *uiEnv) string) {
				e := newUIEnv()
				Expect(uiConfirmsOf(e.get("/settings").Body.String(), "/ui/settings/"+kind+"/purge")).To(BeEmpty(), "no button on an empty list")
				add(e)
				Expect(uiConfirmsOf(e.get("/settings").Body.String(), "/ui/settings/"+kind+"/purge")).To(ConsistOf(confirm))
				rec := e.post("/ui/settings/"+kind+"/purge", url.Values{})
				uiExpectFragment(rec, "s-"+kind)
				Expect(rec.Body.String()).NotTo(ContainSubstring("/purge"))
			},
			Entry("users", "users", "Delete all 1 user?", uiAddBob),
			Entry("grants", "grants", "Delete all 1 grant?", uiAddGrant),
			Entry("host auth", "hostauth", "Delete all 1 host auth entry?", func(e *uiEnv) string {
				Expect(e.svc.PutHostAuth(api.HostAuth{Host: upstreamHost, Scheme: api.Scheme(uiSchemeBasic)})).To(Succeed())
				return ""
			}),
			Entry("aliases", "aliases", "Delete all 1 alias?", uiAddAlias),
			Entry("divergents", "divergents", "Delete all 1 divergent?", func(e *uiEnv) string {
				_, err := e.svc.CreateDivergent(api.Divergent{Host: upstreamHost, Repo: repoFoo, Salt: "s"})
				Expect(err).NotTo(HaveOccurred())
				return ""
			}),
			Entry("platforms", "platforms", "Delete all 1 platform rule?", func(e *uiEnv) string {
				_, err := e.svc.CreatePlatform(api.PlatformRule{Host: upstreamHost, Repo: repoFoo, Platforms: []string{"linux/amd64"}})
				Expect(err).NotTo(HaveOccurred())
				return ""
			}),
			Entry("quotas", "quotas", "Delete all 1 quota?", uiAddQuota),
		)

		It("empties the list and leaves the others", func() {
			e := newUIEnv()
			uiAddAlias(e)
			uiAddQuota(e)
			uiExpectFragment(e.post("/ui/settings/aliases/purge", url.Values{}), "s-aliases")
			Expect(e.svc.ListAliases()).To(BeEmpty())
			Expect(e.svc.ListQuotas()).To(HaveLen(1))
		})

		It("shows a refusal in the message area and changes nothing", func() {
			e := newUIEnv()
			uiAddGrant(e) // names a user
			uiExpectError(e.post("/ui/settings/users/purge", url.Values{}))
			Expect(e.svc.ListUsers()).To(HaveLen(1))
		})

		It("has no rules purge under settings", func() {
			uiExpectError(newUIEnv().post("/ui/settings/rules/purge", url.Values{}))
		})
	})

	Describe("images", func() {
		It("offers a confirmed button with the count of images to forget", func() {
			e := newUIEnv()
			Expect(e.get("/").Body.String()).NotTo(ContainSubstring("/ui/images/purge"))
			Expect(e.svc.store.Update(func(st *state.State) error {
				st.Seen[state.RefKey(upstreamHost, "acme/a", "t1")] = time.Now()
				st.Generations[state.RefKey(upstreamHost, "acme/a", "t1")] = 2
				st.Generations[state.RefKey(upstreamHost, "acme/b", "t1")] = 2
				return nil
			})).To(Succeed())
			Expect(uiConfirmsOf(e.get("/").Body.String(), "/ui/images/purge")).To(ConsistOf(ContainSubstring("Forget all 2 images")))
			Expect(e.get("/").Body.String()).To(ContainSubstring("stays listed"))
		})

		It("purges and asks the browser to refresh", func() {
			e := newUIEnv()
			_, err := e.svc.Resolve(upstreamHost, repoFoo, uiTagT1)
			Expect(err).NotTo(HaveOccurred())
			rec := e.post("/ui/images/purge", url.Values{})
			Expect(rec.Header().Get("HX-Refresh")).To(Equal("true"))
			Expect(rec.Header()).NotTo(HaveKey(uiHdrRetarget))
			rows, _ := e.svc.Images(ImageFilter{})
			Expect(rows).To(BeEmpty())
		})
	})

	Describe("requests", func() {
		It("offers a confirmed button with the log size, and none when empty", func() {
			e := newUIEnv()
			Expect(e.get(requestsPath).Body.String()).NotTo(ContainSubstring("/ui/log/purge"))
			for range 3 {
				e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
			}
			Expect(uiConfirmsOf(e.get(requestsPath).Body.String(), "/ui/log/purge")).To(ConsistOf("Delete all 3 logged requests?"))
		})

		It("empties the log, keeps the stats and answers an empty table body", func() {
			e := newUIEnv()
			e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
			rec := e.post("/ui/log/purge", url.Values{})
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(BeEmpty())
			Expect(e.log.Query(reqlog.Filter{})).To(BeEmpty())
			Expect(e.log.Stats()).NotTo(BeEmpty())
		})

		It("targets the live table body", func() {
			e := newUIEnv()
			e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
			tags := uiTags(e.get(requestsPath).Body.String(), uiHxPost+`/ui/log/purge"`)
			Expect(tags).To(HaveLen(1))
			Expect(uiTagAttrs(tags[0])["hx-target"]).To(Equal("#live tbody"))
		})
	})

	Describe("destinations", func() {
		It("offers a confirmed button per host with the repository count, and none when empty", func() {
			e := newUIEnv()
			Expect(e.get("/destinations").Body.String()).NotTo(ContainSubstring("purge-all"))
			ctx := context.Background()
			Expect(e.svc.DestPushSynthetic(ctx, mirrorHost, repoFoo, uiTagT1, "", 1)).To(Succeed())
			Expect(e.svc.DestPushSynthetic(ctx, mirrorHost, "acme/bar", uiTagT1, "", 1)).To(Succeed())
			Expect(uiConfirmsOf(e.get("/destinations").Body.String(), uiPathDest+"purge-all")).
				To(ConsistOf("Delete all 2 tags in 2 repositories of mirror?"))
		})

		It("purges and swaps the section of the host", func() {
			e := newUIEnv()
			ctx := context.Background()
			Expect(e.svc.DestPushSynthetic(ctx, mirrorHost, repoFoo, uiTagT1, "", 1)).To(Succeed())
			rec := e.post(uiPathDest+"purge-all", url.Values{})
			uiExpectFragment(rec, "dest-"+mirrorHost)
			Expect(rec.Body.String()).To(ContainSubstring("Deleted 1 tags in 1 repositories."))
			Expect(rec.Body.String()).NotTo(ContainSubstring("purge-all"))
		})

		It("reports an unknown host", func() {
			uiExpectError(newUIEnv().post("/ui/destinations/nope/purge-all", url.Values{}))
		})
	})
})
