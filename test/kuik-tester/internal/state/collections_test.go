package state

import (
	"encoding/json"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// sliceFields returns the indexes of the slice fields of State: its lists.
func sliceFields() []int {
	t := reflect.TypeFor[State]()
	var out []int
	for i := range t.NumField() {
		if t.Field(i).Type.Kind() == reflect.Slice {
			out = append(out, i)
		}
	}
	return out
}

// withZero returns a State holding one zero object in each listed field.
func withZero(fields ...int) *State {
	st := &State{}
	v := reflect.ValueOf(st).Elem()
	for _, i := range fields {
		f := v.Field(i)
		f.Set(reflect.Append(f, reflect.Zero(f.Type().Elem())))
	}
	return st
}

var _ = Describe("collections", func() {
	It("describes every list of the state once, in field order", func() {
		fields := sliceFields()
		Expect(collections).To(HaveLen(len(fields)))
		for n, i := range fields {
			name := reflect.TypeFor[State]().Field(i).Name
			dropped := 0
			for _, c := range collections {
				dropped += c.dropSet(withZero(i), "")
			}
			Expect(dropped).To(Equal(1), "objects of %s dropped by the collections", name)
			Expect(collections[n].dropSet(withZero(i), "")).To(Equal(1), "collection %d does not describe %s", n, name)
		}
	})

	It("exports and imports every list", func() {
		src := storeWith(func(st *State) { *st = *withZero(sliceFields()...) })
		b, err := src.Export()
		Expect(err).NotTo(HaveOccurred())
		dst, err := Open("")
		Expect(err).NotTo(HaveOccurred())
		Expect(dst.Import(b, nil)).To(Succeed())
		for name, l := range lists(*dst.Snapshot()) {
			Expect(l).To(HaveLen(1), "%s after an export and an import", name)
		}
	})
})

var _ = Describe("Coll.Find", func() {
	It("returns the index of the object of the key, -1 when there is none", func() {
		st := &State{}
		oneOfEach(st, "", "1")
		oneOfEach(st, "", "2")
		Expect(RuleColl.Find(st, "r2")).To(Equal(1))
		Expect(HostAuthColl.Find(st, "h1")).To(Equal(0))
		Expect(UserColl.Find(st, "r2")).To(Equal(-1))
	})
})

var _ = Describe("State", func() {
	Describe("EachSet", func() {
		It("visits the set of every object", func() {
			st := &State{}
			oneOfEach(st, "a", "1")
			oneOfEach(st, "", "2")
			visits := map[string]int{}
			st.EachSet(func(set string) { visits[set]++ })
			Expect(visits).To(Equal(map[string]int{"a": len(collections)}))
		})
	})

	Describe("DropSet", func() {
		It("drops the objects of a set and counts them", func() {
			st := &State{}
			oneOfEach(st, "a", "1")
			oneOfEach(st, "b", "2")
			oneOfEach(st, "", "3")
			Expect(st.DropSet("a")).To(Equal(len(collections)))
			want := &State{}
			oneOfEach(want, "b", "2")
			oneOfEach(want, "", "3")
			Expect(lists(*st)).To(Equal(lists(*want)))
		})

		It("drops nothing for the empty set name", func() {
			st := &State{}
			oneOfEach(st, "", "1")
			Expect(st.DropSet("")).To(Equal(0))
			want := &State{}
			oneOfEach(want, "", "1")
			Expect(lists(*st)).To(Equal(lists(*want)))
		})
	})

	Describe("PurgeColl", func() {
		It("empties one list, sets included, and leaves the others", func() {
			for _, kind := range []string{"rules", "aliases", "divergents", "grants", "platforms", "users", "hostauth", "quotas"} {
				st := &State{}
				oneOfEach(st, "a", "1")
				oneOfEach(st, "", "2")
				n, ok := st.PurgeColl(kind)
				Expect(ok).To(BeTrue(), kind)
				Expect(n).To(Equal(2), kind)
				empty := 0
				for _, ks := range keys(*st) {
					if len(ks) == 0 {
						empty++
					}
				}
				Expect(empty).To(Equal(1), "%s: exactly one list is empty", kind)
			}
		})

		It("reports an unknown kind and changes nothing", func() {
			st := &State{}
			oneOfEach(st, "", "1")
			n, ok := st.PurgeColl("nope")
			Expect(ok).To(BeFalse())
			Expect(n).To(BeZero())
			want := &State{}
			oneOfEach(want, "", "1")
			Expect(lists(*st)).To(Equal(lists(*want)))
		})
	})

	Describe("FillIDs", func() {
		It("gives an ID to the ID-keyed objects that have none", func() {
			st := withZero(sliceFields()...)
			st.Rules = append(st.Rules, api.Rule{ID: "kept"})
			st.FillIDs()
			Expect(st.Rules[1].ID).To(Equal("kept"), "an existing ID was replaced")
			for name, ks := range keys(*st) {
				switch name {
				case "Users", "HostAuth", "Quotas":
					Expect(ks).To(Equal([]string{"@"}), "the name or host of %s changed", name)
				default:
					Expect(ks[0]).To(MatchRegexp(`^[0-9a-f]{8}@$`), "%s without an ID", name)
				}
			}
			Expect(st.Aliases[0].Host).To(BeEmpty(), "the host of an alias changed")
		})
	})

	Describe("FillEmptyLists", func() {
		It("turns nil lists into empty ones", func() {
			st := &State{}
			st.init()
			st.FillEmptyLists()
			b, err := json.Marshal(st)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("null"))
		})
	})
})
