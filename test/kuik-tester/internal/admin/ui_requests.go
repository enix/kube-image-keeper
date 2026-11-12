package admin

import (
	"net/http"
	"slices"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// maxLive is the number of rows the live request table keeps, on the page
// and in the browser.
const maxLive = 500

type requestsData struct {
	Host, Repo, Kind, Caller, Status, Since string
	Err                                     string
	Entries                                 []api.LogEntry
	Stream                                  string
	Stats                                   []api.Stat
	MaxRows                                 int
	// Logged is how many requests the log holds, whatever the filter.
	Logged int
}

func (u *ui) requestsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := requestsData{Host: q.Get("host"), Repo: q.Get("repo"), Kind: q.Get("kind"), Caller: q.Get("caller"), Status: q.Get("status"), Since: q.Get("since"), Stats: u.log.Stats(), MaxRows: maxLive, Logged: len(u.log.Query(reqlog.Filter{}))}
	if f, used, err := parseFilter(q); err != nil {
		d.Err = err.Error()
	} else {
		es := u.log.Query(f)
		slices.Reverse(es)
		d.Entries = es[:min(len(es), maxLive)]
		d.Stream = "/api/v1/requests/stream?" + used.Encode()
	}
	u.page(w, "requests", "Requests", d)
}

func (u *ui) statsReset(w http.ResponseWriter, r *http.Request) {
	u.log.ResetStats()
	u.render(w, "requests", "stats", u.log.Stats())
}

// logPurge empties the log; the answer is the empty body of the live table.
func (u *ui) logPurge(w http.ResponseWriter, r *http.Request) {
	u.log.Clear()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
}
