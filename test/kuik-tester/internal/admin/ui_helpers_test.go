package admin

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Wiring values of the UI answers and fields the specs post.
const (
	uiHdrRetarget        = "HX-Retarget"
	uiHdrReswap          = "HX-Reswap"
	uiMsgTarget          = "#msg"
	uiClassErr           = `class="err"`
	uiClassOK            = `class="ok"`
	uiClassWarn          = `class="warn"`
	uiNav                = "<nav"
	uiRulesList          = "rules-list"
	uiRuleFormID         = `id="rule-form"`
	uiFieldPass          = "password"
	uiFieldLimit         = "limit"
	uiFieldWindow        = "windowSec"
	uiFieldLatency       = "latencyMs"
	uiFieldMethods       = "methods"
	uiFieldHeaders       = "headers"
	uiFieldNote          = "note"
	uiFieldScheme        = "scheme"
	uiFieldYAML          = "yaml"
	uiPathRules          = "/ui/rules"
	uiPathSettings       = "/ui/settings/"
	uiPathImages         = "/ui/images/"
	uiPathDest           = "/ui/destinations/mirror/"
	uiQuotaName          = "q1"
	uiTagT1              = "t1"
	uiRef                = "upstream/acme/foo:t1"
	uiNumber42           = "42"
	uiNumber503          = "503"
	uiHeaderLines        = "X-A: b\nX-C: d"
	uiSchemeBasic        = "basic"
	uiActionPull         = "pull"
	uiKindPing           = "ping"
	uiKindBlob           = "blob"
	uiOpUsersPut         = "users/put"
	uiOpUsersDelete      = "users/delete"
	uiOpGrantsCreate     = "grants/create"
	uiOpQuotasPut        = "quotas/put"
	uiOpHostAuthPut      = "hostauth/put"
	uiOpAliasesCreate    = "aliases/create"
	uiOpDivergentsCreate = "divergents/create"
	uiOpPlatformsCreate  = "platforms/create"
	uiOpPlatformsDelete  = "platforms/delete"
	uiFieldPlatform      = "platform"
)

// uiEnv is a UI handler on a fresh service, with the objects the specs look at.
type uiEnv struct {
	h   http.Handler
	svc *Service
	log *reqlog.Log
	q   *quota.Counters
}

func newUIEnv() *uiEnv {
	GinkgoHelper()
	svc, _, q := newService(nil)
	l := reqlog.New(100)
	return &uiEnv{h: NewUI(svc, l), svc: svc, log: l, q: q}
}

