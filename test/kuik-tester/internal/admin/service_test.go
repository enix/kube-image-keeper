package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// newService returns a Service over an in-memory store. A nil cfg means
// base domain kt.test and a proxy "mirror" backed by an in-memory registry.
func newService(cfg *testerconfig.Config) (*Service, *state.Store, *quota.Counters) {
	GinkgoHelper()
	st, err := state.Open("")
	Expect(err).NotTo(HaveOccurred())
	return newServiceOn(cfg, st)
}

func newServiceOn(cfg *testerconfig.Config, st *state.Store) (*Service, *state.Store, *quota.Counters) {
	GinkgoHelper()
	if cfg == nil {
		be := testutil.NewBackend()
		DeferCleanup(be.Close)
		cfg = &testerconfig.Config{BaseDomain: baseDomain, Proxies: []testerconfig.ProxyHost{{Name: mirrorHost, Backend: be.URL}}}
	}
	Expect(cfg.Resolve()).To(Succeed())
	proxies, err := dest.NewProxies(cfg)
	Expect(err).NotTo(HaveOccurred())
	q := quota.New()
	return NewService(cfg, st, q, proxies), st, q
}

func ruleIDs(rs []api.Rule) []string {
	ids := make([]string, 0, len(rs))
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	return ids
}

const setFixture = "rules:\n- target: {host: upstream, repo: acme/extra/foo, tag: t-extra}\n  effects: {status: 503}\n  enabled: true\nusers:\n- {name: extra, password: pw}\ngrants:\n- {host: private, repo: acme/extra/**, actions: [pull], users: [extra]}\n"

func baseState(s *Service) {
	GinkgoHelper()
	base := "rules:\n- {id: base, enabled: true, target: {host: nodelete, repo: '**', kinds: [delete]}, effects: {status: 405}}\nusers:\n- {name: fallback, password: pw}\n"
	Expect(s.ImportSet(setBase, []byte(base))).To(Succeed())
}

