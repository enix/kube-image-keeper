package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/admin"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	rulePlaceholder = "{rule}"
	formType        = "application/x-www-form-urlencoded"
	jsonType        = "application/json"
	evilOrigin      = "http://evil.example"
	ownOrigin       = "http://example.com"
	uiRulesPath     = "/ui/rules"
	ruleForm        = "status=503&enabled=on"
)

// newAdminEnv builds the admin handler on a state holding one rule and a
// request log holding one entry, and returns the handler, the state and the
// rule's ID.
func newAdminEnv() (http.Handler, *state.Store, string) {
	GinkgoHelper()
	be := testutil.NewBackend()
	DeferCleanup(be.Close)
	cfg := &testerconfig.Config{
		BaseDomain: "kuik.test",
		Proxies:    []testerconfig.ProxyHost{{Name: "mirror", Backend: be.URL}},
	}
	Expect(cfg.Resolve()).To(Succeed())
	st, err := state.Open("")
	Expect(err).NotTo(HaveOccurred())
	proxies, err := dest.NewProxies(cfg)
	Expect(err).NotTo(HaveOccurred())
	svc := admin.NewService(cfg, st, quota.New(), proxies)
	created, err := svc.CreateRule(api.Rule{Enabled: true, Effects: api.Effects{Status: 503}})
	Expect(err).NotTo(HaveOccurred())
	log := reqlog.New(10)
	log.Add(api.LogEntry{Time: time.Now(), Host: "mirror", Method: http.MethodGet, Status: http.StatusOK})
	return newAdminHandler(svc, log), st, created.ID
}

// serveAdmin serves r through a request context cancelled after 200 ms: the
// SSE route returns then, the others return at once.
func serveAdmin(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

// adminWrite describes a write request to the admin handler.
type adminWrite struct{ method, path, contentType, body, origin string }

func (w adminWrite) request() *http.Request {
	r := httptest.NewRequest(w.method, w.path, strings.NewReader(w.body))
	r.Header.Set("Content-Type", w.contentType)
	r.Header.Set("Origin", w.origin)
	return r
}

var _ = Describe("newAdminHandler", func() {
	DescribeTable("refuses a write from another origin and changes nothing",
		func(method, path, contentType, body string) {
			h, st, id := newAdminEnv()
			before := st.Snapshot()
			path = strings.ReplaceAll(path, rulePlaceholder, id)
			w := adminWrite{method: method, path: path, contentType: contentType, body: body, origin: evilOrigin}
			rec := serveAdmin(h, w.request())
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(st.Snapshot()).To(BeIdenticalTo(before))
		},
		Entry("a UI rule creation", http.MethodPost, uiRulesPath, formType, ruleForm),
		Entry("a UI stats reset", http.MethodPost, "/ui/stats/reset", formType, ""),
		Entry("an API rule creation", http.MethodPost, "/api/v1/rules", jsonType, `{"effects":{"status":503}}`),
		Entry("an API rule deletion", http.MethodDelete, "/api/v1/rules/"+rulePlaceholder, jsonType, ""),
	)

	It("accepts the same write from its own origin", func() {
		h, st, _ := newAdminEnv()
		before := len(st.Snapshot().Rules)
		w := adminWrite{method: http.MethodPost, path: uiRulesPath, contentType: formType, body: ruleForm, origin: ownOrigin}
		rec := serveAdmin(h, w.request())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(st.Snapshot().Rules).To(HaveLen(before + 1))
	})

	// One Entry per GET pattern of the admin handler: http.ServeMux cannot be
	// enumerated, so a new GET route needs an entry here.
	DescribeTable("serves every GET route without changing the state",
		func(path string) {
			h, st, id := newAdminEnv()
			before := st.Snapshot()
			path = strings.ReplaceAll(path, rulePlaceholder, url.PathEscape(id))
			rec := serveAdmin(h, httptest.NewRequest(http.MethodGet, path, http.NoBody))
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(st.Snapshot()).To(BeIdenticalTo(before))
		},
		Entry("the health check", "/healthz"),
		Entry("the UI home", "/"),
		Entry("the UI rules page", "/rules"),
		Entry("the UI settings page", "/settings"),
		Entry("the UI destinations page", "/destinations"),
		Entry("the UI requests page", "/requests"),
		Entry("the UI state page", "/state"),
		Entry("the stylesheet", "/static/style.css"),
		Entry("the new rule form", "/ui/rules/form"),
		Entry("the rule edit form", "/ui/rules/"+rulePlaceholder+"/edit"),
		Entry("the quota usage fragment", "/ui/settings/quota-usage"),
		Entry("the API state", "/api/v1/state"),
		Entry("the API export", "/api/v1/export"),
		Entry("the API sets", "/api/v1/sets"),
		Entry("the API rules", "/api/v1/rules"),
		Entry("the API aliases", "/api/v1/aliases"),
		Entry("the API divergents", "/api/v1/divergents"),
		Entry("the API grants", "/api/v1/grants"),
		Entry("the API platforms", "/api/v1/platforms"),
		Entry("the API users", "/api/v1/users"),
		Entry("the API host auth", "/api/v1/hostauth"),
		Entry("the API quotas", "/api/v1/quotas"),
		Entry("the API quota usage", "/api/v1/quotas/usage"),
		Entry("the API images", "/api/v1/images"),
		Entry("the API destinations", "/api/v1/destinations"),
		Entry("the API destination inventory", "/api/v1/destinations/mirror"),
		Entry("the API requests", "/api/v1/requests"),
		Entry("the API requests export", "/api/v1/requests/export"),
		Entry("the API requests stream", "/api/v1/requests/stream"),
		Entry("the API stats", "/api/v1/stats"),
	)
})
