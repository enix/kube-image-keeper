package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const amd64 = "linux/amd64"

var _ = Describe("Open", func() {
	DescribeTable("starts from an empty, initialised state",
		func(prepare func(path string)) {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			prepare(p)
			s, err := Open(p)
			Expect(err).NotTo(HaveOccurred())
			s.View(func(st *State) {
				Expect(st.Rules).To(BeEmpty(), "not an empty initialized state: %+v", st)
				Expect(st.Generations).NotTo(BeNil())
				Expect(st.Digests).NotTo(BeNil())
				Expect(st.Seen).NotTo(BeNil())
			})
		},
		Entry("when the file is missing", func(string) {}),
		Entry("when the file is empty", func(p string) {
			Expect(os.WriteFile(p, nil, 0o600)).To(Succeed())
		}),
	)

	It("leaves an empty file in place", func() {
		p := filepath.Join(GinkgoT().TempDir(), "state.json")
		Expect(os.WriteFile(p, nil, 0o600)).To(Succeed())
		_, err := Open(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(BeARegularFile())
	})

	It("moves an unreadable file aside and starts from an empty state", func() {
		p := filepath.Join(GinkgoT().TempDir(), "state.json")
		const original = `{"rules": [`
		Expect(os.WriteFile(p, []byte(original), 0o600)).To(Succeed())
		s, err := Open(p)
		Expect(err).NotTo(HaveOccurred())
		moved, err := os.ReadFile(p + ".corrupt")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(moved)).To(Equal(original))
		Expect(p).NotTo(BeAnExistingFile())
		Expect(storeRuleIDs(s)).To(BeEmpty())
		Expect(s.Update(addRule("r1"))).To(Succeed())
		Expect(p).To(BeARegularFile())
	})

	It("fails, naming the file, when an unreadable file cannot be moved aside", func() {
		p := filepath.Join(GinkgoT().TempDir(), "state.json")
		const original = `{"rules": [`
		Expect(os.WriteFile(p, []byte(original), 0o600)).To(Succeed())
		Expect(os.Mkdir(p+".corrupt", 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(p+".corrupt", "keep"), []byte("x"), 0o600)).To(Succeed())
		_, err := Open(p)
		Expect(err).To(MatchError(ContainSubstring(p)))
		kept, err := os.ReadFile(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(kept)).To(Equal(original))
	})

	It("replaces an older .corrupt file", func() {
		p := filepath.Join(GinkgoT().TempDir(), "state.json")
		const unparseable = `{"rules": [`
		Expect(os.WriteFile(p+".corrupt", []byte("old"), 0o600)).To(Succeed())
		Expect(os.WriteFile(p, []byte(unparseable), 0o600)).To(Succeed())
		_, err := Open(p)
		Expect(err).NotTo(HaveOccurred())
		moved, err := os.ReadFile(p + ".corrupt")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(moved)).To(Equal(unparseable))
	})

	It("loads a state file written by the previous version", func() {
		p := filepath.Join(GinkgoT().TempDir(), "state.json")
		const written = `{
  "rules": [{"id":"r1","enabled":true,"target":{"host":"up","repo":"acme/foo"},"effects":{"status":503}}],
  "aliases": [{"id":"a1","host":"up","from":"x","to":"y"}],
  "divergents": [{"id":"d1","host":"up","repo":"acme/foo","salt":"s"}],
  "users": [{"name":"bob","password":"pw"}],
  "grants": [{"id":"g1","host":"up","repo":"acme/foo","actions":["pull"],"users":["bob"]}],
  "hostAuth": [{"host":"up","scheme":"basic"}],
  "quotas": [{"name":"hub","limit":2,"windowSec":60,"key":"ip"}],
  "platforms": [{"id":"p1","host":"up","repo":"acme/foo","platforms":["linux/amd64"]}],
  "generations": {"up/acme/foo:t1": 2},
  "digests": {"sha256:aa": {"contentKey":"a","generation":2,"kind":"index","platforms":["linux/amd64"]}},
  "seen": {"up/acme/foo:t1": "2026-11-04T10:20:30Z"}
}`
		Expect(os.WriteFile(p, []byte(written), 0o600)).To(Succeed())
		s, err := Open(p)
		Expect(err).NotTo(HaveOccurred())
		s.View(func(st *State) {
			Expect(st.Rules).To(HaveLen(1))
			Expect(st.Rules[0].Effects.Status).To(Equal(503))
			Expect(st.Aliases).To(HaveLen(1))
			Expect(st.Divergents[0].Salt).To(Equal("s"))
			Expect(st.Users[0].Name).To(Equal("bob"))
			Expect(st.Grants[0].Users).To(Equal([]string{"bob"}))
			Expect(st.HostAuth[0].Scheme).To(Equal(api.SchemeBasic))
			Expect(st.Quotas[0].Limit).To(Equal(2))
			Expect(st.Platforms[0].Platforms).To(Equal([]string{amd64}))
			Expect(st.Generations).To(HaveKeyWithValue("up/acme/foo:t1", 2))
			Expect(st.Digests["sha256:aa"].Platforms).To(Equal([]string{amd64}))
			Expect(st.Seen["up/acme/foo:t1"]).To(BeTemporally("==", time.Date(2026, 11, 4, 10, 20, 30, 0, time.UTC)))
		})
		Expect(s.Update(addRule("r2"))).To(Succeed())
		b, err := os.ReadFile(p)
		Expect(err).NotTo(HaveOccurred())
		var top map[string]json.RawMessage
		Expect(json.Unmarshal(b, &top)).To(Succeed())
		for _, k := range []string{"rules", "aliases", "divergents", "users", "grants", "hostAuth", "quotas", "platforms", "generations", "digests", "seen"} {
			Expect(top).To(HaveKey(k))
		}
		Expect(string(top["digests"])).To(ContainSubstring(`"platforms"`))
		Expect(string(top["seen"])).To(ContainSubstring("2026-11-04T10:20:30Z"))
	})
})

