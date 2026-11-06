package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

type apiEnv struct {
	srv *httptest.Server
	svc *Service
	log *reqlog.Log
}

func newAPI() *apiEnv { return newAPIWith(nil) }

func newAPIWith(cfg *testerconfig.Config) *apiEnv {
	GinkgoHelper()
	svc, _, _ := newService(cfg)
	l := reqlog.New(100)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", NewAPI(svc, l)))
	srv := httptest.NewServer(SameOrigin(mux))
	DeferCleanup(srv.Close)
	return &apiEnv{srv, svc, l}
}

func (e *apiEnv) do(method, path, body string, hdr ...string) (int, string) {
	GinkgoHelper()
	req, err := http.NewRequest(method, e.srv.URL+"/api/v1"+path, strings.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func (e *apiEnv) mustDo(want int, method, path, body string) string {
	GinkgoHelper()
	code, b := e.do(method, path, body)
	Expect(code).To(Equal(want), "%s %s = %d %s, want %d", method, path, code, b, want)
	return b
}

// getJSON does a request that must answer want and decodes its JSON body.
func getJSON[T any](e *apiEnv, want int, method, path, body string) T {
	GinkgoHelper()
	var v T
	Expect(json.Unmarshal([]byte(e.mustDo(want, method, path, body)), &v)).To(Succeed(), "%s %s", method, path)
	return v
}

// jsonAt returns the value at a dotted path of a decoded JSON value; a
// numeric segment indexes an array. nil when the path leads nowhere.
func jsonAt(v any, path string) any {
	for seg := range strings.SplitSeq(path, ".") {
		switch t := v.(type) {
		case map[string]any:
			v = t[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(t) {
				return nil
			}
			v = t[i]
		default:
			return nil
		}
	}
	return v
}

func ruleList(e *apiEnv) []api.Rule {
	GinkgoHelper()
	return getJSON[[]api.Rule](e, 200, http.MethodGet, "/rules", "")
}

var _ = Describe("Admin API", func() {
	Describe("list endpoints", func() {
		DescribeTable("answer an empty array on a fresh state",
			func(path string) {
				e := newAPI()
				Expect(e.mustDo(200, http.MethodGet, path, "")).To(Equal("[]"), "GET %s", path)
			},
			Entry("rules", "/rules"),
			Entry("aliases", "/aliases"),
			Entry("divergents", "/divergents"),
			Entry("grants", "/grants"),
			Entry("users", "/users"),
			Entry("host auth", "/hostauth"),
			Entry("quotas", "/quotas"),
			Entry("images", "/images"),
			Entry("requests", requestsPath),
			Entry("stats", "/stats"),
		)

		It("answers the configured destinations and empty arrays in the state", func() {
			e := newAPI()
			Expect(e.mustDo(200, http.MethodGet, "/destinations", "")).To(Equal(`["mirror"]`), "destinations")
			e2 := newAPIWith(&testerconfig.Config{BaseDomain: baseDomain})
			Expect(e2.mustDo(200, http.MethodGet, "/destinations", "")).To(Equal("[]"), "empty destinations")
			st := getJSON[map[string]json.RawMessage](e, 200, http.MethodGet, "/state", "")
			for _, f := range []string{"rules", "aliases", "divergents", formUsers, "grants", "hostAuth", "quotas"} {
				Expect(string(st[f])).To(Equal("[]"), "state.%s", f)
			}
		})
	})

	Describe("rules", func() {
		It("round-trips create, move, enable, update and delete", func() {
			e := newAPI()
			var created struct {
				Rule     struct{ ID string } `json:"rule"`
				Warnings []string            `json:"warnings"`
			}
			post := func(body string) string {
				Expect(json.Unmarshal([]byte(e.mustDo(201, "POST", "/rules", body)), &created)).To(Succeed())
				return created.Rule.ID
			}
			a := post(`{"enabled":true,"effects":{"status":503}}`)
			b := post(`{"enabled":true,"effects":{"status":429}}`)
			rs := ruleList(e)
			Expect(rs).To(HaveLen(2), "order = %+v", rs)
			Expect(rs[0].ID).To(Equal(b), "order = %+v", rs)
			Expect(rs[1].ID).To(Equal(a), "order = %+v", rs)
			e.mustDo(204, "POST", "/rules/"+b+"/move", `{"index":1}`)
			rs = ruleList(e)
			Expect(rs[0].ID).To(Equal(a), "after move = %+v", rs)
			Expect(rs[1].ID).To(Equal(b), "after move = %+v", rs)
			e.mustDo(204, "POST", "/rules/"+b+"/enabled", `{"enabled":false}`)
			Expect(ruleList(e)[1].Enabled).To(BeFalse(), "still enabled")
			var put struct {
				Rule     struct{ ID string } `json:"rule"`
				Warnings []string            `json:"warnings"`
			}
			body := e.mustDo(200, "PUT", "/rules/"+a, `{"id":"ignored","enabled":true,"target":{"caller":"anonymous"},"effects":{"status":503}}`)
			Expect(json.Unmarshal([]byte(body), &put)).To(Succeed())
			Expect(put.Rule.ID).To(Equal(a), "put = %s", body)
			Expect(put.Warnings).To(HaveLen(1), "put = %s", body)
			e.mustDo(204, "DELETE", "/rules/"+a, "")
			rs = ruleList(e)
			Expect(rs).To(HaveLen(1), "after delete = %+v", rs)
			Expect(rs[0].ID).To(Equal(b), "after delete = %+v", rs)
			// a rule without warnings carries an empty array
			Expect(e.mustDo(201, "POST", "/rules", `{"enabled":true,"effects":{"status":503}}`)).To(ContainSubstring(`"warnings":[]`))
		})

		It("refuses a bad body and an unknown rule", func() {
			e := newAPI()
			code, b := e.do("POST", "/rules", `{"enabled":true,"bogus":1}`)
			Expect(code).To(Equal(400), "unknown field = %s", b)
			Expect(b).To(ContainSubstring(`"error"`), "unknown field")
			code, b = e.do("POST", "/rules", `{"enabled":true,"effects":{"status":302}}`)
			Expect(code).To(Equal(400), "status 302 = %s", b)
			Expect(b).To(ContainSubstring(formStatus), "status 302")
			code, _ = e.do("PUT", "/rules/nope", `{"enabled":true,"effects":{"status":503}}`)
			Expect(code).To(Equal(404), "PUT unknown")
			code, _ = e.do("DELETE", "/rules/nope", "")
			Expect(code).To(Equal(404), "DELETE unknown")
			code, _ = e.do("POST", "/rules", `{"enabled":true} {"x":1}`)
			Expect(code).To(Equal(400), "trailing data")
		})

		It("defaults enabled to true when the body omits it", func() {
			e := newAPI()
			var res struct {
				Rule struct {
					ID      string
					Enabled bool
				}
			}
			body := e.mustDo(201, "POST", "/rules", `{"target":{"host":"upstream"},"effects":{"status":503}}`)
			Expect(json.Unmarshal([]byte(body), &res)).To(Succeed(), "created = %s", body)
			Expect(res.Rule.Enabled).To(BeTrue(), "created = %s", body)
			body = e.mustDo(201, "POST", "/rules", `{"enabled":false,"effects":{"status":503}}`)
			Expect(json.Unmarshal([]byte(body), &res)).To(Succeed(), "explicit false = %s", body)
			Expect(res.Rule.Enabled).To(BeFalse(), "explicit false = %s", body)
			id := res.Rule.ID
			body = e.mustDo(200, "PUT", "/rules/"+id, `{"effects":{"status":502}}`)
			Expect(json.Unmarshal([]byte(body), &res)).To(Succeed(), "PUT omitted = %s", body)
			Expect(res.Rule.Enabled).To(BeTrue(), "PUT omitted = %s", body)
			body = e.mustDo(200, "PUT", "/rules/"+id, `{"enabled":false,"effects":{"status":502}}`)
			Expect(json.Unmarshal([]byte(body), &res)).To(Succeed(), "PUT false = %s", body)
			Expect(res.Rule.Enabled).To(BeFalse(), "PUT false = %s", body)
		})

		DescribeTable("answers a write with the normalised stored object",
			func(method, route, create, body, field, want string) {
				e := newAPI()
				e.mustDo(200, http.MethodPut, "/users/bob", `{"password":"pw"}`)
				base, status := route, 200
				if method == http.MethodPost {
					status = 201
				}
				if strings.Contains(route, "{id}") {
					base = strings.Split(route, "/{id}")[0]
					created := getJSON[map[string]any](e, 201, http.MethodPost, base, create)
					id := jsonAt(created, "id")
					if id == nil {
						id = jsonAt(created, "rule.id")
					}
					route = strings.Replace(route, "{id}", id.(string), 1)
				} else if method == http.MethodPut {
					base = route[:strings.LastIndex(route, "/")]
				}
				answer := getJSON[map[string]any](e, status, method, route, body)
				Expect(jsonAt(answer, field)).To(Equal(want), "%s %s answer", method, route)
				listed := getJSON[[]map[string]any](e, 200, http.MethodGet, base, "")
				Expect(listed).NotTo(BeEmpty(), "GET %s", base)
				Expect(jsonAt(listed[0], strings.TrimPrefix(field, "rule."))).To(Equal(want), "GET %s", base)
			},
			Entry("POST /rules lower-cases the host", http.MethodPost, "/rules", "", `{"target":{"host":"UpStream"},"effects":{"status":503}}`, "rule.target.host", upstreamHost),
			Entry("POST /rules upper-cases the methods", http.MethodPost, "/rules", "", `{"target":{"methods":["get"]},"effects":{"status":503}}`, "rule.target.methods.0", "GET"),
			Entry("PUT /rules/{id} lower-cases the host", http.MethodPut, "/rules/{id}", `{"target":{"host":"upstream"},"effects":{"status":503}}`, `{"target":{"host":"UPSTREAM"},"effects":{"status":503}}`, "rule.target.host", upstreamHost),
			Entry("POST /aliases lower-cases the host", http.MethodPost, "/aliases", "", `{"host":"UpStream","from":"a/x","to":"b/x"}`, formHost, upstreamHost),
			Entry("PUT /aliases/{id} lower-cases the host", http.MethodPut, "/aliases/{id}", `{"host":"upstream","from":"a/x","to":"b/x"}`, `{"host":"UPSTREAM","from":"a/x","to":"b/x"}`, formHost, upstreamHost),
			Entry("POST /divergents lower-cases the host", http.MethodPost, "/divergents", "", `{"host":"UP","repo":"a","salt":"s"}`, formHost, "up"),
			Entry("PUT /divergents/{id} lower-cases the host", http.MethodPut, "/divergents/{id}", `{"host":"up","repo":"a","salt":"s"}`, `{"host":"UP","repo":"a","salt":"s"}`, formHost, "up"),
			Entry("POST /grants lower-cases the host", http.MethodPost, "/grants", "", `{"host":"UP","repo":"a","actions":["pull"],"users":["bob"]}`, formHost, "up"),
			Entry("PUT /grants/{id} lower-cases the host", http.MethodPut, "/grants/{id}", `{"host":"up","repo":"a","actions":["pull"],"users":["bob"]}`, `{"host":"UP","repo":"a","actions":["pull"],"users":["bob"]}`, formHost, "up"),
			Entry("POST /platforms lower-cases the host", http.MethodPost, "/platforms", "", `{"host":"UP","repo":"a","platforms":["linux/amd64"]}`, formHost, "up"),
			Entry("PUT /platforms/{id} lower-cases the host", http.MethodPut, "/platforms/{id}", `{"host":"up","repo":"a","platforms":["linux/amd64"]}`, `{"host":"UP","repo":"a","platforms":["linux/amd64"]}`, formHost, "up"),
			Entry("PUT /hostauth/{host} lower-cases the host", http.MethodPut, "/hostauth/UP*", "", `{"scheme":"basic"}`, formHost, "up*"),
			Entry("PUT /users/{name} answers the name", http.MethodPut, "/users/bob", "", `{"password":"pw"}`, formName, bob),
			Entry("PUT /quotas/{name} upper-cases the methods", http.MethodPut, "/quotas/q1", "", `{"limit":2,"windowSec":60,"key":"ip","methods":["get"]}`, "methods.0", "GET"),
		)

		It("resets the counters of a quota put or deleted through the API", func() {
			e := newAPI()
			def := api.QuotaDef{Name: "q1", Limit: 2, WindowSec: 60, Key: "ip"}
			const body = `{"limit":2,"windowSec":60,"key":"ip"}`
			e.mustDo(200, http.MethodPut, "/quotas/q1", body)
			e.svc.quotas.Take(def, "", time.Now())
			Expect(getJSON[map[string]map[string]int](e, 200, http.MethodGet, "/quotas/usage", "")).To(HaveKey("q1"), "usage before the put")
			e.mustDo(200, http.MethodPut, "/quotas/q1", body)
			Expect(getJSON[map[string]map[string]int](e, 200, http.MethodGet, "/quotas/usage", "")).NotTo(HaveKey("q1"), "usage after the put")
			e.svc.quotas.Take(def, "", time.Now())
			e.mustDo(204, http.MethodDelete, "/quotas/q1", "")
			Expect(getJSON[map[string]map[string]int](e, 200, http.MethodGet, "/quotas/usage", "")).NotTo(HaveKey("q1"), "usage after the delete")
		})

		It("deletes a host auth named in another case", func() {
			e := newAPI()
			e.mustDo(200, http.MethodPut, "/hostauth/alt*", `{"scheme":"basic"}`)
			e.mustDo(204, http.MethodDelete, "/hostauth/ALT*", "")
			Expect(e.mustDo(200, http.MethodGet, "/hostauth", "")).To(Equal("[]"))
		})

		DescribeTable("refuses a bad write and an unknown key",
			func(method, route, body string, want int) {
				e := newAPI()
				code, b := e.do(method, route, body)
				Expect(code).To(Equal(want), "%s %s = %s", method, route, b)
			},
			Entry("a PUT on an id route with an unknown field", http.MethodPut, "/aliases/x", `{"bogus":1}`, 400),
			Entry("a PUT on an unknown id", http.MethodPut, "/aliases/x", `{"host":"upstream","from":"a/x","to":"b/x"}`, 404),
			Entry("a PUT on a name route with an unknown field", http.MethodPut, "/users/bob", `{"bogus":1}`, 400),
			Entry("an invalid PUT on a name route", http.MethodPut, "/quotas/q1", `{"limit":0}`, 400),
			Entry("a DELETE of an unknown name", http.MethodDelete, "/users/nobody", "", 404),
			Entry("a POST on an id route with an unknown field", http.MethodPost, "/grants", `{"bogus":1}`, 400),
		)

		DescribeTable("answers 413 to a JSON body over the limit",
			func(method, path, prefix string) {
				e := newAPI()
				code, b := e.do(method, path, prefix+strings.Repeat("a", maxBody)+"\"}")
				Expect(code).To(Equal(http.StatusRequestEntityTooLarge), "%s %s = %s", method, path, b)
				Expect(b).To(ContainSubstring(`"error"`))
			},
			Entry("a POST on an id route", http.MethodPost, "/rules", `{"note":"`),
			Entry("a PUT on a name route", http.MethodPut, "/users/bob", `{"password":"`),
			Entry("an action route", http.MethodPost, "/destinations/mirror/tag", `{"repo":"`),
		)

		It("reads a JSON body of exactly the limit", func() {
			e := newAPI()
			const prefix, suffix = `{"enabled":true,"note":"`, `"}`
			body := prefix + strings.Repeat("a", maxBody-len(prefix)-len(suffix)) + suffix
			Expect(body).To(HaveLen(maxBody))
			code, b := e.do(http.MethodPost, "/rules", body)
			Expect(code).To(Equal(http.StatusCreated), "POST /rules (exactly %d bytes) = %.200s", maxBody, b)
		})

		It("allows a write whose Origin host differs from the Host only in case", func() {
			h := SameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			req := httptest.NewRequest(http.MethodPost, "http://admin.example:8080/rules", nil)
			req.Header.Set("Origin", "http://ADMIN.EXAMPLE:8080")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusNoContent), "body = %s", rec.Body)
		})

		It("refuses a cross-origin write but serves a cross-origin read", func() {
			e := newAPI()
			body := `{"enabled":true,"effects":{"status":503}}`
			code, _ := e.do("POST", "/rules", body, "Origin", "http://evil.example")
			Expect(code).To(Equal(403), "cross-origin POST")
			code, _ = e.do("POST", "/rules", body, "Origin", e.srv.URL)
			Expect(code).To(Equal(201), "same-origin POST")
			code, _ = e.do(http.MethodGet, "/rules", "", "Origin", "http://evil.example")
			Expect(code).To(Equal(200), "cross-origin GET")
			code, _ = e.do("POST", "/rules", body, "Origin", "null")
			Expect(code).To(Equal(403), "null origin POST")
			Expect(ruleList(e)).To(HaveLen(1), "rules")
		})
	})

	Describe("path parameters", func() {
		It("decodes the path keys of the settings routes", func() {
			e := newAPI()
			e.mustDo(200, "PUT", "/hostauth/alt*", `{"scheme":"bearer"}`)
			ha := getJSON[[]api.HostAuth](e, 200, http.MethodGet, "/hostauth", "")
			Expect(ha).To(HaveLen(1), "hostauth = %+v", ha)
			Expect(ha[0].Host).To(Equal(hostGlob), "hostauth = %+v", ha)
			e.mustDo(200, "PUT", "/users/a%40b", `{"password":"pw"}`)
			us := getJSON[[]api.User](e, 200, http.MethodGet, "/users", "")
			Expect(us).To(HaveLen(1), "users = %+v", us)
			Expect(us[0].Name).To(Equal("a@b"), "users = %+v", us)
			e.mustDo(204, "DELETE", "/users/a%40b", "")
			e.mustDo(204, "DELETE", "/hostauth/alt*", "")
		})

	})

	Describe("settings", func() {
		It("supports CRUD on aliases and quotas", func() {
			e := newAPI()
			var a struct{ ID string }
			body := e.mustDo(201, "POST", "/aliases", `{"host":"upstream","from":"a/y","to":"b/y"}`)
			Expect(json.Unmarshal([]byte(body), &a)).To(Succeed(), "alias = %s", body)
			Expect(a.ID).NotTo(BeEmpty(), "alias = %s", body)
			e.mustDo(200, "PUT", "/aliases/"+a.ID, `{"host":"upstream","from":"a/x","to":"b/x"}`)
			Expect(e.mustDo(200, http.MethodGet, "/aliases", "")).To(ContainSubstring(`"from":"a/x"`), "aliases")
			e.mustDo(204, "DELETE", "/aliases/"+a.ID, "")
			e.mustDo(404, "DELETE", "/aliases/"+a.ID, "")

			e.mustDo(200, "PUT", "/quotas/q1", `{"limit":2,"windowSec":60,"key":"ip"}`)
			Expect(e.mustDo(200, http.MethodGet, "/quotas/usage", "")).To(Equal("{}"), "usage")
			e.mustDo(204, "POST", "/quotas/q1/reset", "")
			e.mustDo(404, "POST", "/quotas/nope/reset", "")
			e.mustDo(204, "DELETE", "/quotas/q1", "")
		})

		It("serves the platform routes and exports and imports the platform rules", func() {
			e := newAPI()
			b := e.mustDo(201, "POST", "/platforms", `{"host":"upstream","repo":"acme/plat/*","platforms":["linux/amd64"]}`)
			var p api.PlatformRule
			Expect(json.Unmarshal([]byte(b), &p)).To(Succeed(), "POST /platforms = %s", b)
			Expect(p.ID).NotTo(BeEmpty(), "POST /platforms = %s", b)
			code, b := e.do("POST", "/platforms", `{"host":"upstream","repo":"x","platforms":[]}`)
			Expect(code).To(Equal(400), "empty platforms = %s", b)
			exp := e.mustDo(200, http.MethodGet, "/export", "")
			Expect(exp).To(ContainSubstring(platAMD64), "export lacks the platform rule")
			e.mustDo(204, "DELETE", "/platforms/"+p.ID, "")
			Expect(e.mustDo(200, http.MethodGet, "/platforms", "")).To(Equal("[]"), "GET /platforms after delete")
			e.mustDo(204, "POST", "/import", exp)
			Expect(e.mustDo(200, http.MethodGet, "/platforms", "")).To(ContainSubstring("acme/plat/*"), "import did not restore the platform rule")
		})

		It("serves the sets routes", func() {
			e := newAPI()
			e.mustDo(204, "POST", "/import?set=kt-base", "users:\n- {name: fallback, password: pw}\n")
			e.mustDo(204, "POST", "/import?set=kt-add", setFixture)
			Expect(e.mustDo(200, http.MethodGet, "/sets", "")).To(Equal(`[{"name":"kt-add","objects":3},{"name":"kt-base","objects":1}]`), "GET /sets")
			exp := e.mustDo(200, http.MethodGet, "/export?set=kt-add", "")
			Expect(exp).To(ContainSubstring("t-extra"), "export of a set")
			Expect(exp).NotTo(ContainSubstring("fallback"), "export of a set")
			code, b := e.do("POST", "/import?set=bad%20name", setFixture)
			Expect(code).To(Equal(400), "bad set name = %s", b)
			e.mustDo(204, "DELETE", "/sets/kt-add", "")
			e.mustDo(404, "DELETE", "/sets/kt-add", "")
			Expect(e.mustDo(200, http.MethodGet, "/users", "")).NotTo(ContainSubstring("extra"), "users after the set deletion")
			// an object created through its own route keeps the set of its body
			e.mustDo(201, "POST", "/rules", `{"set":"kt-other","target":{"host":"mirror"},"effects":{"status":503}}`)
			Expect(e.mustDo(200, http.MethodGet, "/sets", "")).To(ContainSubstring(`{"name":"kt-other","objects":1}`), "GET /sets")
			// a plain import still replaces the whole state
			e.mustDo(204, "POST", "/import", "users:\n- {name: only, password: pw}\n")
			Expect(e.mustDo(200, http.MethodGet, "/sets", "")).To(Equal("[]"), "sets after a replace import")
		})
	})

	Describe("state export and import", func() {
		It("round-trips a state through export and import", func() {
			e := newAPI()
			e.mustDo(201, "POST", "/rules", `{"enabled":true,"target":{"host":"upstream"},"effects":{"status":503}}`)
			code, yml := e.do(http.MethodGet, "/export", "")
			Expect(code).To(Equal(200), "export = %q", yml)
			Expect(yml).NotTo(BeEmpty(), "export")
			req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/export", nil)
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.Header.Get("Content-Type")).To(Equal("application/yaml"), "content-type")
			Expect(resp.Header.Get("Content-Disposition")).To(Equal("attachment; filename=kuik-tester-state.yaml"), "content-disposition")
			e2 := newAPI()
			e2.mustDo(204, "POST", "/import", yml)
			a, b := e.mustDo(200, http.MethodGet, "/rules", ""), e2.mustDo(200, http.MethodGet, "/rules", "")
			Expect(b).To(Equal(a), "rules differ")
			code, b = e2.do("POST", "/import", "rules: [not, valid")
			Expect(code).To(Equal(400), "invalid yaml = %s", b)
		})

		It("answers 413 to an import over the body limit", func() {
			e := newAPI()
			code, b := e.do("POST", "/import", strings.Repeat("a", maxBody+1))
			Expect(code).To(Equal(413), "POST /import (over %d bytes) = %s", maxBody, b)
		})
	})

	DescribeTable("refuses a body key the route does not know",
		func(path, body string) {
			e := newAPI()
			code, b := e.do(http.MethodPost, path, body)
			Expect(code).To(Equal(http.StatusBadRequest), "POST %s %s = %s", path, body, b)
			Expect(b).To(ContainSubstring(`"error"`))
		},
		Entry("resolve", "/resolve", `{"reference":"upstream/acme/foo:t1"}`),
		Entry("drift", "/drift", `{"repository":"acme/foo"}`),
		Entry("rule move", "/rules/r1/move", `{"position":1}`),
		Entry("rule enabled", "/rules/r1/enabled", `{"on":true}`),
		Entry("image action", "/images/action", `{"image":"upstream/acme/foo:t1","action":"503"}`),
		Entry("delete-tag", "/destinations/mirror/delete-tag", `{"repository":"a","tag":"t"}`),
		Entry("delete-manifest", "/destinations/mirror/delete-manifest", `{"repo":"a","manifest":"sha256:x"}`),
		Entry("tag", "/destinations/mirror/tag", `{"repo":"a","source":"t1","newTag":"t2"}`),
		Entry("push", "/destinations/mirror/push", `{"repo":"a","tag":"t","gen":2}`),
		Entry("purge", "/destinations/mirror/purge", `{"path":"a"}`),
	)

	Describe("image routes", func() {
		It("resolves a reference and drives the image actions and the drift", func() {
			e := newAPI()
			var r api.Resolved
			body := e.mustDo(200, "POST", "/resolve", `{"ref":"upstream/acme/foo:t1"}`)
			Expect(json.Unmarshal([]byte(body), &r)).To(Succeed())
			Expect(r.Pinned).To(HavePrefix("upstream.kt.test/acme/foo:t1@sha256:"), "pinned")
			e.mustDo(400, "POST", "/resolve", `{"ref":"mirror/x:t"}`)
			e.mustDo(400, "POST", "/resolve", `{"ref":"nohost"}`)
			e.mustDo(400, "POST", "/resolve", `{}`)
			e.mustDo(405, http.MethodGet, "/resolve?ref=upstream/acme/foo:t1", "")
			code, _ := e.do("POST", "/resolve", `{"ref":"upstream/acme/foo:t1"}`, "Origin", "http://evil.example")
			Expect(code).To(Equal(403), "cross-origin resolve")
			Expect(e.mustDo(200, http.MethodGet, "/images", "")).To(ContainSubstring(`"repo":"acme/foo"`), "images")
			Expect(e.mustDo(200, "POST", "/images/action", `{"host":"upstream","repo":"acme/foo","tag":"t1","action":"503"}`)).To(ContainSubstring(`"behaviour":"503"`), "action")
			e.mustDo(400, "POST", "/images/action", `{"host":"upstream","repo":"acme/foo","tag":"t1","action":"boom"}`)
			Expect(e.mustDo(200, "POST", "/drift", `{"host":"upstream","repo":"acme/foo","tag":"t1"}`)).To(ContainSubstring(`"changed":[`), "drift")
		})

		It("answers a drift with the changed references", func() {
			e := newAPI()
			e.mustDo(http.StatusOK, http.MethodPost, "/resolve", `{"ref":"upstream/acme/foo:t1"}`)
			Expect(e.mustDo(http.StatusOK, http.MethodPost, "/drift", `{"host":"upstream","repo":"acme/foo","tag":"t1"}`)).To(Equal(`{"changed":["upstream/acme/foo:t1"]}`))
			Expect(e.mustDo(http.StatusOK, http.MethodPost, "/drift", `{"host":"upstream","repo":"acme/none"}`)).To(Equal(`{"changed":[]}`))
		})
	})

	Describe("destination routes", func() {
		It("pushes, tags, deletes and reports errors", func() {
			e := newAPI()
			e.mustDo(204, "POST", "/destinations/mirror/push", `{"repo":"acme/foo","tag":"t1"}`)
			Expect(e.mustDo(200, http.MethodGet, "/destinations/mirror", "")).To(ContainSubstring(`"tag":"t1"`), "inventory")
			e.mustDo(204, "POST", "/destinations/mirror/tag", `{"repo":"acme/foo","src":"t1","newTag":"t2"}`)
			e.mustDo(204, "POST", "/destinations/mirror/delete-tag", `{"repo":"acme/foo","tag":"t1"}`)
			e.mustDo(404, http.MethodGet, "/destinations/nope", "")
			e.mustDo(404, "POST", "/destinations/nope/push", `{"repo":"a","tag":"t"}`)
			e.mustDo(400, "POST", "/destinations/mirror/push", `{"repo":"Bad Repo","tag":"t"}`)

			dead := httptest.NewServer(http.NotFoundHandler())
			url := dead.URL
			dead.Close()
			e2 := newAPIWith(&testerconfig.Config{BaseDomain: baseDomain, Proxies: []testerconfig.ProxyHost{{Name: deadHost, Backend: url}}})
			e2.mustDo(502, http.MethodGet, "/destinations/dead", "")
			e2.mustDo(502, "POST", "/destinations/dead/push", `{"repo":"a","tag":"t"}`)
		})

		It("pushes the content key and the generation the body names", func() {
			e := newAPI()
			const push = "/destinations/mirror/push"
			e.mustDo(http.StatusNoContent, http.MethodPost, push, `{"repo":"acme/a","tag":"g1"}`)
			e.mustDo(http.StatusNoContent, http.MethodPost, push, `{"repo":"acme/a","tag":"g2","generation":2}`)
			e.mustDo(http.StatusNoContent, http.MethodPost, push, `{"repo":"acme/a","tag":"k","contentKey":"acme/b"}`)
			e.mustDo(http.StatusNoContent, http.MethodPost, push, `{"repo":"acme/b","tag":"b"}`)
			repos := getJSON[[]api.Repository](e, http.StatusOK, http.MethodGet, "/destinations/mirror", "")
			digest := func(repo, tag string) string {
				GinkgoHelper()
				for _, r := range repos {
					if r.Name != repo {
						continue
					}
					for _, t := range r.Tags {
						if t.Tag == tag {
							return t.Digest
						}
					}
				}
				Fail("no " + repo + ":" + tag + " in the inventory")
				return ""
			}
			Expect(digest("acme/a", "g1")).NotTo(Equal(digest("acme/a", "g2")), "generation 1 and 2")
			Expect(digest("acme/a", "k")).To(Equal(digest("acme/b", "b")), "content key")
		})

		It("deletes a manifest by its digest", func() {
			e := newAPI()
			e.mustDo(http.StatusNoContent, http.MethodPost, "/destinations/mirror/push", `{"repo":"acme/foo","tag":"t1"}`)
			repos := getJSON[[]api.Repository](e, http.StatusOK, http.MethodGet, "/destinations/mirror", "")
			Expect(repos).To(HaveLen(1))
			Expect(repos[0].Tags).To(HaveLen(1))
			d := repos[0].Tags[0].Digest
			e.mustDo(http.StatusNoContent, http.MethodPost, "/destinations/mirror/delete-manifest", `{"repo":"acme/foo","digest":"`+d+`"}`)
			repos = getJSON[[]api.Repository](e, http.StatusOK, http.MethodGet, "/destinations/mirror", "")
			for _, r := range repos {
				Expect(r.Tags).To(BeEmpty(), r.Name)
			}
		})

		DescribeTable("maps the destination errors to statuses",
			func(dead bool, method, path, body string, want int) {
				e := newAPI()
				if dead {
					backend := httptest.NewServer(http.NotFoundHandler())
					url := backend.URL
					backend.Close()
					e = newAPIWith(&testerconfig.Config{BaseDomain: baseDomain, Proxies: []testerconfig.ProxyHost{{Name: deadHost, Backend: url}}})
				}
				e.mustDo(want, method, path, body)
			},
			Entry("a bad repo on delete-tag is a 400", false, "POST", "/destinations/mirror/delete-tag", `{"repo":"Bad//repo","tag":"t"}`, 400),
			Entry("a bad digest on delete-manifest is a 400", false, "POST", "/destinations/mirror/delete-manifest", `{"repo":"acme/foo","digest":"sha256:zz"}`, 400),
			Entry("a bad new tag is a 400", false, "POST", "/destinations/mirror/tag", `{"repo":"acme/foo","src":"t1","newTag":"bad tag"}`, 400),
			Entry("the inventory of a dead backend is a 502", true, http.MethodGet, "/destinations/dead", "", 502),
			Entry("delete-manifest on a dead backend is a 502", true, "POST", "/destinations/dead/delete-manifest", `{"repo":"acme/foo","digest":"sha256:`+strings.Repeat("a", 64)+`"}`, 502),
			Entry("a purge on a dead backend is a 502", true, "POST", "/destinations/dead/purge", `{"prefix":"acme"}`, 502),
			Entry("a push to an unknown host is a 404", false, "POST", "/destinations/nope/push", `{"repo":"a","tag":"t"}`, 404),
		)

		It("purges the tags under a prefix", func() {
			e := newAPI()
			e.mustDo(204, "POST", "/destinations/mirror/push", `{"repo":"purge/acme/foo","tag":"t1"}`)
			e.mustDo(204, "POST", "/destinations/mirror/push", `{"repo":"other/foo","tag":"t1"}`)
			code, b := e.do("POST", "/destinations/mirror/purge", `{"prefix":"a/*"}`)
			Expect(code).To(Equal(400), "glob prefix = %s", b)
			code, b = e.do("POST", "/destinations/mirror/purge", `{"prefix":""}`)
			Expect(code).To(Equal(400), "empty prefix = %s", b)
			e.mustDo(404, "POST", "/destinations/nope/purge", `{"prefix":"a"}`)
			Expect(e.mustDo(200, "POST", "/destinations/mirror/purge", `{"prefix":"purge"}`)).To(Equal(`{"deletedTags":1,"repositories":["purge/acme/foo"]}`), "purge")
			// the repository may stay listed, empty: a purge removes tags, never repositories
			repos := getJSON[[]api.Repository](e, 200, http.MethodGet, "/destinations/mirror", "")
			Expect(repos).To(ContainElement(SatisfyAll(
				HaveField("Name", "other/foo"),
				HaveField("Tags", ConsistOf(SatisfyAll(HaveField("Tag", "t1"), HaveField("Digest", Not(BeEmpty()))))),
			)), "inventory after purge")
			Expect(repos).NotTo(ContainElement(SatisfyAll(
				HaveField("Name", "purge/acme/foo"), HaveField("Tags", Not(BeEmpty())),
			)), "inventory after purge")
		})
	})
})
