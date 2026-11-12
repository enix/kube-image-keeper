package admin

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("Admin API purge", func() {
	DescribeTable("DELETE on a collection empties it and counts",
		func(kind, create string) {
			e := newAPI()
			e.mustDo(http.StatusOK, http.MethodPut, "/users/u1", `{"password":"pw"}`) // named by the grants
			e.mustDo(http.StatusCreated, http.MethodPost, "/"+kind, create)
			e.mustDo(http.StatusCreated, http.MethodPost, "/"+kind, create)
			Expect(e.mustDo(http.StatusOK, http.MethodDelete, "/"+kind, "")).To(Equal(`{"deleted":2}`))
			Expect(e.mustDo(http.StatusOK, http.MethodGet, "/"+kind, "")).To(Equal("[]"))
			Expect(e.mustDo(http.StatusOK, http.MethodDelete, "/"+kind, "")).To(Equal(`{"deleted":0}`))
		},
		Entry("rules", "rules", `{"enabled":true,"effects":{"status":503}}`),
		Entry("aliases", "aliases", `{"host":"upstream","from":"a","to":"b"}`),
		Entry("divergents", "divergents", `{"host":"upstream","repo":"acme/foo","salt":"s"}`),
		Entry("grants", "grants", `{"host":"private","repo":"**","actions":["pull"],"users":["u1"]}`),
		Entry("platforms", "platforms", `{"host":"upstream","repo":"acme/foo","platforms":["linux/amd64"]}`),
	)

	It("purges the name-keyed lists", func() {
		e := newAPI()
		e.mustDo(http.StatusOK, http.MethodPut, "/users/u1", `{"password":"pw"}`)
		e.mustDo(http.StatusOK, http.MethodPut, "/hostauth/upstream", `{"scheme":"bearer"}`)
		e.mustDo(http.StatusOK, http.MethodPut, "/quotas/q1", `{"limit":2,"windowSec":60,"key":"ip"}`)
		for _, kind := range []string{kindUsers, kindHostAuth, kindQuotas} {
			Expect(e.mustDo(http.StatusOK, http.MethodDelete, "/"+kind, "")).To(Equal(`{"deleted":1}`), kind)
			Expect(e.mustDo(http.StatusOK, http.MethodGet, "/"+kind, "")).To(Equal("[]"), kind)
		}
	})

	It("keeps DELETE /rules/{id} working next to DELETE /rules", func() {
		e := newAPI()
		r := getJSON[api.RuleResponse](e, http.StatusCreated, http.MethodPost, "/rules", `{"enabled":true,"effects":{"status":503}}`)
		e.mustDo(http.StatusNoContent, http.MethodDelete, "/rules/"+r.Rule.ID, "")
		e.mustDo(http.StatusNotFound, http.MethodDelete, "/rules/"+r.Rule.ID, "")
	})

	It("answers 400 when users are still named by a grant", func() {
		e := newAPI()
		e.mustDo(http.StatusOK, http.MethodPut, "/users/u1", `{"password":"pw"}`)
		e.mustDo(http.StatusCreated, http.MethodPost, "/grants", `{"host":"private","repo":"**","actions":["pull"],"users":["u1"]}`)
		e.mustDo(http.StatusBadRequest, http.MethodDelete, "/users", "")
	})

	It("refuses a cross-origin purge", func() {
		e := newAPI()
		code, _ := e.do(http.MethodDelete, "/rules", "", "Origin", "http://evil.example")
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("purges the images", func() {
		e := newAPI()
		e.mustDo(http.StatusOK, http.MethodPost, "/resolve", `{"ref":"upstream/acme/foo:t1"}`)
		e.mustDo(http.StatusOK, http.MethodPost, "/drift", `{"host":"upstream","repo":"acme/foo","tag":"t1"}`)
		Expect(e.mustDo(http.StatusOK, http.MethodDelete, "/images", "")).To(Equal(`{"deleted":1}`))
		Expect(e.mustDo(http.StatusOK, http.MethodGet, "/images", "")).To(Equal("[]"))
	})

	It("purges the request log and keeps the stats", func() {
		e := newAPI()
		e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
		e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
		Expect(e.mustDo(http.StatusOK, http.MethodDelete, "/log", "")).To(Equal(`{"deleted":2}`))
		Expect(e.mustDo(http.StatusOK, http.MethodGet, "/requests", "")).To(Equal("[]"))
		Expect(e.log.Stats()).NotTo(BeEmpty())
	})

	Describe("POST /purge", func() {
		It("purges everything, the log and its stats included, and leaves the backends", func() {
			e := newAPI()
			e.mustDo(http.StatusCreated, http.MethodPost, "/rules", `{"enabled":true,"effects":{"status":503}}`)
			e.mustDo(http.StatusNoContent, http.MethodPost, "/destinations/mirror/push", `{"repo":"acme/foo","tag":"t1"}`)
			e.log.Add(api.LogEntry{Time: time.Now(), Host: upstreamHost, Method: http.MethodGet, Kind: api.KindPing, Status: 200})
			res := getJSON[api.PurgeAllResult](e, http.StatusOK, http.MethodPost, "/purge", `{"backends":false}`)
			Expect(res.Rules).To(Equal(1))
			Expect(res.Requests).To(Equal(1))
			Expect(res.Backends).To(BeEmpty())
			Expect(e.mustDo(http.StatusOK, http.MethodGet, "/rules", "")).To(Equal("[]"))
			Expect(e.log.Stats()).To(BeEmpty())
			Expect(e.mustDo(http.StatusOK, http.MethodGet, "/destinations/mirror", "")).To(ContainSubstring("acme/foo"))
		})

		It("purges the backends on request, answering 200", func() {
			e := newAPI()
			e.mustDo(http.StatusNoContent, http.MethodPost, "/destinations/mirror/push", `{"repo":"acme/foo","tag":"t1"}`)
			res := getJSON[api.PurgeAllResult](e, http.StatusOK, http.MethodPost, "/purge", `{"backends":true}`)
			Expect(res.Backends).To(HaveLen(1))
			Expect(res.Backends[0].DeletedTags).To(Equal(1))
			Expect(res.Backends[0].Repositories).To(ConsistOf("acme/foo"))
		})

		DescribeTable("refuses a bad body",
			func(body string) { newAPI().mustDo(http.StatusBadRequest, http.MethodPost, "/purge", body) },
			Entry("empty", ""),
			Entry("not JSON", "yes"),
			Entry("an unknown field", `{"backend":true}`),
		)

		It("refuses a cross-origin purge and changes nothing", func() {
			e := newAPI()
			e.mustDo(http.StatusCreated, http.MethodPost, "/rules", `{"enabled":true,"effects":{"status":503}}`)
			code, _ := e.do(http.MethodPost, "/purge", `{"backends":false}`, "Origin", "http://evil.example")
			Expect(code).To(Equal(http.StatusForbidden))
			Expect(e.svc.ListRules()).To(HaveLen(1))
		})

		It("only answers POST", func() {
			newAPI().mustDo(http.StatusMethodNotAllowed, http.MethodGet, "/purge", "")
		})
	})

	It("purges every repository of a destination", func() {
		e := newAPI()
		e.mustDo(http.StatusNoContent, http.MethodPost, "/destinations/mirror/push", `{"repo":"acme/foo","tag":"t1"}`)
		e.mustDo(http.StatusNoContent, http.MethodPost, "/destinations/mirror/push", `{"repo":"other/bar","tag":"t1"}`)
		res := getJSON[api.PurgeResult](e, http.StatusOK, http.MethodPost, "/destinations/mirror/purge-all", "")
		Expect(res.DeletedTags).To(Equal(2))
		Expect(res.Repositories).To(ConsistOf("acme/foo", "other/bar"))
		e.mustDo(http.StatusNotFound, http.MethodPost, "/destinations/nope/purge-all", "")
		e.mustDo(http.StatusMethodNotAllowed, http.MethodGet, "/destinations/mirror/purge-all", "")
	})
})
