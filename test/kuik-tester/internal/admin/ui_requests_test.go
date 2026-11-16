package admin

import (
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("UI requests", func() {
	It("resets the stats and answers the stats fragment", func() {
		e := newUIEnv()
		e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
		Expect(e.log.Stats()).NotTo(BeEmpty())
		uiExpectFragment(e.post("/ui/stats/reset", url.Values{}), "stats")
		Expect(e.log.Stats()).To(BeEmpty())
	})

	It("reports an unknown kind without the live table", func() {
		rec := newUIEnv().get("/requests?kind=nope")
		Expect(rec.Code).To(Equal(200))
		Expect(rec.Body.String()).To(ContainSubstring(uiClassErr))
		Expect(rec.Body.String()).NotTo(ContainSubstring(`id="live"`))
	})

	It("renders the time of an entry as a time element", func() {
		e := newUIEnv()
		ts := time.Date(2026, 1, 2, 3, 4, 5, 6000000, time.UTC)
		e.log.Add(api.LogEntry{Time: ts, Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
		Expect(uiAttrs(e.get(requestsPath).Body.String(), "datetime")).To(ConsistOf("2026-01-02T03:04:05.006Z"))
	})
})
