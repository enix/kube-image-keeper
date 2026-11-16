package reqlog

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("Log.Clear", func() {
	It("empties a partial ring, keeps the stats and the subscribers, and accepts new entries", func() {
		l := New(5)
		ch, unsub := l.Subscribe()
		defer unsub()
		l.Add(api.LogEntry{Host: "a", Method: http.MethodGet, Status: 200})
		l.Add(api.LogEntry{Host: "a", Method: http.MethodGet, Status: 200})
		Expect(l.Clear()).To(Equal(2))
		Expect(l.Query(Filter{})).To(BeEmpty())
		Expect(l.Stats()).To(HaveLen(1))
		Expect(l.Subscribers()).To(Equal(1))
		l.Add(api.LogEntry{Host: "b", Method: http.MethodGet, Status: 200})
		Expect(l.Query(Filter{})).To(HaveLen(1))
		Expect(ch).To(HaveLen(3))
	})

	It("empties a full, wrapped ring", func() {
		l := New(3)
		for range 7 {
			l.Add(api.LogEntry{Host: "a", Method: http.MethodGet, Status: 200})
		}
		Expect(l.Clear()).To(Equal(3))
		Expect(l.Query(Filter{})).To(BeEmpty())
		l.Add(api.LogEntry{Host: "x", Method: http.MethodGet, Status: 200})
		es := l.Query(Filter{})
		Expect(es).To(HaveLen(1))
		Expect(es[0].Host).To(Equal("x"))
	})

	It("returns 0 on an empty log", func() {
		Expect(New(3).Clear()).To(BeZero())
	})
})
