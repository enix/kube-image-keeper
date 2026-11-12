package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

type stateData struct {
	Sets []api.SetInfo
	OOB  bool
	Msg  string
	// Warns are shown under Msg, one per line.
	Warns []string
}

// purgeWord is what the user types to confirm the global purge.
const purgeWord = "purge"

// purged answers a global purge: the summary goes to #msg, a warning per
// failed backend under it, and the set list and the word field, emptied, are
// refreshed out of band.
func (u *ui) purged(w http.ResponseWriter, res api.PurgeAllResult) {
	d := stateData{Sets: u.svc.Sets(), OOB: true, Msg: purgeSummary(res)}
	for _, b := range res.Backends {
		if b.Error != "" {
			d.Warns = append(d.Warns, fmt.Sprintf("Backend %s: %s", b.Backend, b.Error))
		}
	}
	w.Header().Set("HX-Retarget", "#msg")
	w.Header().Set("HX-Reswap", "innerHTML")
	u.render(w, "state", "purged", d)
}

// purgeSummary says what a global purge removed.
func purgeSummary(res api.PurgeAllResult) string {
	msg := fmt.Sprintf("Purged %d rules, %d aliases, %d divergents, %d grants, %d platforms, %d users, %d host auth entries, %d quotas, %d images and %d requests.",
		res.Rules, res.Aliases, res.Divergents, res.Grants, res.Platforms, res.Users, res.HostAuth, res.Quotas, res.Images, res.Requests)
	if len(res.Backends) > 0 {
		tags := 0
		for _, b := range res.Backends {
			tags += b.DeletedTags
		}
		msg += fmt.Sprintf(" Deleted %d tags on %d backends.", tags, len(res.Backends))
	}
	return msg
}

// statePurge purges everything once the word is typed; a wrong word changes
// nothing.
func (u *ui) statePurge(w http.ResponseWriter, r *http.Request) {
	if val(r, "confirm") != purgeWord {
		u.fail(w, invalid("type %q to confirm: nothing was purged", purgeWord))
		return
	}
	res, err := purgeAll(r.Context(), u.svc, u.log, r.PostForm.Get("backends") != "")
	if err != nil {
		u.fail(w, err)
		return
	}
	u.purged(w, res)
}

func (u *ui) statePage(w http.ResponseWriter, r *http.Request) {
	u.page(w, "state", "State", stateData{Sets: u.svc.Sets()})
}

// imported answers a successful import: the message goes to #msg and the set
// list is refreshed out of band.
func (u *ui) imported(w http.ResponseWriter, msg string) {
	w.Header().Set("HX-Retarget", "#msg")
	w.Header().Set("HX-Reswap", "innerHTML")
	u.render(w, "state", "imported", stateData{Sets: u.svc.Sets(), OOB: true, Msg: msg})
}

func (u *ui) stateImport(w http.ResponseWriter, r *http.Request) {
	doc := r.PostForm.Get("yaml")
	if strings.TrimSpace(doc) == "" {
		u.fail(w, invalid("the YAML document is empty: nothing to replace the settings with"))
		return
	}
	if err := u.svc.Import([]byte(doc)); err != nil {
		u.fail(w, err)
		return
	}
	u.imported(w, "Imported.")
}

func (u *ui) setImport(w http.ResponseWriter, r *http.Request) {
	if err := u.svc.ImportSet(val(r, "set"), []byte(r.PostForm.Get("yaml"))); err != nil {
		u.fail(w, err)
		return
	}
	u.imported(w, "Merged into set "+val(r, "set")+".")
}

func (u *ui) setDelete(w http.ResponseWriter, r *http.Request) {
	if err := u.svc.DeleteSet(val(r, "name")); err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "state", "sets", stateData{Sets: u.svc.Sets()})
}
