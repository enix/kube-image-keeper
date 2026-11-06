package admin

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

type delArg struct{ Kind, Key, What string }

// purgeArg is the Purge all button of a settings section.
type purgeArg struct {
	Kind      string
	Count     int
	One, Many string
}

type quotaView struct {
	api.QuotaDef
	Usage []string
}

type settingsData struct {
	Users      []api.User
	Grants     []api.Grant
	HostAuth   []api.HostAuth
	Aliases    []api.Alias
	Divergents []api.Divergent
	Platforms  []api.PlatformRule
	Quotas     []quotaView
}

func (u *ui) settingsData() settingsData {
	d := settingsData{
		Users: u.svc.ListUsers(), Grants: u.svc.ListGrants(), HostAuth: u.svc.ListHostAuth(),
		Aliases: u.svc.ListAliases(), Divergents: u.svc.ListDivergents(), Platforms: u.svc.ListPlatforms(),
	}
	usage := u.svc.QuotaUsage()
	for _, q := range u.svc.ListQuotas() {
		v := quotaView{QuotaDef: q}
		for k, n := range usage[q.Name] {
			label := k
			if label == "" {
				label = "(global)"
			}
			v.Usage = append(v.Usage, fmt.Sprintf("%s: %d/%d", label, n, q.Limit))
		}
		slices.Sort(v.Usage)
		d.Quotas = append(d.Quotas, v)
	}
	return d
}

func (u *ui) settingsPage(w http.ResponseWriter, r *http.Request) {
	u.page(w, "settings", "Settings", u.settingsData())
}

// uiSettingsOps maps "<kind>/<op>" to the service call it makes.
var uiSettingsOps = map[string]func(svc *Service, r *http.Request) error{
	"users/put": func(svc *Service, r *http.Request) error {
		return svc.PutUser(api.User{Name: val(r, "name"), Password: r.PostForm.Get("password"), Set: val(r, "set")})
	},
	"users/delete": func(svc *Service, r *http.Request) error { return svc.DeleteUser(val(r, "key")) },
	"hostauth/put": func(svc *Service, r *http.Request) error {
		return svc.PutHostAuth(api.HostAuth{Host: val(r, "host"), Scheme: api.Scheme(val(r, "scheme")), Set: val(r, "set")})
	},
	"hostauth/delete": func(svc *Service, r *http.Request) error { return svc.DeleteHostAuth(val(r, "key")) },
	"aliases/create": func(svc *Service, r *http.Request) error {
		_, err := svc.CreateAlias(api.Alias{Host: val(r, "host"), From: val(r, "from"), To: val(r, "to"), Set: val(r, "set")})
		return err
	},
	"aliases/delete": func(svc *Service, r *http.Request) error { return svc.DeleteAlias(val(r, "key")) },
	"divergents/create": func(svc *Service, r *http.Request) error {
		_, err := svc.CreateDivergent(api.Divergent{Host: val(r, "host"), Repo: val(r, "repo"), Salt: val(r, "salt"), Set: val(r, "set")})
		return err
	},
	"divergents/delete": func(svc *Service, r *http.Request) error { return svc.DeleteDivergent(val(r, "key")) },
	"platforms/create": func(svc *Service, r *http.Request) error {
		_, err := svc.CreatePlatform(api.PlatformRule{Host: val(r, "host"), Repo: val(r, "repo"), Platforms: r.PostForm["platform"], Set: val(r, "set")})
		return err
	},
	"platforms/delete": func(svc *Service, r *http.Request) error { return svc.DeletePlatform(val(r, "key")) },
	"grants/create": func(svc *Service, r *http.Request) error {
		g := api.Grant{Host: val(r, "host"), Repo: val(r, "repo"), Users: formList(r, "users"), Set: val(r, "set")}
		for _, a := range r.PostForm["action"] {
			g.Actions = append(g.Actions, api.Action(a))
		}
		_, err := svc.CreateGrant(g)
		return err
	},
	"grants/delete": func(svc *Service, r *http.Request) error { return svc.DeleteGrant(val(r, "key")) },
	"quotas/put": func(svc *Service, r *http.Request) error {
		q := api.QuotaDef{Name: val(r, "name"), Key: api.QuotaKey(val(r, "key")), Methods: formList(r, "methods"), Set: val(r, "set")}
		for _, k := range r.PostForm["kind"] {
			q.Kinds = append(q.Kinds, api.Kind(k))
		}
		var err error
		if q.Limit, err = intVal(r, "limit"); err != nil {
			return err
		}
		if q.WindowSec, err = intVal(r, "windowSec"); err != nil {
			return err
		}
		return svc.PutQuota(q)
	},
	"quotas/delete": func(svc *Service, r *http.Request) error { return svc.DeleteQuota(val(r, "key")) },
	"quotas/reset":  func(svc *Service, r *http.Request) error { return svc.ResetQuota(val(r, "key")) },
}

func init() {
	// Every list but the rules (their own table) has a "purge" operation.
	for _, kind := range purgeKinds {
		if kind != kindRules {
			uiSettingsOps[kind+"/purge"] = func(svc *Service, r *http.Request) error {
				return errOnly(svc.PurgeColl(kind))
			}
		}
	}
}

func (u *ui) settingsPost(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("kind") + "/" + r.PathValue("op")
	do, ok := uiSettingsOps[op]
	if !ok {
		u.fail(w, invalid("unknown operation %q", op))
		return
	}
	if err := do(u.svc, r); err != nil {
		u.fail(w, err)
		return
	}
	// Only the section of the kind is swapped, so the other forms keep their drafts.
	u.render(w, "settings", "set-"+r.PathValue("kind"), u.settingsData())
}
