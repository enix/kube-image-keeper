package admin

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

//go:embed ui
var uiFS embed.FS

var uiPages = []string{"images", "rules", "settings", "destinations", "requests", "state"}

type ui struct {
	svc *Service
	log *reqlog.Log
	tpl map[string]*template.Template

	maxImages int
}

// uiMaxImageRows bounds the rows of the images page.
const uiMaxImageRows = 500

// NewUI returns the server-rendered admin UI. The templates are parsed once
// from the embedded files; a parse error panics.
func NewUI(svc *Service, log *reqlog.Log) http.Handler {
	return newUI(svc, log, uiMaxImageRows)
}

func newUI(svc *Service, log *reqlog.Log, maxImages int) http.Handler {
	u := &ui{svc: svc, log: log, tpl: map[string]*template.Template{}, maxImages: maxImages}
	funcs := template.FuncMap{
		"shortDigest": shortDigest,
		"actions":     func() []imageAction { return imageActions },
		"del":         func(kind, key, what string) delArg { return delArg{kind, key, what} },
		"purge":       func(kind string, n int, one, many string) purgeArg { return purgeArg{kind, n, one, many} },
		"count":       count,
		"iso":         func(e api.LogEntry) string { return e.Time.UTC().Format(time.RFC3339Nano) },
		"hasKind":     func(list []api.Kind, k api.Kind) bool { return slices.Contains(list, k) },
		"kinds":       api.AllKinds,
		"platforms": func() []string {
			names := make([]string, len(synth.Platforms))
			for i, p := range synth.Platforms {
				names[i] = synth.PlatformName(p)
			}
			return names
		},
		"targetStr": targetStr,
		"join":      strings.Join,
	}
	for _, p := range uiPages {
		u.tpl[p] = template.Must(template.New(p).Funcs(funcs).ParseFS(uiFS, "ui/templates/layout.html", "ui/templates/"+p+".html"))
	}
	static, err := fs.Sub(uiFS, "ui/static")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	// post registers a form POST: the form is parsed, and bounded, first.
	post := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc("POST "+pattern, func(w http.ResponseWriter, r *http.Request) {
			if u.form(w, r) {
				h(w, r)
			}
		})
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", u.imagesPage)
	mux.HandleFunc("GET /rules", u.rulesPage)
	mux.HandleFunc("GET /settings", u.settingsPage)
	mux.HandleFunc("GET /destinations", u.destinationsPage)
	mux.HandleFunc("GET /requests", u.requestsPage)
	mux.HandleFunc("GET /state", u.statePage)

	post("/ui/images/action", u.imageAction)
	post("/ui/images/resolve", u.imageResolve)
	post("/ui/images/drift", u.imageDrift)
	post("/ui/images/purge", u.imagesPurge)
	post("/ui/rules", u.ruleCreate)
	mux.HandleFunc("GET /ui/rules/form", u.ruleNewForm)
	mux.HandleFunc("GET /ui/rules/{id}/edit", u.ruleEditForm)
	post("/ui/rules/{id}", u.ruleUpdate)
	post("/ui/rules/move", u.ruleMove)
	post("/ui/rules/toggle", u.ruleToggle)
	post("/ui/rules/delete", u.ruleDelete)
	post("/ui/rules/purge", u.rulePurge)
	mux.HandleFunc("GET /ui/settings/quota-usage", func(w http.ResponseWriter, r *http.Request) {
		u.render(w, "settings", "set-quotas", u.settingsData())
	})
	post("/ui/settings/{kind}/{op}", u.settingsPost)
	post("/ui/destinations/{host}/{op}", u.destinationsPost)
	mux.HandleFunc("POST /ui/stats/reset", u.statsReset)
	post("/ui/log/purge", u.logPurge)
	post("/ui/state/import", u.stateImport)
	post("/ui/state/purge", u.statePurge)
	post("/ui/sets/import", u.setImport)
	post("/ui/sets/delete", u.setDelete)
	return mux
}

// count writes "1 rule" or "42 rules".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

func targetStr(t api.Target) string {
	var parts []string
	if t.Host != "" || t.Repo != "" || t.Tag != "" {
		parts = append(parts, orStar(t.Host)+"/"+orStar(t.Repo)+":"+orStar(t.Tag))
	}
	if len(t.Kinds) > 0 {
		ks := make([]string, len(t.Kinds))
		for i, k := range t.Kinds {
			ks[i] = string(k)
		}
		parts = append(parts, "kinds="+strings.Join(ks, ","))
	}
	if len(t.Methods) > 0 {
		parts = append(parts, "methods="+strings.Join(t.Methods, ","))
	}
	if t.Caller != "" {
		parts = append(parts, "caller="+t.Caller)
	}
	if len(parts) == 0 {
		return "everything"
	}
	return strings.Join(parts, " ")
}

func orStar(s string) string {
	if s == "" {
		return "*"
	}
	return s
}

// render executes a named template of a page into the response.
func (u *ui) render(w http.ResponseWriter, page, name string, data any) {
	var buf bytes.Buffer
	if err := u.tpl[page].ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (u *ui) page(w http.ResponseWriter, name, title string, data any) {
	u.render(w, name, "layout", struct {
		Title, Nav string
		Data       any
	}{title, name, data})
}

// fail renders an error as an inline fragment with status 200, swapped into
// the #msg area so the element that triggered it stays as it was.
func (u *ui) fail(w http.ResponseWriter, err error) {
	w.Header().Set("HX-Retarget", "#msg")
	w.Header().Set("HX-Reswap", "innerHTML")
	u.render(w, "state", "err", err.Error())
}

// form parses a posted form, refusing bodies over maxBody.
func (u *ui) form(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		u.fail(w, invalid("unreadable form: %v", err))
		return false
	}
	return true
}

func val(r *http.Request, name string) string { return strings.TrimSpace(r.PostForm.Get(name)) }

func intVal(r *http.Request, name string) (int, error) {
	s := val(r, name)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, invalid("%s %q is not a number", name, s)
	}
	return n, nil
}

// formList splits a comma or space separated field; nil when it is empty.
func formList(r *http.Request, name string) []string {
	l := strings.FieldsFunc(r.PostForm.Get(name), func(c rune) bool { return c == ',' || c == ' ' || c == '\n' || c == '\r' || c == '\t' })
	if len(l) == 0 {
		return nil
	}
	return l
}
