package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

type destView struct {
	Host     string
	Repos    []api.Repository
	Tags     int // tags over every repository
	Err, Msg string
}

// inventoryTimeout bounds the listing of one backend, so a dead one does not
// stall the page.
const inventoryTimeout = 10 * time.Second

func (u *ui) destView(ctx context.Context, host string, opErr error, msg string) destView {
	ctx, cancel := context.WithTimeout(ctx, inventoryTimeout)
	defer cancel()
	v := destView{Host: host, Msg: msg}
	if opErr != nil {
		v.Err = opErr.Error()
	}
	repos, err := u.svc.Inventory(ctx, host)
	if err != nil && v.Err == "" {
		v.Err = "inventory: " + err.Error()
	}
	v.Repos = repos
	for _, r := range repos {
		v.Tags += len(r.Tags)
	}
	return v
}

func (u *ui) destinationsPage(w http.ResponseWriter, r *http.Request) {
	hosts := u.svc.Destinations()
	views := make([]destView, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Go(func() { views[i] = u.destView(r.Context(), h, nil, "") })
	}
	wg.Wait()
	u.page(w, "destinations", "Destinations", views)
}

func (u *ui) destinationsPost(w http.ResponseWriter, r *http.Request) {
	host, ctx := r.PathValue("host"), r.Context()
	var err error
	msg := "Done."
	switch op := r.PathValue("op"); op {
	case "delete-tag":
		err = u.svc.DestDeleteTag(ctx, host, val(r, "repo"), val(r, "tag"))
	case "delete-manifest":
		err = u.svc.DestDeleteManifest(ctx, host, val(r, "repo"), val(r, "digest"))
	case "tag":
		err = u.svc.DestTag(ctx, host, val(r, "repo"), val(r, "src"), val(r, "newTag"))
	case "push":
		var gen int
		if gen, err = intVal(r, "generation"); err == nil {
			err = u.svc.DestPushSynthetic(ctx, host, val(r, "repo"), val(r, "tag"), val(r, "contentKey"), gen)
		}
	case "purge":
		var res api.PurgeResult
		if res, err = u.svc.DestPurge(ctx, host, val(r, "prefix")); err == nil {
			msg = fmt.Sprintf("Deleted %d tags in %d repositories.", res.DeletedTags, len(res.Repositories))
		}
	case "purge-all":
		var res api.PurgeResult
		if res, err = u.svc.DestPurgeAll(ctx, host); err == nil {
			msg = fmt.Sprintf("Deleted %d tags in %d repositories.", res.DeletedTags, len(res.Repositories))
		}
	default:
		err = invalid("unknown operation %q", op)
	}
	if errors.Is(err, ErrNotFound) {
		u.fail(w, err)
		return
	}
	if err != nil {
		msg = ""
	}
	u.render(w, "destinations", "dest", u.destView(ctx, host, err, msg))
}