var _ = Describe("Service", func() {
	Describe("rules", func() {
		It("supports CRUD, move and enable", func() {
			s, _, _ := newService(nil)
			a, err := s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
			b, err := s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 429}})
			Expect(err).NotTo(HaveOccurred())
			Expect(a.ID).NotTo(BeEmpty(), "ids %q %q", a.ID, b.ID)
			Expect(a.ID).NotTo(Equal(b.ID), "ids %q %q", a.ID, b.ID)
			Expect(ruleIDs(s.ListRules())).To(Equal([]string{b.ID, a.ID}), "order, want newest first")
			Expect(s.MoveRule(b.ID, 5)).To(Succeed())
			Expect(ruleIDs(s.ListRules())).To(Equal([]string{a.ID, b.ID}), "after move")
			Expect(s.MoveRule(b.ID, -3)).To(Succeed())
			Expect(ruleIDs(s.ListRules())).To(Equal([]string{b.ID, a.ID}), "after move to a negative index")
			Expect(s.SetRuleEnabled(a.ID, false)).To(Succeed())
			Expect(s.ListRules()[1].Enabled).To(BeFalse(), "rule still enabled")
			a.Effects.Status = 500
			_, err = s.UpdateRule(a)
			Expect(err).NotTo(HaveOccurred())
			got := s.ListRules()[1]
			Expect(got.Effects.Status).To(Equal(500), "updated rule = %+v", got)
			Expect(got.ID).To(Equal(a.ID), "updated rule = %+v", got)
			_, err = s.UpdateRule(api.Rule{ID: "nope"})
			Expect(err).To(MatchError(ErrNotFound), "UpdateRule(unknown)")
			Expect(s.MoveRule("nope", 0)).To(MatchError(ErrNotFound), "MoveRule(unknown)")
			Expect(s.DeleteRule(a.ID)).To(Succeed())
			Expect(s.DeleteRule(a.ID)).To(MatchError(ErrNotFound), "DeleteRule twice")
			Expect(ruleIDs(s.ListRules())).To(Equal([]string{b.ID}), "rules")
		})

		It("upper-cases the methods of a rule on a copy", func() {
			svc, _, _ := newService(nil)
			in := []string{lowerGet}
			_, err := svc.CreateRule(api.Rule{Enabled: true, Target: api.Target{Methods: in}, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
			Expect(in[0]).To(Equal(lowerGet), "caller slice mutated: %v", in)
		})

		It("upper-cases the methods of a quota on a copy", func() {
			svc, _, _ := newService(nil)
			in := []string{lowerGet}
			Expect(svc.PutQuota(api.QuotaDef{Name: "q", Limit: 1, WindowSec: 60, Key: keyGlobal, Methods: in})).To(Succeed())
			Expect(in[0]).To(Equal(lowerGet), "caller slice mutated: %v", in)
			Expect(svc.ListQuotas()[0].Methods).To(Equal([]string{http.MethodGet}), "stored methods")
		})

	})

	Describe("lists", func() {
		It("are never nil on a fresh state", func() {
			s, _, _ := newService(nil)
			Expect(s.ListRules()).NotTo(BeNil(), "ListRules")
			Expect(s.ListAliases()).NotTo(BeNil(), "ListAliases")
			Expect(s.ListDivergents()).NotTo(BeNil(), "ListDivergents")
			Expect(s.ListGrants()).NotTo(BeNil(), "ListGrants")
			Expect(s.ListUsers()).NotTo(BeNil(), "ListUsers")
			Expect(s.ListHostAuth()).NotTo(BeNil(), "ListHostAuth")
			Expect(s.ListQuotas()).NotTo(BeNil(), "ListQuotas")
			rows, _ := s.Images(ImageFilter{})
			Expect(rows).NotTo(BeNil(), "Images")
			Expect(s.Destinations()).NotTo(BeNil(), "Destinations")
			Expect(s.QuotaUsage()).NotTo(BeNil(), "QuotaUsage")
			b, err := s.StateJSON()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("null"), "StateJSON")
		})
	})

	Describe("settings", func() {
		It("keys aliases, divergents, grants, host auth and users", func() {
			s, _, _ := newService(nil)
			a1, err := s.CreateAlias(api.Alias{Host: mirrorHost, From: "a", To: "b"})
			Expect(err).NotTo(HaveOccurred())
			a2, _ := s.CreateAlias(api.Alias{Host: mirrorHost, From: "c", To: "d"})
			got := s.ListAliases()
			Expect(got[0].ID).To(Equal(a2.ID), "aliases = %+v", got)
			Expect(got[1].ID).To(Equal(a1.ID), "aliases = %+v", got)
			a1.To = "z"
			_, err = aliasColl.replace(s, a1)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.DeleteAlias(a2.ID)).To(Succeed())
			got = s.ListAliases()
			Expect(got).To(HaveLen(1), "aliases = %+v", got)
			Expect(got[0].To).To(Equal("z"), "aliases = %+v", got)
			d, err := s.CreateDivergent(api.Divergent{Host: "*", Repo: "**", Salt: "x"})
			Expect(err).NotTo(HaveOccurred(), "divergent")
			Expect(s.DeleteDivergent(d.ID)).To(Succeed(), "divergent")
			Expect(s.ListDivergents()).To(BeEmpty(), "divergent")
			Expect(s.PutUser(api.User{Name: alice, Password: "pw"})).To(Succeed())
			g, err := s.CreateGrant(api.Grant{Host: "*", Repo: "**", Actions: []api.Action{api.ActionPull}, Users: []string{alice}})
			Expect(err).NotTo(HaveOccurred(), "grant")
			Expect(s.DeleteGrant(g.ID)).To(Succeed(), "grant")
			Expect(s.ListGrants()).To(BeEmpty(), "grant")
			Expect(s.PutHostAuth(api.HostAuth{Host: hostGlob, Scheme: schemeBasic})).To(Succeed())
			Expect(s.PutHostAuth(api.HostAuth{Host: hostGlob, Scheme: api.SchemeBearer})).To(Succeed())
			ha := s.ListHostAuth()
			Expect(ha).To(HaveLen(1), "hostAuth = %+v", ha)
			Expect(ha[0].Scheme).To(Equal(api.SchemeBearer), "hostAuth = %+v", ha)
			Expect(s.DeleteHostAuth("nope")).To(MatchError(ErrNotFound), "DeleteHostAuth(unknown)")
			Expect(s.PutUser(api.User{Name: alice, Password: newValue})).To(Succeed(), "PutUser replace")
			Expect(s.ListUsers()[0].Password).To(Equal(newValue), "PutUser replace")
		})

		It("deletes a host auth named in another case", func() {
			s, _, _ := newService(nil)
			Expect(s.PutHostAuth(api.HostAuth{Host: hostGlob, Scheme: schemeBasic})).To(Succeed())
			Expect(s.DeleteHostAuth("ALT*")).To(Succeed())
			Expect(s.ListHostAuth()).To(BeEmpty())
		})

		It("refuses to delete a user or a quota that is still referenced", func() {
			s, _, _ := newService(nil)
			Expect(s.PutUser(api.User{Name: alice, Password: "pw"})).To(Succeed())
			_, err := s.CreateGrant(api.Grant{Host: "*", Repo: "**", Actions: []api.Action{api.ActionPull}, Users: []string{alice}})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.PutQuota(api.QuotaDef{Name: "q", Limit: 1, WindowSec: 60, Key: keyGlobal})).To(Succeed())
			_, err = s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Quota: "q"}})
			Expect(err).NotTo(HaveOccurred())
			wantInvalid(s.DeleteUser(alice))
			wantInvalid(s.DeleteQuota("q"))
			Expect(s.ListUsers()).To(HaveLen(1), "a refused delete changed the state")
			Expect(s.ListQuotas()).To(HaveLen(1), "a refused delete changed the state")
			Expect(s.DeleteUser("nobody")).To(MatchError(ErrNotFound), "DeleteUser(unknown)")
		})

		It("resets the counters of a quota on put, reset and delete", func() {
			s, _, q := newService(nil)
			def := api.QuotaDef{Name: "q", Limit: 5, WindowSec: 60, Key: keyGlobal}
			Expect(s.PutQuota(def)).To(Succeed())
			q.Take(def, "", time.Now())
			got := s.QuotaUsage()
			Expect(got["q"][""]).To(Equal(1), "usage = %v", got)
			def.WindowSec = 120
			Expect(s.PutQuota(def)).To(Succeed())
			got = s.QuotaUsage()
			Expect(got["q"]).To(BeEmpty(), "usage after PutQuota = %v, want none", got)

			q.Take(def, "", time.Now())
			Expect(s.ResetQuota("q")).To(Succeed())
			got = s.QuotaUsage()
			Expect(got["q"]).To(BeEmpty(), "usage after ResetQuota = %v", got)
			Expect(s.ResetQuota("nope")).To(MatchError(ErrNotFound), "ResetQuota(unknown)")
			q.Take(def, "", time.Now())
			Expect(s.DeleteQuota("q")).To(Succeed())
			got = s.QuotaUsage()
			Expect(got["q"]).To(BeEmpty(), "usage after DeleteQuota = %v", got)
		})
	})

	Describe("Import", func() {
		It("validates and leaves the state, the file and the counters untouched on a refusal", func() {
			path := filepath.Join(GinkgoT().TempDir(), "state.json")
			st, err := state.Open(path)
			Expect(err).NotTo(HaveOccurred())
			s, _, q := newServiceOn(nil, st)
			def := api.QuotaDef{Name: "q", Limit: 5, WindowSec: 60, Key: keyGlobal}
			Expect(s.PutQuota(def)).To(Succeed())
			_, err = s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
			q.Take(def, "", time.Now())
			before, err := s.Export()
			Expect(err).NotTo(HaveOccurred())
			fileBefore, _ := os.ReadFile(path)

			bad := "quotas:\n- {name: q, limit: 5, windowSec: 60, key: global}\n" +
				"rules:\n- {id: r1, enabled: true, effects: {status: 503}}\n" +
				"grants:\n- {id: g1, host: '*', repo: '**', actions: [pull], users: [ghost]}\n"
			err = s.Import([]byte(bad))
			wantInvalid(err)
			Expect(err).To(MatchError(ContainSubstring(`unknown user "ghost"`)))
			wantInvalid(s.Import([]byte("rules: [")))
			after, _ := s.Export()
			Expect(string(after)).To(Equal(string(before)), "Export changed after a rejected import")
			fileAfter, _ := os.ReadFile(path)
			Expect(string(fileAfter)).To(Equal(string(fileBefore)), "state file changed after a rejected import")
			got := s.QuotaUsage()
			Expect(got["q"][""]).To(Equal(1), "counters after a rejected import = %v, want untouched", got)

			good := "rules:\n- id: r9\n  enabled: true\n  target: {host: up}\n  effects: {status: 429}\n"
			Expect(s.Import([]byte(good))).To(Succeed())
			Expect(ruleIDs(s.ListRules())).To(Equal([]string{"r9"}), "rules after import")
			got = s.QuotaUsage()
			Expect(got["q"]).To(BeEmpty(), "counters of a quota named before the import = %v, want reset", got)
		})

		It("replaces a state holding a framing-header rule with a whole import", func() {
			s, st, _ := newService(nil)
			setRules(st, api.Rule{ID: "legacy", Enabled: true, Effects: api.Effects{Headers: map[string]string{"Content-Length": "0"}}})
			_, err := s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
			wantInvalid(err)
			good := "rules:\n- id: r9\n  enabled: true\n  target: {host: up}\n  effects: {status: 429}\n"
			Expect(s.Import([]byte(good))).To(Succeed())
			Expect(ruleIDs(s.ListRules())).To(Equal([]string{"r9"}), "the old rule must be gone")
			_, err = s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
		})

		It("assigns the missing ids and refuses duplicate ids", func() {
			svc, _, _ := newService(nil)
			yml := `rules:
  - enabled: true
    target: {host: upstream}
    effects: {status: 503}
users:
  - {name: bob, password: pw}
aliases:
  - {host: upstream, from: a/x, to: b/x}
divergents:
  - {host: upstream, repo: a/x, salt: s}
grants:
  - {host: upstream, repo: a/x, actions: [pull], users: [bob]}
`
			Expect(svc.Import([]byte(yml))).To(Succeed())
			ids := []string{svc.ListRules()[0].ID, svc.ListAliases()[0].ID, svc.ListDivergents()[0].ID, svc.ListGrants()[0].ID}
			for _, id := range ids {
				Expect(id).NotTo(BeEmpty(), "ids = %q", ids)
			}
			dup := `rules:
  - {id: x, enabled: true, effects: {status: 503}}
  - {id: x, enabled: true, effects: {status: 503}}
`
			err := svc.Import([]byte(dup))
			var ve *ValidationError
			Expect(errors.As(err, &ve)).To(BeTrue(), "duplicate ids = %v", err)
			Expect(err.Error()).To(ContainSubstring("duplicate"), "duplicate ids")
		})
	})

	Describe("destinations", func() {
		It("reports unknown hosts, refuses a bad repo and lists the inventory", func() {
			s, _, _ := newService(nil)
			_, err := s.Inventory(ctx, "nope")
			Expect(err).To(MatchError(ErrNotFound), "Inventory(unknown)")
			wantInvalid(s.DestDeleteTag(ctx, mirrorHost, "Bad//repo", "t"))
			Expect(s.DestPushSynthetic(ctx, mirrorHost, repoFoo, "t1", "", 0)).To(Succeed())
			repos, err := s.Inventory(ctx, mirrorHost)
			Expect(err).NotTo(HaveOccurred())
			idx, _ := synth.Build(repoFoo, 1, nil)
			want, _ := idx.Digest()
			Expect(repos).To(HaveLen(1), "inventory = %+v, want t1 at %s", repos, want)
			Expect(repos[0].Tags).To(HaveLen(1), "inventory = %+v, want t1 at %s", repos, want)
			Expect(repos[0].Tags[0].Tag).To(Equal("t1"), "inventory = %+v, want t1 at %s", repos, want)
			Expect(repos[0].Tags[0].Digest).To(Equal(want.String()), "inventory = %+v, want t1 at %s", repos, want)
			Expect(s.Destinations()).To(Equal([]string{mirrorHost}), "Destinations")
		})

		It("answers ErrNotFound to every destination method for an unknown host", func() {
			s, _, _ := newService(nil)
			_, err := s.Inventory(ctx, "nope")
			Expect(err).To(MatchError(ErrNotFound), "Inventory")
			Expect(s.DestDeleteTag(ctx, "nope", repoFoo, "t")).To(MatchError(ErrNotFound), "DestDeleteTag")
			Expect(s.DestDeleteManifest(ctx, "nope", repoFoo, "sha256:"+strings.Repeat("a", 64))).To(MatchError(ErrNotFound), "DestDeleteManifest")
			Expect(s.DestTag(ctx, "nope", repoFoo, "t1", "t2")).To(MatchError(ErrNotFound), "DestTag")
			Expect(s.DestPushSynthetic(ctx, "nope", repoFoo, "t1", "", 0)).To(MatchError(ErrNotFound), "DestPushSynthetic")
			_, err = s.DestPurge(ctx, "nope", "acme")
			Expect(err).To(MatchError(ErrNotFound), "DestPurge")
		})

		It("keeps the dest sentinel and the cause reachable behind a refused reference", func() {
			s, _, _ := newService(nil)
			err := s.DestDeleteTag(ctx, mirrorHost, "Bad//repo", "t")
			wantInvalid(err)
			Expect(err).To(MatchError(dest.ErrInvalid))
			Expect(err).NotTo(MatchError(dest.ErrBackend))
			var bad *name.ErrBadName
			Expect(errors.As(err, &bad)).To(BeTrue(), "error = %v, want a *name.ErrBadName behind it", err)
		})

		It("keeps the dest sentinel behind a backend failure", func() {
			backend := httptest.NewServer(http.NotFoundHandler())
			url := backend.URL
			backend.Close()
			s, _, _ := newService(&testerconfig.Config{BaseDomain: baseDomain, Proxies: []testerconfig.ProxyHost{{Name: deadHost, Backend: url}}})
			_, err := s.Inventory(ctx, deadHost)
			var be *backendError
			Expect(errors.As(err, &be)).To(BeTrue(), "error = %v, want a *backendError", err)
			Expect(err).To(MatchError(dest.ErrBackend))
			Expect(err).NotTo(MatchError(dest.ErrInvalid))
		})
	})

	Describe("platform rules", func() {
		DescribeTable("refuses an invalid platform rule",
			func(rule api.PlatformRule) {
				s, _, _ := newService(nil)
				wantInvalid(errOf(s.CreatePlatform(rule)))
				Expect(s.ListPlatforms()).To(BeEmpty(), "a refused create stored a rule")
			},
			Entry("an empty list", api.PlatformRule{Host: "*", Repo: repoDeep}),
			Entry("an unknown platform", api.PlatformRule{Host: "*", Repo: repoDeep, Platforms: []string{"linux/riscv64"}}),
			Entry("a duplicate platform", api.PlatformRule{Host: "*", Repo: repoDeep, Platforms: []string{platAMD64, platAMD64}}),
			Entry("a bad repo glob", api.PlatformRule{Host: "*", Repo: "acme/[", Platforms: []string{platAMD64}}),
		)

		It("creates, lists and deletes a platform rule and refuses a duplicate import", func() {
			s, _, _ := newService(nil)
			p, err := s.CreatePlatform(api.PlatformRule{Host: upstreamHost, Repo: "acme/plat/*", Platforms: []string{platAMD64}})
			Expect(err).NotTo(HaveOccurred())
			got := s.ListPlatforms()
			Expect(got).To(HaveLen(1), "platforms = %+v", got)
			Expect(got[0].ID).To(Equal(p.ID), "platforms = %+v", got)
			dup := "platforms:\n- {id: x, host: '*', repo: '**', platforms: [linux/amd64]}\n- {id: x, host: '*', repo: '**', platforms: [linux/arm64]}\n"
			wantInvalid(s.Import([]byte(dup)))
			Expect(s.ListPlatforms()).To(HaveLen(1), "a refused import changed the platforms")
			Expect(s.DeletePlatform(p.ID)).To(Succeed(), "delete")
			Expect(s.ListPlatforms()).To(BeEmpty(), "delete")
		})
	})

	Describe("sets", func() {
		Describe("ImportSet", func() {
			It("merges the objects and tags them with the set", func() {
				s, _, _ := newService(nil)
				baseState(s)
				Expect(s.ImportSet(setAdd, []byte(setFixture))).To(Succeed())
				rules := s.ListRules()
				Expect(rules).To(HaveLen(2), "rules = %+v", rules)
				Expect(rules[0].Set).To(Equal(setAdd), "rules = %+v", rules)
				Expect(rules[0].Target.Tag).To(Equal("t-extra"), "rules = %+v", rules)
				Expect(rules[1].ID).To(Equal("base"), "rules = %+v", rules)
				Expect(rules[1].Set).To(Equal(setBase), "rules = %+v", rules)
				u := s.ListUsers()
				Expect(u).To(HaveLen(2), "users = %+v", u)
				Expect(u[1].Name).To(Equal("extra"), "users = %+v", u)
				Expect(u[1].Set).To(Equal(setAdd), "users = %+v", u)
				g := s.ListGrants()
				Expect(g).To(HaveLen(1), "grants = %+v", g)
				Expect(g[0].Set).To(Equal(setAdd), "grants = %+v", g)
				Expect(g[0].ID).NotTo(BeEmpty(), "grants = %+v", g)
				Expect(s.Sets()).To(Equal([]api.SetInfo{{Name: setAdd, Objects: 3}, {Name: setBase, Objects: 2}}), "sets")
				// a name-keyed object of the same set is replaced in place
				Expect(s.ImportSet(setAdd, []byte("users:\n- {name: extra, password: other}\n"))).To(Succeed())
				u = s.ListUsers()
				Expect(u).To(HaveLen(2), "users after replace = %+v", u)
				Expect(u[1].Password).To(Equal(other), "users after replace = %+v", u)
				Expect(u[1].Set).To(Equal(setAdd), "users after replace = %+v", u)
			})

			DescribeTable("refuses a bad set name",
				func(name string) {
					s, _, _ := newService(nil)
					wantInvalid(s.ImportSet(name, []byte(setFixture)))
				},
				Entry("a name with a space", "a b"),
				Entry("an empty name", ""),
				Entry("a name with a slash", "x/y"),
				Entry("a name of 65 characters", strings.Repeat("a", 65)),
			)

			It("refuses taking over another set's object but takes over an untagged one", func() {
				s, _, _ := newService(nil)
				baseState(s)
				err := s.ImportSet(setAdd, []byte("users:\n- {name: fallback, password: other}\n"))
				var ve *ValidationError
				Expect(errors.As(err, &ve)).To(BeTrue(), "ImportSet taking over a kt-base user = %v, want a ValidationError naming the user and its set", err)
				Expect(err.Error()).To(ContainSubstring("fallback"), "ImportSet taking over a kt-base user")
				Expect(err.Error()).To(ContainSubstring(setBase), "ImportSet taking over a kt-base user")
				u := s.ListUsers()
				Expect(u).To(HaveLen(1), "users changed by a refused import: %+v", u)
				Expect(u[0].Set).To(Equal(setBase), "users changed by a refused import: %+v", u)
				Expect(u[0].Password).To(Equal("pw"), "users changed by a refused import: %+v", u)
				// an untagged object (created outside any set) is taken over
				Expect(s.PutUser(api.User{Name: "loose", Password: "pw"})).To(Succeed())
				Expect(s.ImportSet(setAdd, []byte("users:\n- {name: loose, password: new}\n"))).To(Succeed())
				u = s.ListUsers()
				Expect(u).To(HaveLen(2), "users = %+v", u)
				Expect(u[1].Set).To(Equal(setAdd), "users = %+v", u)
				Expect(u[1].Password).To(Equal(newValue), "users = %+v", u)
				// re-importing the same set replaces its own objects
				Expect(s.ImportSet(setAdd, []byte("users:\n- {name: loose, password: again}\n"))).To(Succeed())
				u = s.ListUsers()
				Expect(u[1].Password).To(Equal("again"), "users = %+v", u)
			})
		})

		Describe("ExportSet", func() {
			It("refuses a bad set name", func() {
				s, _, _ := newService(nil)
				wantInvalid(errOf(s.ExportSet("a b")))
			})
		})

		Describe("DeleteSet", func() {
			It("resets the counters of the quotas of the set", func() {
				s, _, q := newService(nil)
				Expect(s.ImportSet(setAdd, []byte("quotas:\n- {name: q, limit: 5, windowSec: 60, key: global}\n"))).To(Succeed())
				q.Take(api.QuotaDef{Name: "q", Limit: 5, WindowSec: 60, Key: keyGlobal}, "", time.Now())
				Expect(s.DeleteSet(setAdd)).To(Succeed())
				Expect(s.QuotaUsage()["q"]).To(BeEmpty(), "usage after DeleteSet = %v", s.QuotaUsage())
			})

			It("removes every object of the set", func() {
				s, _, _ := newService(nil)
				baseState(s)
				Expect(s.ImportSet(setAdd, []byte(setFixture))).To(Succeed())
				Expect(s.DeleteSet(setAdd)).To(Succeed())
				Expect(ruleIDs(s.ListRules())).To(Equal([]string{"base"}), "rules")
				u := s.ListUsers()
				Expect(u).To(HaveLen(1), "users = %+v", u)
				Expect(u[0].Name).To(Equal("fallback"), "users = %+v", u)
				Expect(s.ListGrants()).To(BeEmpty(), "grants")
				Expect(s.Sets()).To(Equal([]api.SetInfo{{Name: setBase, Objects: 2}}), "sets")
				Expect(s.DeleteSet(setAdd)).To(MatchError(ErrNotFound), "second delete")
			})

			It("finds nothing under the empty set name and keeps the untagged objects", func() {
				s, _, _ := newService(nil)
				_, err := s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
				Expect(err).NotTo(HaveOccurred())
				Expect(s.DeleteSet("")).To(MatchError(ErrNotFound))
				Expect(s.ListRules()).To(HaveLen(1), "DeleteSet(\"\") dropped an untagged rule")
			})

			It("is refused when an object outside the set still references one of its users", func() {
				s, _, _ := newService(nil)
				Expect(s.ImportSet(setAdd, []byte(setFixture))).To(Succeed())
				g, err := s.CreateGrant(api.Grant{Host: upstreamHost, Repo: "**", Actions: []api.Action{api.ActionPull}, Users: []string{"extra"}})
				Expect(err).NotTo(HaveOccurred())
				err = s.DeleteSet(setAdd)
				var ve *ValidationError
				Expect(errors.As(err, &ve)).To(BeTrue(), "DeleteSet with a dangling grant = %v, want a ValidationError naming grant %s", err, g.ID)
				Expect(err.Error()).To(ContainSubstring(g.ID), "DeleteSet with a dangling grant, want a ValidationError naming grant %s", g.ID)
				Expect(s.ListRules()).To(HaveLen(1), "a refused set deletion changed the state")
				Expect(s.ListUsers()).To(HaveLen(1), "a refused set deletion changed the state")
				Expect(s.ListGrants()).To(HaveLen(2), "a refused set deletion changed the state")
			})
		})
	})
})
