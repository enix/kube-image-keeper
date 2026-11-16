package quota

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

var _ = Describe("Cache", func() {
	Describe("Take", func() {
		It("counts requests inside a window and starts a new one after it", func() {
			c := New()
			def := api.QuotaDef{Name: "q", Limit: 2, WindowSec: 60}
			for i, want := range []struct {
				rem int
				ok  bool
			}{{1, true}, {0, true}, {0, false}} {
				rem, ok := c.Take(def, "k", t0)
				Expect(rem).To(Equal(want.rem), "take %d", i)
				Expect(ok).To(Equal(want.ok), "take %d", i)
			}
			Expect(c.Peek(def, "k", t0)).To(Equal(2))
			rem, ok := c.Take(def, "k", t0.Add(60*time.Second))
			Expect(rem).To(Equal(1), "take after window")
			Expect(ok).To(BeTrue(), "take after window")
		})

		It("drops expired windows when it counts after the sweep interval", func() {
			c := New()
			def := api.QuotaDef{Name: "q", Limit: 2, WindowSec: 1}
			c.Take(def, "a", t0)
			_, ok := c.Take(def, "b", t0.Add(2*time.Minute))
			Expect(ok).To(BeTrue(), "take after the sweep")
			Expect(c.m[def.Name]).To(HaveLen(1), "windows left after the sweep")
		})

		It("drops a quota whose every window expired when it counts after the sweep interval", func() {
			c := New()
			c.Take(api.QuotaDef{Name: "old", Limit: 2, WindowSec: 1}, "a", t0)
			c.Take(api.QuotaDef{Name: "new", Limit: 2, WindowSec: 60}, "b", t0.Add(2*time.Minute))
			Expect(c.m).NotTo(HaveKey("old"), "quota left after its windows expired")
			Expect(c.m).To(HaveKey("new"))
		})

		It("keeps the keys independent", func() {
			c := New()
			def := api.QuotaDef{Name: "q", Limit: 2, WindowSec: 60}
			for _, k := range []string{"a", "b"} {
				for range 2 {
					_, ok := c.Take(def, k, t0)
					Expect(ok).To(BeTrue(), "key %s refused", k)
				}
			}
		})

		It("grants exactly the limit under concurrent calls", func() {
			c := New()
			def := api.QuotaDef{Name: "q", Limit: 20, WindowSec: 60}
			var wg sync.WaitGroup
			var mu sync.Mutex
			okCount := 0
			for range 50 {
				wg.Go(func() {
					defer GinkgoRecover()
					if _, ok := c.Take(def, "k", t0); ok {
						mu.Lock()
						okCount++
						mu.Unlock()
					}
				})
			}
			wg.Wait()
			Expect(okCount).To(Equal(20))
		})
	})

	Describe("Peek", func() {
		It("reports an unused quota as empty", func() {
			Expect(New().Peek(api.QuotaDef{Name: "q", Limit: 2, WindowSec: 60}, "k", t0)).To(BeZero())
		})
	})

	Describe("Reset", func() {
		It("clears the counters of a quota", func() {
			c := New()
			def := api.QuotaDef{Name: "q", Limit: 1, WindowSec: 60}
			c.Take(def, "k", t0)
			_, ok := c.Take(def, "k", t0)
			Expect(ok).To(BeFalse(), "expected exhausted")
			Expect(c.Snapshot(t0)).To(Equal(map[string]map[string]int{"q": {"k": 1}}))
			c.Reset("q")
			_, ok = c.Take(def, "k", t0)
			Expect(ok).To(BeTrue(), "Take refused after Reset")
		})
	})

	Describe("ResetAll", func() {
		It("clears the counters of every quota", func() {
			c := New()
			a := api.QuotaDef{Name: "a", Limit: 1, WindowSec: 60}
			b := api.QuotaDef{Name: "b", Limit: 1, WindowSec: 60}
			c.Take(a, "k", t0)
			c.Take(b, "k", t0)
			Expect(c.Snapshot(t0)).To(HaveLen(2))
			c.ResetAll()
			Expect(c.Snapshot(t0)).To(BeEmpty())
			_, ok := c.Take(a, "k", t0)
			Expect(ok).To(BeTrue(), "Take refused after ResetAll")
		})
	})

	Describe("Snapshot", func() {
		It("evicts expired windows", func() {
			c := New()
			def := api.QuotaDef{Name: "q", Limit: 2, WindowSec: 60}
			c.Take(def, "k", t0)
			Expect(c.Snapshot(t0.Add(61*time.Second))).To(BeEmpty(), "Snapshot after the window")
			Expect(c.Snapshot(t0)).To(BeEmpty(), "Snapshot at an earlier time, the window being evicted")
		})
	})
})

var _ = Describe("Key", func() {
	DescribeTable("computing the counter key",
		func(key api.QuotaKey, ip, user, want string) {
			Expect(Key(api.QuotaDef{Key: key}, ip, user)).To(Equal(want))
		},
		Entry("global", api.QuotaKeyGlobal, clientIP, userAlice, ""),
		Entry("ip", api.QuotaKeyIP, clientIP, userAlice, clientIP),
		Entry("user", api.QuotaKeyUser, clientIP, userAlice, userAlice),
		Entry("user without a name", api.QuotaKeyUser, clientIP, "", api.Anonymous),
		Entry("an unknown key", api.QuotaKey("other"), clientIP, userAlice, ""),
	)
})

var _ = Describe("Counts", func() {
	It("counts GET manifests by default", func() {
		def := api.QuotaDef{}
		Expect(Counts(def, api.KindManifestByTag, "GET")).To(BeTrue())
		Expect(Counts(def, api.KindManifestByDigest, "get")).To(BeTrue())
	})

	It("does not count HEAD or blobs by default", func() {
		def := api.QuotaDef{}
		Expect(Counts(def, api.KindManifestByTag, "HEAD")).To(BeFalse())
		Expect(Counts(def, api.KindBlob, "GET")).To(BeFalse())
	})

	It("counts what the quota names explicitly", func() {
		def := api.QuotaDef{Kinds: []api.Kind{api.KindBlob}, Methods: []string{"head"}}
		Expect(Counts(def, api.KindBlob, "HEAD")).To(BeTrue())
	})
})
