package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

func addEntries(l *reqlog.Log) {
	l.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Repo: "a", Kind: api.KindManifestByTag, Status: 503, Method: http.MethodGet})
	l.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Repo: "a", Kind: api.KindManifestByTag, Status: 200, Method: http.MethodGet})
	l.Add(api.LogEntry{Time: time.Now(), Host: other, Repo: "b", Status: 503, Method: http.MethodGet})
}

func waitSubscribers(l *reqlog.Log, want int) {
	GinkgoHelper()
	Eventually(l.Subscribers).WithTimeout(3*time.Second).WithPolling(5*time.Millisecond).Should(Equal(want), "subscribers")
}

var _ = Describe("Requests API", func() {
	It("reports the filter values parseFilter consumed", func() {
		_, used, err := parseFilter(url.Values{"host": {"up"}, "kind": {""}, "status": {"503"}, "x": {"1"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(used).To(Equal(url.Values{"host": {"up"}, "status": {"503"}}))
	})

	It("filters the requests and validates the query", func() {
		e := newAPI()
		addEntries(e.log)
		es := getJSON[[]api.LogEntry](e, 200, http.MethodGet, "/requests?host=upstream&status=503", "")
		Expect(es).To(HaveLen(1), "entries = %+v", es)
		Expect(es[0].Host).To(Equal(upstreamHost), "entries = %+v", es)
		Expect(es[0].Status).To(Equal(503), "entries = %+v", es)
		future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		Expect(e.mustDo(200, http.MethodGet, "/requests?since="+future, "")).To(Equal("[]"), "since future")
		e.mustDo(400, http.MethodGet, "/requests?status=abc", "")
		e.mustDo(400, http.MethodGet, "/requests?since=yesterday", "")
		byKind := getJSON[[]api.LogEntry](e, 200, http.MethodGet, "/requests?kind=manifest-by-tag", "")
		Expect(byKind).To(HaveLen(2), "kind=manifest-by-tag = %+v", byKind)
		for _, en := range byKind {
			Expect(en.Kind).To(Equal(api.KindManifestByTag), "kind")
		}
		Expect(e.mustDo(200, http.MethodGet, "/requests?kind=blob", "")).To(Equal("[]"), "kind=blob")
		Expect(e.mustDo(200, http.MethodGet, "/stats", "")).To(ContainSubstring(`"count"`), "stats")
	})

	DescribeTable("refuses an unknown kind",
		func(path string) {
			e := newAPI()
			code, _ := e.do(http.MethodGet, path+"?kind=manifests", "")
			Expect(code).To(Equal(400), "GET %s?kind=manifests", path)
		},
		Entry("on the list", requestsPath),
		Entry("on the export", "/requests/export"),
		Entry("on the stream", "/requests/stream"),
	)

	It("exports the requests as NDJSON", func() {
		e := newAPI()
		addEntries(e.log)
		req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/requests/export", nil)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/x-ndjson"), "content-type")
		n := 0
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var m map[string]any
			Expect(json.Unmarshal(sc.Bytes(), &m)).To(Succeed(), "line %q", sc.Text())
			n++
		}
		Expect(n).To(Equal(3), "lines")
	})

	It("streams the matching requests as server-sent events and drops the subscriber on cancel", func() {
		e := newAPI()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/api/v1/requests/stream?host=upstream&kind=manifest-by-tag", nil)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.Header.Get("Content-Type")).To(Equal("text/event-stream"), "content-type")
		waitSubscribers(e.log, 1)
		e.log.Add(api.LogEntry{Host: other, Status: 200})
		e.log.Add(api.LogEntry{Host: upstreamHost, Kind: api.KindBlob, Status: 200})
		e.log.Add(api.LogEntry{Host: upstreamHost, Kind: api.KindManifestByTag, Status: 503})

		sc := bufio.NewScanner(resp.Body)
		var event, data string
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = v
				break
			}
		}
		Expect(event).To(Equal("request"), "event")
		var got api.LogEntry
		Expect(json.Unmarshal([]byte(data), &got)).To(Succeed(), "data = %q", data)
		Expect(got.Host).To(Equal(upstreamHost), "data = %q", data)
		Expect(got.Kind).To(Equal(api.KindManifestByTag), "data = %q", data)
		Expect(got.Status).To(Equal(503), "data = %q", data)
		cancel()
		waitSubscribers(e.log, 0)
	})

	It("resets the stats", func() {
		e := newAPI()
		addEntries(e.log)
		e.mustDo(204, "POST", "/stats/reset", "")
		Expect(e.mustDo(200, http.MethodGet, "/stats", "")).To(Equal("[]"), "stats")
	})

	It("filters on a zero status", func() {
		e := newAPI()
		e.log.Add(api.LogEntry{Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 0})
		e.log.Add(api.LogEntry{Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
		count := func(q string) int {
			GinkgoHelper()
			return len(getJSON[[]api.LogEntry](e, 200, http.MethodGet, requestsPath+q, ""))
		}
		Expect(count("?status=0")).To(Equal(1), "status=0")
		Expect(count("")).To(Equal(2), "no status")
		Expect(count("?status=200")).To(Equal(1), "status=200")
	})
})