func (e *uiEnv) get(path string) *httptest.ResponseRecorder {
	GinkgoHelper()
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func (e *uiEnv) post(path string, form url.Values) *httptest.ResponseRecorder {
	GinkgoHelper()
	var body io.Reader = strings.NewReader(form.Encode())
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// uiExpectPage asserts a whole page: 200 and the navigation.
func uiExpectPage(rec *httptest.ResponseRecorder) {
	GinkgoHelper()
	Expect(rec.Code).To(Equal(http.StatusOK), "body = %s", rec.Body)
	Expect(rec.Body.String()).To(ContainSubstring(uiNav))
}

// uiExpectFragment asserts a swap of the element id, with no page around it.
func uiExpectFragment(rec *httptest.ResponseRecorder, id string) {
	GinkgoHelper()
	Expect(rec.Code).To(Equal(http.StatusOK), "body = %s", rec.Body)
	Expect(rec.Header()).NotTo(HaveKey(uiHdrRetarget), "an answer retargeted: %s", rec.Body)
	Expect(rec.Body.String()).To(ContainSubstring(`id="`+id+`"`), "no element %s", id)
	Expect(rec.Body.String()).NotTo(ContainSubstring(uiNav))
}

// uiExpectError asserts an error answer, swapped into the message area.
func uiExpectError(rec *httptest.ResponseRecorder) {
	GinkgoHelper()
	Expect(rec.Code).To(Equal(http.StatusOK), "body = %s", rec.Body)
	Expect(rec.Header().Get(uiHdrRetarget)).To(Equal(uiMsgTarget))
	Expect(rec.Header().Get(uiHdrReswap)).To(Equal("innerHTML"))
	Expect(rec.Body.String()).To(ContainSubstring(uiClassErr))
}

// uiState is every collection of the service, for before/after comparisons.
type uiState struct {
	Rules      []api.Rule
	Users      []api.User
	Grants     []api.Grant
	HostAuth   []api.HostAuth
	Aliases    []api.Alias
	Divergents []api.Divergent
	Quotas     []api.QuotaDef
	Platforms  []api.PlatformRule
	Sets       []api.SetInfo
}

func uiSnapshot(svc *Service) uiState {
	return uiState{
		Rules: svc.ListRules(), Users: svc.ListUsers(), Grants: svc.ListGrants(), HostAuth: svc.ListHostAuth(),
		Aliases: svc.ListAliases(), Divergents: svc.ListDivergents(), Quotas: svc.ListQuotas(),
		Platforms: svc.ListPlatforms(), Sets: svc.Sets(),
	}
}

var (
	uiTagRe      = regexp.MustCompile(`<[a-zA-Z][^>]*>`)
	uiAttrRe     = regexp.MustCompile(`\s([a-zA-Z][\w:.-]*)(?:="([^"]*)")?`)
	uiInputRe    = regexp.MustCompile(`<input\b[^>]*>`)
	uiTextareaRe = regexp.MustCompile(`(?s)<textarea\b([^>]*)>(.*?)</textarea>`)
	uiSelectRe   = regexp.MustCompile(`(?s)<select\b([^>]*)>(.*?)</select>`)
	uiOptionRe   = regexp.MustCompile(`(?s)<option\b([^>]*)>(.*?)</option>`)
)

// uiAttrs returns the values of every attr="…" of body, in order.
func uiAttrs(body, attr string) []string {
	out := make([]string, 0, 4)
	for _, m := range regexp.MustCompile(`\s`+regexp.QuoteMeta(attr)+`="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

// uiTags returns every start tag of body that contains needle.
func uiTags(body, needle string) []string {
	var out []string
	for _, t := range uiTagRe.FindAllString(body, -1) {
		if strings.Contains(t, needle) {
			out = append(out, t)
		}
	}
	return out
}

// uiTagAttrs parses the attributes of one tag (or of an attribute string);
// a bare attribute such as checked has the value "".
func uiTagAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	for _, m := range uiAttrRe.FindAllStringSubmatch(tag, -1) {
		attrs[m[1]] = html.UnescapeString(m[2])
	}
	return attrs
}

// uiFormValues is what a browser would post from the fields of body.
func uiFormValues(body string) url.Values {
	v := url.Values{}
	for _, tag := range uiInputRe.FindAllString(body, -1) {
		a := uiTagAttrs(strings.TrimPrefix(tag, "<input"))
		name, ok := a["name"]
		if !ok {
			continue
		}
		if a["type"] == "checkbox" {
			if _, checked := a["checked"]; !checked {
				continue
			}
			if _, has := a["value"]; !has {
				v.Add(name, "on")
				continue
			}
		}
		v.Add(name, a["value"])
	}
	for _, m := range uiTextareaRe.FindAllStringSubmatch(body, -1) {
		if name, ok := uiTagAttrs(m[1])["name"]; ok {
			v.Add(name, html.UnescapeString(m[2]))
		}
	}
	for _, m := range uiSelectRe.FindAllStringSubmatch(body, -1) {
		name, ok := uiTagAttrs(m[1])["name"]
		if !ok {
			continue
		}
		opts := uiOptionRe.FindAllStringSubmatch(m[2], -1)
		chosen := -1
		for i, o := range opts {
			if _, sel := uiTagAttrs(o[1])["selected"]; sel {
				chosen = i
				break
			}
		}
		if chosen < 0 && len(opts) > 0 {
			chosen = 0
		}
		if chosen < 0 {
			continue
		}
		o := opts[chosen]
		if val, has := uiTagAttrs(o[1])["value"]; has {
			v.Add(name, val)
		} else {
			v.Add(name, html.UnescapeString(o[2]))
		}
	}
	return v
}
