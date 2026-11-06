package admin

import (
	"net/http"
	"slices"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

type ruleView struct {
	api.Rule
	Index     int
	Last      bool
	Behaviour string
	Warnings  []string
}

type rulesData struct {
	Rules []ruleView
	Form  ruleFormData
}

// ruleFormData is the create or edit form of a rule.
type ruleFormData struct {
	Edit    bool
	OOB     bool // render as an out-of-band swap of #rule-form
	Rule    api.Rule
	Methods string
	Quotas  []api.QuotaDef
}

func (u *ui) ruleForm(r api.Rule, edit, oob bool) ruleFormData {
	return ruleFormData{Edit: edit, OOB: oob, Rule: r, Methods: strings.Join(r.Target.Methods, ", "), Quotas: u.svc.ListQuotas()}
}

func (u *ui) newRuleForm(oob bool) ruleFormData {
	return u.ruleForm(api.Rule{Enabled: true}, false, oob)
}

// rulesData lists the rules and the create form, which is an out-of-band swap
// when oob.
func (u *ui) rulesData(oob bool) rulesData {
	rs := u.svc.ListRules()
	d := rulesData{Form: u.newRuleForm(oob)}
	for i, r := range rs {
		d.Rules = append(d.Rules, ruleView{Rule: r, Index: i, Last: i == len(rs)-1, Behaviour: behaviour(&r), Warnings: RuleWarnings(r)})
	}
	return d
}

func (u *ui) rulesPage(w http.ResponseWriter, r *http.Request) {
	u.page(w, "rules", "Rules", u.rulesData(false))
}

// rulesFragment renders only the rule table: the create and edit forms are
// not swapped, so a half-typed form survives a table action.
func (u *ui) rulesFragment(w http.ResponseWriter) {
	u.render(w, "rules", "ruleslist", u.rulesData(false))
}

func (u *ui) ruleNewForm(w http.ResponseWriter, r *http.Request) {
	u.render(w, "rules", "ruleform", u.newRuleForm(false))
}

func (u *ui) ruleEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rs := u.svc.ListRules()
	i := slices.IndexFunc(rs, func(x api.Rule) bool { return x.ID == id })
	if i < 0 {
		u.fail(w, ErrNotFound)
		return
	}
	u.render(w, "rules", "ruleform", u.ruleForm(rs[i], true, false))
}

func (u *ui) ruleUpdate(w http.ResponseWriter, r *http.Request) {
	rule, err := ruleFromForm(r)
	rule.ID = r.PathValue("id")
	if err == nil {
		_, err = u.svc.UpdateRule(rule)
	}
	if err != nil {
		u.fail(w, err)
		return
	}
	// The list replaces the table; the edit form goes back to the create form.
	u.render(w, "rules", "ruleupdated", u.rulesData(true))
}

func ruleFromForm(r *http.Request) (api.Rule, error) {
	rule := api.Rule{Enabled: val(r, "enabled") != "", Note: val(r, "note"), Set: val(r, "set")}
	t := &rule.Target
	t.Host, t.Repo, t.Tag, t.Caller = val(r, "host"), val(r, "repo"), val(r, "tag"), val(r, "caller")
	for _, k := range r.PostForm["kind"] {
		t.Kinds = append(t.Kinds, api.Kind(k))
	}
	t.Methods = formList(r, "methods")
	e := &rule.Effects
	var err error
	if e.Status, err = intVal(r, "status"); err != nil {
		return rule, err
	}
	if e.LatencyMS, err = intVal(r, "latencyMs"); err != nil {
		return rule, err
	}
	e.Reset = val(r, "reset") != ""
	e.Pass = val(r, "pass") != ""
	e.Quota = val(r, "quota")
	for line := range strings.SplitSeq(r.PostForm.Get("headers"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, v, ok := strings.Cut(line, ":")
		if n = strings.TrimSpace(n); !ok || n == "" {
			return rule, invalid("header line %q must look like Name: value", line)
		}
		if e.Headers == nil {
			e.Headers = map[string]string{}
		}
		e.Headers[n] = strings.TrimSpace(v)
	}
	return rule, nil
}

func (u *ui) ruleCreate(w http.ResponseWriter, r *http.Request) {
	rule, err := ruleFromForm(r)
	if err == nil {
		_, err = u.svc.CreateRule(rule)
	}
	u.ruleOp(w, err)
}

func (u *ui) ruleMove(w http.ResponseWriter, r *http.Request) {
	id := val(r, "id")
	i := slices.IndexFunc(u.svc.ListRules(), func(x api.Rule) bool { return x.ID == id })
	if i < 0 {
		u.fail(w, ErrNotFound)
		return
	}
	to := i - 1
	if val(r, "dir") == "down" {
		to = i + 1
	}
	u.ruleOp(w, u.svc.MoveRule(id, to))
}

func (u *ui) ruleToggle(w http.ResponseWriter, r *http.Request) {
	u.ruleOp(w, u.svc.SetRuleEnabled(val(r, "id"), val(r, "enabled") == "true"))
}

func (u *ui) ruleDelete(w http.ResponseWriter, r *http.Request) {
	u.ruleOp(w, u.svc.DeleteRule(val(r, "id")))
}

func (u *ui) rulePurge(w http.ResponseWriter, r *http.Request) {
	u.ruleOp(w, errOnly(u.svc.PurgeColl(kindRules)))
}

// ruleOp answers a change of the rule table: the new table, or the error.
func (u *ui) ruleOp(w http.ResponseWriter, err error) {
	if err != nil {
		u.fail(w, err)
		return
	}
	u.rulesFragment(w)
}