var _ = Describe("Store", func() {
	Describe("Update", func() {
		It("persists the changes and reloads them", func() {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			s, _ := Open(p)
			rule := api.Rule{ID: "r1", Enabled: true, Target: api.Target{Host: "upstream", Repo: "acme/foo", Kinds: []api.Kind{api.KindManifestByTag}}, Effects: api.Effects{Status: 503, Headers: map[string]string{"A": "b"}}}
			err := s.Update(func(st *State) error {
				st.Rules = append(st.Rules, rule)
				st.Generations[RefKey("upstream", "acme/foo", "t1")] = 2
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
			s2, err := Open(p)
			Expect(err).NotTo(HaveOccurred())
			s2.View(func(st *State) {
				Expect(st.Rules).To(Equal([]api.Rule{rule}))
				Expect(st.Generations[RefKey("upstream", "acme/foo", "t1")]).To(Equal(2), "generations = %v", st.Generations)
			})
		})

		It("leaves the state and the file untouched when the function fails", func() {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			s, _ := Open(p)
			_ = s.Update(func(st *State) error { st.Users = append(st.Users, api.User{Name: "a"}); return nil })
			before, _ := os.ReadFile(p)
			boom := errors.New("boom")
			err := s.Update(func(st *State) error {
				st.Rules = append(st.Rules, api.Rule{ID: "x"})
				return boom
			})
			Expect(err).To(MatchError(boom))
			s.View(func(st *State) {
				Expect(st.Rules).To(BeEmpty(), "want no rules")
			})
			after, _ := os.ReadFile(p)
			Expect(string(after)).To(Equal(string(before)), "file changed after a failed update")
		})

		It("leaves the state and the file unchanged when the temporary file cannot be created", func() {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			s, err := Open(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Update(addRule("r1"))).To(Succeed())
			before, err := os.ReadFile(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Mkdir(p+".tmp", 0o700)).To(Succeed())
			Expect(s.Update(addRule("r2"))).To(HaveOccurred())
			Expect(storeRuleIDs(s)).To(Equal([]string{"r1"}))
			after, err := os.ReadFile(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(after)).To(Equal(string(before)))
		})

		It("removes the temporary file when the rename fails", func() {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			s, err := Open(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Update(addRule("r1"))).To(Succeed())
			Expect(os.Remove(p)).To(Succeed())
			Expect(os.Mkdir(p, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(p, "keep"), []byte("x"), 0o600)).To(Succeed())
			Expect(s.Update(addRule("r2"))).To(HaveOccurred())
			_, err = os.Stat(p + ".tmp")
			Expect(err).To(MatchError(fs.ErrNotExist))
			Expect(storeRuleIDs(s)).To(Equal([]string{"r1"}))
		})

		It("leaves no temporary file after a write", func() {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			s, err := Open(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Update(addRule("r1"))).To(Succeed())
			_, err = os.Stat(p + ".tmp")
			Expect(err).To(MatchError(fs.ErrNotExist))
		})

		It("reloads the digest index and the seen images", func() {
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			s, err := Open(p)
			Expect(err).NotTo(HaveOccurred())
			digests := map[string]DigestEntry{"sha256:aa": {ContentKey: "a", Generation: 2, Kind: "index", Platforms: []string{amd64}}}
			key := RefKey("up", "a", "t")
			t0 := time.Now()
			Expect(s.Update(func(st *State) error {
				st.Digests["sha256:aa"] = digests["sha256:aa"]
				st.Seen[key] = t0
				return nil
			})).To(Succeed())
			s2, err := Open(p)
			Expect(err).NotTo(HaveOccurred())
			s2.View(func(st *State) {
				Expect(st.Digests).To(Equal(digests))
				Expect(st.Seen).To(HaveKey(key))
				Expect(st.Seen[key]).To(BeTemporally("==", t0))
			})
		})

		It("lets readers through while a writer runs", func() {
			s, err := Open(filepath.Join(GinkgoT().TempDir(), "state.json"))
			Expect(err).NotTo(HaveOccurred())
			entered, release, written := make(chan struct{}), make(chan struct{}), make(chan struct{})
			DeferCleanup(func() {
				close(release)
				Eventually(written).Should(BeClosed())
			})
			go func() {
				defer GinkgoRecover()
				defer close(written)
				Expect(s.Update(func(st *State) error {
					close(entered)
					<-release
					st.Rules = append(st.Rules, api.Rule{ID: "r1"})
					return nil
				})).To(Succeed())
			}()
			Eventually(entered).Should(BeClosed())
			viewed := make(chan struct{})
			var seen int
			go func() {
				s.View(func(st *State) { seen = len(st.Rules) })
				close(viewed)
			}()
			Eventually(viewed).WithTimeout(time.Second).Should(BeClosed())
			Expect(seen).To(BeZero(), "the reader saw the write in progress")
		})

		It("keeps every change of concurrent writers", func() {
			s, err := Open(filepath.Join(GinkgoT().TempDir(), "state.json"))
			Expect(err).NotTo(HaveOccurred())
			stop := make(chan struct{})
			var readers, writers sync.WaitGroup
			for range 4 {
				readers.Go(func() {
					for {
						select {
						case <-stop:
							return
						default:
							s.View(func(st *State) { _ = len(st.Generations) })
						}
					}
				})
			}
			for i := range 20 {
				writers.Go(func() {
					defer GinkgoRecover()
					Expect(s.Update(func(st *State) error {
						st.Generations[RefKey("h", "r", strconv.Itoa(i))] = 1
						return nil
					})).To(Succeed())
				})
			}
			writers.Wait()
			close(stop)
			readers.Wait()
			s.View(func(st *State) {
				Expect(st.Generations).To(HaveLen(20))
			})
		})
	})

	Describe("Snapshot", func() {
		It("returns a state that later writes leave alone", func() {
			s, err := Open("")
			Expect(err).NotTo(HaveOccurred())
			snap := s.Snapshot()
			Expect(s.Update(addRule("r1"))).To(Succeed())
			Expect(snap.Rules).To(BeEmpty())
			Expect(s.Snapshot().Rules).To(HaveLen(1))
		})
	})

	Describe("Export and Import", func() {
		It("round-trips the rules, quotas and generations, keeping the local digests", func() {
			src, _ := Open("")
			_ = src.Update(func(st *State) error {
				st.Rules = []api.Rule{{ID: "r1", Enabled: true, Target: api.Target{Host: "up"}, Effects: api.Effects{Status: 429}}}
				st.Quotas = []api.QuotaDef{{Name: "hub", Limit: 2, WindowSec: 60, Key: "ip"}}
				st.Generations["up/a:t"] = 3
				return nil
			})
			out, err := src.Export()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(out)).NotTo(ContainSubstring("digests"), "export contains digests")
			Expect(string(out)).NotTo(ContainSubstring("seen"), "export contains seen")
			dst, _ := Open("")
			_ = dst.Update(func(st *State) error {
				st.Digests["sha256:aa"] = DigestEntry{ContentKey: "a", Generation: 1, Kind: "index"}
				return nil
			})
			Expect(dst.Import(out, nil)).To(Succeed())
			src.View(func(want *State) {
				dst.View(func(got *State) {
					Expect(got.Rules).To(Equal(want.Rules))
					Expect(got.Quotas).To(Equal(want.Quotas))
					Expect(got.Generations["up/a:t"]).To(Equal(3))
					Expect(got.Digests).To(HaveKey("sha256:aa"), "import dropped digests")
				})
			})
		})

		It("leaves the state untouched when the import check fails", func() {
			s, _ := Open("")
			_ = s.Update(func(st *State) error { st.Users = []api.User{{Name: "a", Password: "p"}}; return nil })
			before, _ := s.Export()
			boom := errors.New("boom")
			err := s.Import([]byte("users: [{name: b, password: q}]\n"), func(*State) error { return boom })
			Expect(err).To(MatchError(boom))
			after, _ := s.Export()
			Expect(string(after)).To(Equal(string(before)), "state changed after a failed check")
		})
	})
})

var _ = Describe("RefKey", func() {
	It("round-trips through SplitRefKey", func() {
		h, r, tag := SplitRefKey(RefKey("up", "a/b", "t_c-1"))
		Expect(h).To(Equal("up"))
		Expect(r).To(Equal("a/b"))
		Expect(tag).To(Equal("t_c-1"))
	})
})

var _ = Describe("NewID", func() {
	It("returns distinct 8-character identifiers", func() {
		a, b := NewID(), NewID()
		Expect(a).To(HaveLen(8))
		Expect(a).NotTo(Equal(b))
	})
})

func addRule(id string) func(*State) error {
	return func(st *State) error {
		st.Rules = append(st.Rules, api.Rule{ID: id})
		return nil
	}
}

func storeRuleIDs(s *Store) []string {
	GinkgoHelper()
	var ids []string
	s.View(func(st *State) {
		for _, r := range st.Rules {
			ids = append(ids, r.ID)
		}
	})
	return ids
}
