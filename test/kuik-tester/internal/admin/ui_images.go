package admin

import (
	"net/http"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

type imgGroup struct {
	Host string
	Rows []api.ImageRow
}

// imagesData is the images page: the filter, the rows kept and how many
// matched before the cap.
type imagesData struct {
	Host, Repo   string
	Groups       []imgGroup
	Shown, Total int
	// Purgeable is how many images a purge forgets: the seen ones and the
	// ones with a generation.
	Purgeable int
}

func (u *ui) imagesPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := imagesData{Host: strings.TrimSpace(q.Get("host")), Repo: strings.TrimSpace(q.Get("repo"))}
	rows, total := u.svc.Images(ImageFilter{Host: d.Host, Repo: d.Repo, Limit: u.maxImages})
	d.Shown, d.Total = len(rows), total
	d.Purgeable = u.svc.PurgeableImages()
	for _, row := range rows {
		if n := len(d.Groups); n == 0 || d.Groups[n-1].Host != row.Host {
			d.Groups = append(d.Groups, imgGroup{Host: row.Host})
		}
		g := &d.Groups[len(d.Groups)-1]
		g.Rows = append(g.Rows, row)
	}
	u.page(w, "images", "Images", d)
}

// imagesPurge forgets the images and reloads the page: every group changes.
func (u *ui) imagesPurge(w http.ResponseWriter, r *http.Request) {
	if _, err := u.svc.PurgeImages(); err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

func (u *ui) imageAction(w http.ResponseWriter, r *http.Request) {
	row, err := u.svc.ImageAction(val(r, "host"), val(r, "repo"), val(r, "tag"), val(r, "action"))
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "images", "imgrow", row)
}

func (u *ui) imageResolve(w http.ResponseWriter, r *http.Request) {
	res, err := u.svc.Resolve(val(r, "host"), val(r, "repo"), val(r, "tag"))
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "images", "resolved", res)
}

func (u *ui) imageDrift(w http.ResponseWriter, r *http.Request) {
	changed, err := u.svc.Drift(api.Target{Host: val(r, "host"), Repo: val(r, "repo"), Tag: val(r, "tag")})
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "images", "drifted", changed)
}
