package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// anonymous is the caller of a request without credentials; the admin specs share it.
const anonymous = api.Anonymous

// wantInvalid fails unless err is a ValidationError.
func wantInvalid(err error) {
	GinkgoHelper()
	_, ok := errors.AsType[*ValidationError](err)
	Expect(ok).To(BeTrue(), "error = %v, want *ValidationError", err)
}

// errOf drops the value of a two-valued call.
func errOf[T any](_ T, err error) error { return err }

var _ = Describe("Validation", func() {
	rule := func(f func(*api.Rule)) func(*Service) error {
		return func(s *Service) error {
			r := api.Rule{Enabled: true}
			f(&r)
			_, err := s.CreateRule(r)
			return err
		}
	}

	setHeader := func(name string) func(*api.Rule) {
		return func(r *api.Rule) { r.Effects.Headers = map[string]string{name: "1"} }
	}

	DescribeTable("refuses an invalid change and leaves the state unchanged",
		func(do func(*Service) error) {
			s, _, _ := newService(nil)
			_, err := s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
			Expect(err).NotTo(HaveOccurred())
			rulesBefore := s.ListRules()
			exportBefore, _ := s.Export()
			wantInvalid(do(s))
			Expect(s.ListRules()).To(Equal(rulesBefore), "rules changed")
			after, _ := s.Export()
			Expect(string(after)).To(Equal(string(exportBefore)), "state changed")
		},
		Entry("a rule host glob", rule(func(r *api.Rule) { r.Target.Host = "up[" })),
		Entry("a rule kind", rule(func(r *api.Rule) { r.Target.Kinds = []api.Kind{"pull"} })),
		Entry("a rule status 302", rule(func(r *api.Rule) { r.Effects.Status = 302 })),
		Entry("a rule status 600", rule(func(r *api.Rule) { r.Effects.Status = 600 })),
		Entry("a rule with pass and a status", rule(func(r *api.Rule) { r.Effects.Pass, r.Effects.Status = true, 503 })),
		Entry("a rule with an unknown quota", rule(func(r *api.Rule) { r.Effects.Quota = "nope" })),
		Entry("a rule with an unknown caller", rule(func(r *api.Rule) { r.Target.Caller = bob })),
		Entry("a rule header name", rule(func(r *api.Rule) { r.Effects.Headers = map[string]string{"Bad Header": "x"} })),
		Entry("a rule setting Content-Length", rule(setHeader("Content-Length"))),
		Entry("a rule setting Transfer-Encoding", rule(setHeader("Transfer-Encoding"))),
		Entry("a rule setting Connection", rule(setHeader("Connection"))),
		Entry("a rule setting Host", rule(setHeader("Host"))),
		Entry("a rule setting Upgrade", rule(setHeader("Upgrade"))),
		Entry("a rule setting TE", rule(setHeader("TE"))),
		Entry("a rule setting content-length in lower case", rule(setHeader("content-length"))),
		Entry("a rule header value", rule(func(r *api.Rule) { r.Effects.Headers = map[string]string{"X-A": "a\r\nb: c"} })),
		Entry("a rule method", rule(func(r *api.Rule) { r.Target.Methods = []string{"FETCH"} })),
		Entry("a rule latency", rule(func(r *api.Rule) { r.Effects.LatencyMS = 600001 })),
		Entry("a rule set name", rule(func(r *api.Rule) { r.Set = "bad name" })),
		Entry("a user set name", func(s *Service) error { return s.PutUser(api.User{Name: "u1", Password: "p", Set: "a/b"}) }),
		Entry("an alias with an empty target", func(s *Service) error {
			_, err := s.CreateAlias(api.Alias{Host: "a", From: "x", To: ""})
			return err
		}),
		Entry("an alias with a glob source", func(s *Service) error {
			_, err := s.CreateAlias(api.Alias{Host: "a", From: repoGlob, To: "x"})
			return err
		}),
		Entry("an alias with a dot segment", func(s *Service) error {
			_, err := s.CreateAlias(api.Alias{Host: "a", From: "a/../b", To: "x"})
			return err
		}),
		Entry("a divergent without a salt", func(s *Service) error {
			_, err := s.CreateDivergent(api.Divergent{Host: "*", Repo: "**"})
			return err
		}),
		Entry("the anonymous user", func(s *Service) error { return s.PutUser(api.User{Name: anonymous, Password: "x"}) }),
		Entry("an invalid user name", func(s *Service) error { return s.PutUser(api.User{Name: "a(invalid)", Password: "x"}) }),
		Entry("a user name with a colon", func(s *Service) error { return s.PutUser(api.User{Name: "a:b", Password: "x"}) }),
		Entry("a user without a password", func(s *Service) error { return s.PutUser(api.User{Name: "a"}) }),
		Entry("a grant naming an unknown user", func(s *Service) error {
			_, err := s.CreateGrant(api.Grant{Host: "*", Repo: "**", Actions: []api.Action{api.ActionPull}, Users: []string{"ghost"}})
			return err
		}),
		Entry("a host auth scheme", func(s *Service) error { return s.PutHostAuth(api.HostAuth{Host: "h", Scheme: "digest"}) }),
		Entry("a quota limit", func(s *Service) error {
			return s.PutQuota(api.QuotaDef{Name: "q", Limit: 0, WindowSec: 1, Key: "ip"})
		}),
		Entry("a quota window", func(s *Service) error {
			return s.PutQuota(api.QuotaDef{Name: "q", Limit: 1, WindowSec: 0, Key: "ip"})
		}),
		Entry("a quota key", func(s *Service) error {
			return s.PutQuota(api.QuotaDef{Name: "q", Limit: 1, WindowSec: 1, Key: "team"})
		}),
	)

	It("accepts the other headers on a rule", func() {
		s, _, _ := newService(nil)
		_, err := s.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{
			Status: 503, Headers: map[string]string{"RateLimit-Remaining": "0", "Retry-After": "1"}}})
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a zero-valued object in every list of the state", func() {
		lists := 0
		for _, f := range reflect.VisibleFields(reflect.TypeFor[state.State]()) {
			if f.Type.Kind() != reflect.Slice {
				continue
			}
			lists++
			var st state.State
			reflect.ValueOf(&st).Elem().FieldByIndex(f.Index).Set(reflect.MakeSlice(f.Type, 1, 1))
			_, ok := errors.AsType[*ValidationError](validateState(&st))
			Expect(ok).To(BeTrue(), "a zero-valued object in %s was accepted", f.Name)
		}
		Expect(lists).To(BeNumerically(">", 0), "no list in state.State")
	})

	It("never writes the state it validates", func() {
		get := []string{http.MethodGet}
		st := state.State{
			Users:  []api.User{{Name: bob, Password: "pw"}},
			Quotas: []api.QuotaDef{{Name: "q", Limit: 1, WindowSec: 1, Key: keyGlobal, Methods: get}},
			Rules: []api.Rule{{ID: "r1", Enabled: true,
				Target: api.Target{Host: "UP", Methods: get}, Effects: api.Effects{Status: 503}}},
			Aliases:    []api.Alias{{ID: "a1", Host: "UP", From: "a/x", To: "b/x"}},
			Divergents: []api.Divergent{{ID: "d1", Host: "UP", Repo: "a", Salt: "s"}},
			Grants:     []api.Grant{{ID: "g1", Host: "UP", Repo: "a", Actions: []api.Action{api.ActionPull}, Users: []string{bob}}},
			HostAuth:   []api.HostAuth{{Host: "UP", Scheme: schemeBasic}},
			Platforms:  []api.PlatformRule{{ID: "p1", Host: "UP", Repo: "a", Platforms: []string{platAMD64}}},
		}
		before, err := json.Marshal(st)
		Expect(err).NotTo(HaveOccurred())
		Expect(validateState(&st)).To(Succeed())
		after, err := json.Marshal(st)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(Equal(string(before)), "validation wrote the state")
	})

	Describe("RuleWarnings", func() {
		It("warns about an anonymous caller with a status effect but not with a quota", func() {
			r := api.Rule{Target: api.Target{Caller: anonymous}, Effects: api.Effects{Status: 503}}
			Expect(RuleWarnings(r)).To(HaveLen(1), "anonymous + status, want one warning")
			r = api.Rule{Target: api.Target{Caller: anonymous}, Effects: api.Effects{Quota: "q"}}
			got := RuleWarnings(r)
			Expect(got).NotTo(BeNil(), "anonymous + quota, want an empty non-nil slice")
			Expect(got).To(BeEmpty(), "anonymous + quota, want an empty non-nil slice")
		})
	})
})
