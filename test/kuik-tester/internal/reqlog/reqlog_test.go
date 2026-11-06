package reqlog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("Log", func() {
	Describe("Expire", func() {
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		add := func(l *Log, from, to int) {
			for i := from; i <= to; i++ {
				l.Add(api.LogEntry{Status: i, Time: base.Add(time.Duration(i) * time.Hour)})
			}
		}
		statuses := func(l *Log) (out []int) {
			for _, e := range l.Query(Filter{}) {
				out = append(out, e.Status)
			}
			return out
		}

		It("drops the old entries of a partial ring and keeps the counters", func() {
			l := New(10)
			add(l, 1, 5)
			Expect(l.Expire(base.Add(3 * time.Hour))).To(Equal(2))
			Expect(statuses(l)).To(Equal([]int{3, 4, 5}))
			Expect(l.Stats()).To(HaveLen(5))
			add(l, 6, 7)
			Expect(statuses(l)).To(Equal([]int{3, 4, 5, 6, 7}))
		})

		It("drops the old entries of a full ring that has wrapped", func() {
			l := New(4)
			add(l, 1, 6) // keeps 3..6
			Expect(l.Expire(base.Add(5 * time.Hour))).To(Equal(2))
			Expect(statuses(l)).To(Equal([]int{5, 6}))
			add(l, 7, 9)
			Expect(statuses(l)).To(Equal([]int{6, 7, 8, 9}))
		})

		It("empties the log and removes nothing when nothing is old", func() {
			l := New(3)
			add(l, 1, 3)
			Expect(l.Expire(base)).To(BeZero())
			Expect(statuses(l)).To(HaveLen(3))
			Expect(l.Expire(base.Add(24 * time.Hour))).To(Equal(3))
			Expect(statuses(l)).To(BeEmpty())
			Expect(l.Expire(base.Add(48 * time.Hour))).To(BeZero())
		})
	})

	Describe("Add", func() {
		It("wraps around when the ring is full", func() {
			l := New(3)
			for i := 1; i <= 5; i++ {
				l.Add(api.LogEntry{Status: i})
			}
			got := l.Query(Filter{})
			Expect(got).To(HaveLen(3))
			for i, e := range got {
				Expect(e.Status).To(Equal(i+3), "entry %d status", i)
			}
		})

		It("never blocks on a slow subscriber", func() {
			l := New(10)
			_, unsub := l.Subscribe()
			defer unsub()
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				for range 1000 {
					l.Add(api.LogEntry{})
				}
				close(done)
			}()
			Eventually(done).WithTimeout(5*time.Second).Should(BeClosed(), "Add blocked on slow subscriber")
		})

		// Adds race with subscribers coming and going; run it under -race.
		It("copes with concurrent adds and subscribers", func() {
			l := New(50)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					defer GinkgoRecover()
					for range 500 {
						l.Add(api.LogEntry{Host: "h", Method: http.MethodGet, Status: 200})
					}
				})
			}
			for range 8 {
				wg.Go(func() {
					defer GinkgoRecover()
					for range 100 {
						ch, unsub := l.Subscribe()
						select {
						case <-ch:
						default:
						}
						_ = l.Query(Filter{})
						_ = l.Stats()
						unsub()
					}
				})
			}
			wg.Wait()
			Expect(l.Subscribers()).To(BeZero(), "subscribers left")
			st := l.Stats()
			Expect(st).To(HaveLen(1), "want one row counting 4000")
			Expect(st[0].Count).To(BeEquivalentTo(8 * 500))
		})

		DescribeTable("cuts a logged string at 256 bytes",
			func(ua string, wantWhole bool) {
				l := New(1)
				l.Add(api.LogEntry{UserAgent: ua})
				got := l.Query(Filter{})[0].UserAgent
				Expect(len(got)).To(BeNumerically("<=", 256))
				Expect(utf8.ValidString(got)).To(BeTrue(), "valid UTF-8")
				if wantWhole {
					Expect(got).To(Equal(ua))
					return
				}
				Expect(got).To(HaveSuffix("…"))
			},
			Entry("exactly 256 bytes is kept whole", strings.Repeat("a", 256), true),
			Entry("257 bytes is cut", strings.Repeat("a", 257), false),
			Entry("1 MiB is cut", strings.Repeat("a", 1<<20), false),
			Entry("a multi-byte character reaching past the limit is cut", strings.Repeat("a", 255)+"é", false),
		)

		It("backs up to the start of a multi-byte character straddling the cut", func() {
			l := New(1)
			l.Add(api.LogEntry{UserAgent: strings.Repeat("a", 252) + "é" + "bbbb"})
			Expect(l.Query(Filter{})[0].UserAgent).To(Equal(strings.Repeat("a", 252) + "…"))
		})

		It("treats a size below 1 as 1", func() {
			l := New(0)
			l.Add(api.LogEntry{Host: "a"})
			l.Add(api.LogEntry{Host: "b"})
			got := l.Query(Filter{})
			Expect(got).To(HaveLen(1))
			Expect(got[0].Host).To(Equal("b"))
		})

		It("cuts every string field of the entry", func() {
			long := strings.Repeat("x", 1024)
			l := New(1)
			l.Add(api.LogEntry{Host: long, ClientIP: long, User: long, UserAgent: long, Caller: long,
				Method: long, Repo: long, Reference: long, Digest: long, RuleID: long})
			e := l.Query(Filter{})[0]
			for name, v := range map[string]string{"Host": e.Host, "ClientIP": e.ClientIP, "User": e.User,
				"UserAgent": e.UserAgent, "Caller": e.Caller, "Method": e.Method, "Repo": e.Repo,
				"Reference": e.Reference, "Digest": e.Digest, "RuleID": e.RuleID} {
				Expect(len(v)).To(BeNumerically("<=", 256), name)
			}
		})

		It("counts an unknown method under OTHER", func() {
			l := New(10)
			l.Add(api.LogEntry{Host: "a", Method: "FOO", Status: 405})
			l.Add(api.LogEntry{Host: "a", Method: "BAR", Status: 405})
			l.Add(api.LogEntry{Host: "a", Method: http.MethodGet, Status: 405})
			st := l.Stats()
			Expect(st).To(HaveLen(2))
			byMethod := map[string]int64{}
			for _, s := range st {
				byMethod[s.Method] = s.Count
			}
			Expect(byMethod).To(Equal(map[string]int64{"OTHER": 2, "GET": 1}))
			Expect(l.Query(Filter{})[0].Method).To(Equal("FOO"), "the entry keeps its method")
		})

		It("counts new keys under an overflow host once the stats are full", func() {
			l := New(1)
			for i := range maxStatKeys {
				l.Add(api.LogEntry{Host: fmt.Sprintf("h%d", i), Method: http.MethodGet, Status: 200})
			}
			l.Add(api.LogEntry{Host: "new", Method: http.MethodGet, Status: 200})
			l.Add(api.LogEntry{Host: "h0", Method: http.MethodGet, Status: 200})
			counts := map[string]int64{}
			for _, s := range l.Stats() {
				counts[s.Host] = s.Count
			}
			Expect(counts).To(HaveLen(maxStatKeys + 1))
			Expect(counts).NotTo(HaveKey("new"))
			Expect(counts).To(HaveKeyWithValue("(overflow)", BeEquivalentTo(1)))
			Expect(counts).To(HaveKeyWithValue("h0", BeEquivalentTo(2)))
		})

		It("buckets the entries of unknown hosts together in the stats only", func() {
			l := New(10)
			l.Add(api.LogEntry{Host: "a.example", UnknownHost: true, Status: 404})
			l.Add(api.LogEntry{Host: "b.example", UnknownHost: true, Status: 404})
			st := l.Stats()
			Expect(st).To(HaveLen(1))
			Expect(st[0].Host).To(Equal("(unknown)"))
			Expect(st[0].Count).To(BeEquivalentTo(2))
			es := l.Query(Filter{})
			Expect(es).To(HaveLen(2))
			Expect(es[0].Host).To(Equal("a.example"))
			Expect(es[1].Host).To(Equal("b.example"))
		})
	})

	Describe("Query", func() {
		t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		var l *Log

		BeforeEach(func() {
			l = New(10)
			l.Add(api.LogEntry{Time: t0, Host: "a", Repo: "r1", Caller: callerKuik, Kind: api.KindBlob, Status: 200})
			l.Add(api.LogEntry{Time: t0.Add(time.Hour), Host: "b", Repo: "r2", Caller: "crane", Kind: api.KindPing, Status: 404})
		})

		DescribeTable("filtering the entries",
			func(f Filter, want int) {
				Expect(l.Query(f)).To(HaveLen(want))
			},
			Entry("any", Filter{}, 2),
			Entry("host", Filter{Host: "a"}, 1),
			Entry("repo", Filter{Repo: "r2"}, 1),
			Entry("caller", Filter{Caller: callerKuik}, 1),
			Entry("kind", Filter{Kind: api.KindPing}, 1),
			Entry("status", Filter{Status: new(200)}, 1),
			Entry("status zero", Filter{Status: new(0)}, 0),
			Entry("since inclusive", Filter{Since: t0}, 2),
			Entry("since later", Filter{Since: t0.Add(time.Minute)}, 1),
			Entry("no match", Filter{Host: "a", Repo: "r2"}, 0),
		)
	})

	Describe("Stats", func() {
		It("survives the ring wrapping and a reset", func() {
			l := New(2)
			for range 5 {
				l.Add(api.LogEntry{Host: "a", Kind: api.KindBlob, Method: http.MethodGet, Status: 200})
			}
			st := l.Stats()
			Expect(st).To(HaveLen(1))
			Expect(st[0].Count).To(BeEquivalentTo(5))
			l.ResetStats()
			Expect(l.Stats()).To(BeEmpty(), "after reset")
		})

		It("sorts the rows by host then status", func() {
			l := New(10)
			l.Add(api.LogEntry{Host: "b", Method: http.MethodGet, Status: 200})
			l.Add(api.LogEntry{Host: "a", Method: http.MethodGet, Status: 404})
			l.Add(api.LogEntry{Host: "a", Method: http.MethodGet, Status: 200})
			st := l.Stats()
			Expect(st).To(HaveLen(3))
			Expect(st[0].Host).To(Equal("a"))
			Expect(st[0].Status).To(Equal(200))
			Expect(st[1].Status).To(Equal(404))
			Expect(st[2].Host).To(Equal("b"))
		})
	})

	Describe("Subscribe", func() {
		It("drops entries when the subscriber is slow", func() {
			l := New(10)
			ch, unsub := l.Subscribe()
			defer unsub()
			for range subBuffer * 2 {
				l.Add(api.LogEntry{})
			}
			Expect(ch).To(HaveLen(subBuffer), "the rest is dropped")
		})

		It("delivers the added entries", func() {
			l := New(10)
			ch, unsub := l.Subscribe()
			defer unsub()
			l.Add(api.LogEntry{Host: "x"})
			var e api.LogEntry
			Eventually(ch).WithTimeout(time.Second).Should(Receive(&e), "no entry received")
			Expect(e.Host).To(Equal("x"))
		})

		It("closes the channel on unsubscribe, idempotently", func() {
			l := New(10)
			ch, unsub := l.Subscribe()
			unsub()
			Expect(ch).To(BeClosed())
			unsub() // idempotent
			l.Add(api.LogEntry{})
		})
	})

	Describe("Subscribers", func() {
		It("counts the live subscribers", func() {
			l := New(1)
			_, un := l.Subscribe()
			Expect(l.Subscribers()).To(Equal(1))
			un()
			Expect(l.Subscribers()).To(BeZero())
		})
	})
})

var _ = Describe("LogEntry", func() {
	It("marshals to JSON with milliseconds and no unknownHost", func() {
		b, err := json.Marshal(api.LogEntry{ClientIP: "1.2.3.4", Duration: 1500 * time.Microsecond})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"durationMs":1.5`))
		Expect(string(b)).To(ContainSubstring(`"clientIp":"1.2.3.4"`))
		Expect(string(b)).NotTo(ContainSubstring("unknownHost"), "unknownHost should be omitted")
	})
})
