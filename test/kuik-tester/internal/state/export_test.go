package state

import (
	"errors"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Fixture values of the set specs.
const (
	upstream    = "upstream"
	basic       = "basic"
	bearer      = "bearer"
	newPassword = "new"
)

// oneOfEach adds to st one valid object per list, tagged with set; suffix
// makes its key and its repository unique.
func oneOfEach(st *State, set, suffix string) {
	repo := "acme/" + suffix
	st.Rules = append(st.Rules, api.Rule{ID: "r" + suffix, Set: set, Enabled: true,
		Target: api.Target{Host: upstream, Repo: repo}, Effects: api.Effects{Status: 503}})
	st.Aliases = append(st.Aliases, api.Alias{ID: "a" + suffix, Set: set, Host: upstream, From: repo, To: "other/" + suffix})
	st.Divergents = append(st.Divergents, api.Divergent{ID: "d" + suffix, Set: set, Host: upstream, Repo: repo, Salt: "s"})
	st.Users = append(st.Users, api.User{Name: "u" + suffix, Set: set, Password: "pw"})
	st.Grants = append(st.Grants, api.Grant{ID: "g" + suffix, Set: set, Host: upstream, Repo: repo,
		Actions: []api.Action{api.ActionPull}, Users: []string{"u" + suffix}})
	st.HostAuth = append(st.HostAuth, api.HostAuth{Host: "h" + suffix, Set: set, Scheme: basic})
	st.Quotas = append(st.Quotas, api.QuotaDef{Name: "q" + suffix, Set: set, Limit: 1, WindowSec: 60, Key: "global"})
	st.Platforms = append(st.Platforms, api.PlatformRule{ID: "p" + suffix, Set: set, Host: upstream, Repo: repo,
		Platforms: []string{"linux/amd64"}})
}

// lists maps the name of every slice field of the struct v (a State or an
// exported) to its value.
func lists(v any) map[string]any {
	GinkgoHelper()
	rv := reflect.ValueOf(v)
	out := map[string]any{}
	for i := range rv.NumField() {
		if rv.Field(i).Kind() == reflect.Slice {
			out[rv.Type().Field(i).Name] = rv.Field(i).Interface()
		}
	}
	return out
}

// keys maps the name of every slice field of the struct v to the
// "key@set" of its objects, in order; the key is the ID, else the name,
// else the host.
func keys(v any) map[string][]string {
	GinkgoHelper()
	rv := reflect.ValueOf(v)
	out := map[string][]string{}
	for i := range rv.NumField() {
		f := rv.Field(i)
		if f.Kind() != reflect.Slice {
			continue
		}
		ks := make([]string, 0, f.Len())
		for j := range f.Len() {
			e := f.Index(j)
			key := ""
			for _, n := range []string{"ID", "Name", "Host"} {
				if k := e.FieldByName(n); k.IsValid() {
					key = k.String()
					break
				}
			}
			ks = append(ks, key+"@"+e.FieldByName("Set").String())
		}
		out[rv.Type().Field(i).Name] = ks
	}
	return out
}

// storeWith returns an in-memory store after fn ran on its state.
func storeWith(fn func(*State)) *Store {
	GinkgoHelper()
	s, err := Open("")
	Expect(err).NotTo(HaveOccurred())
	Expect(s.Update(func(st *State) error { fn(st); return nil })).To(Succeed())
	return s
}

var _ = Describe("Store.ExportSet", func() {
	It("exports only the objects of the set, without generations", func() {
		s := storeWith(func(st *State) {
			oneOfEach(st, "a", "1")
			oneOfEach(st, "b", "2")
			oneOfEach(st, "", "3")
			st.Generations[RefKey(upstream, "acme/1", "t")] = 2
		})
		b, err := s.ExportSet("a")
		Expect(err).NotTo(HaveOccurred())
		var e exported
		Expect(yaml.UnmarshalStrict(b, &e)).To(Succeed())
		want := &State{}
		oneOfEach(want, "a", "1")
		Expect(lists(e)).To(Equal(lists(*want)))
		Expect(e.Generations).To(BeNil())
	})
})

var _ = Describe("Store.Merge", func() {
	It("inserts the ID-keyed objects on top, in file order, tagged with the set", func() {
		s := storeWith(func(st *State) { oneOfEach(st, "", "0") })
		doc := "rules: [{id: r1}, {id: r2}]\naliases: [{id: a1}, {id: a2}]\ndivergents: [{id: d1}, {id: d2}]\n" +
			"grants: [{id: g1}, {id: g2}]\nplatforms: [{id: p1}, {id: p2}]\n"
		Expect(s.Merge([]byte(doc), "a", nil)).To(Succeed())
		got := keys(*s.Snapshot())
		for list, prefix := range map[string]string{"Rules": "r", "Aliases": "a", "Divergents": "d", "Grants": "g", "Platforms": "p"} {
			Expect(got[list]).To(Equal([]string{prefix + "1@a", prefix + "2@a", prefix + "0@"}), list)
		}
	})

	It("replaces a name-keyed object of the same set or untagged, and appends a new one", func() {
		s := storeWith(func(st *State) {
			st.Users = []api.User{{Name: "u0", Password: "old"}, {Name: "ua", Set: "a", Password: "old"}}
			st.HostAuth = []api.HostAuth{{Host: "h0", Scheme: basic}, {Host: "ha", Set: "a", Scheme: basic}}
			st.Quotas = []api.QuotaDef{{Name: "q0", Limit: 1}, {Name: "qa", Set: "a", Limit: 1}}
		})
		doc := "users: [{name: u0, password: new}, {name: ua, password: new}, {name: un, password: new}]\n" +
			"hostAuth: [{host: h0, scheme: bearer}, {host: ha, scheme: bearer}, {host: hn, scheme: bearer}]\n" +
			"quotas: [{name: q0, limit: 2}, {name: qa, limit: 2}, {name: qn, limit: 2}]\n"
		Expect(s.Merge([]byte(doc), "a", nil)).To(Succeed())
		st := s.Snapshot()
		Expect(st.Users).To(Equal([]api.User{
			{Name: "u0", Set: "a", Password: newPassword}, {Name: "ua", Set: "a", Password: newPassword}, {Name: "un", Set: "a", Password: newPassword}}))
		Expect(st.HostAuth).To(Equal([]api.HostAuth{
			{Host: "h0", Set: "a", Scheme: bearer}, {Host: "ha", Set: "a", Scheme: bearer}, {Host: "hn", Set: "a", Scheme: bearer}}))
		Expect(st.Quotas).To(Equal([]api.QuotaDef{
			{Name: "q0", Set: "a", Limit: 2}, {Name: "qa", Set: "a", Limit: 2}, {Name: "qn", Set: "a", Limit: 2}}))
	})

	DescribeTable("refuses to take over a name-keyed object of another set",
		func(doc, what string) {
			s := storeWith(func(st *State) {
				st.Users = []api.User{{Name: "kb", Set: "b"}}
				st.HostAuth = []api.HostAuth{{Host: "kb", Set: "b"}}
				st.Quotas = []api.QuotaDef{{Name: "kb", Set: "b"}}
			})
			before := s.Snapshot()
			err := s.Merge([]byte(doc), "a", nil)
			_, ok := errors.AsType[*InvalidError](err)
			Expect(ok).To(BeTrue(), "error = %v, want *InvalidError", err)
			Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf(`%s "kb" belongs to set "b"`, what))))
			Expect(s.Snapshot()).To(BeIdenticalTo(before), "a refused merge changed the state")
		},
		Entry("a user", "users: [{name: kb}]", "user"),
		Entry("a host auth", "hostAuth: [{host: kb}]", "hostAuth"),
		Entry("a quota", "quotas: [{name: kb}]", "quota"),
	)

	It("merges the generations", func() {
		k1, k2, k3 := RefKey(upstream, "acme/x", "t1"), RefKey(upstream, "acme/x", "t2"), RefKey(upstream, "acme/x", "t3")
		s := storeWith(func(st *State) { st.Generations[k1], st.Generations[k2] = 2, 3 })
		doc := fmt.Sprintf("generations: {%q: 5, %q: 1}\n", k2, k3)
		Expect(s.Merge([]byte(doc), "a", nil)).To(Succeed())
		Expect(s.Snapshot().Generations).To(Equal(map[string]int{k1: 2, k2: 5, k3: 1}))
	})

	It("changes nothing when the check fails", func() {
		s := storeWith(func(st *State) { oneOfEach(st, "", "0") })
		before := s.Snapshot()
		boom := errors.New("boom")
		err := s.Merge([]byte("rules: [{id: r1}]\nusers: [{name: u1}]\n"), "a", func(*State) error { return boom })
		Expect(err).To(MatchError(boom))
		Expect(s.Snapshot()).To(BeIdenticalTo(before), "a refused merge changed the state")
	})

	It("refuses an unknown field", func() {
		s := storeWith(func(*State) {})
		err := s.Merge([]byte("bogus: 1\n"), "a", nil)
		_, ok := errors.AsType[*InvalidError](err)
		Expect(ok).To(BeTrue(), "error = %v, want *InvalidError", err)
	})
})
