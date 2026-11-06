package admin

import (
	"context"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Purge: wipe one kind of object at once.

// The kinds PurgeColl takes: the API names of the lists.
const (
	kindRules      = "rules"
	kindAliases    = "aliases"
	kindDivergents = "divergents"
	kindGrants     = "grants"
	kindPlatforms  = "platforms"
	kindUsers      = "users"
	kindHostAuth   = "hostauth"
	kindQuotas     = "quotas"
)

// purgeKinds are the lists DELETE /{kind} empties.
var purgeKinds = []string{kindRules, kindAliases, kindDivergents, kindGrants, kindPlatforms, kindUsers, kindHostAuth, kindQuotas}

// PurgeColl removes every object of a list, the ones of a set included, and
// returns how many. The kind is the API name: rules, aliases, divergents,
// grants, platforms, users, hostauth or quotas; any other is invalid. The
// result is validated like any write: emptying users or quotas that a grant
// or a rule still names is refused, and nothing changes. The counters of the
// removed quotas are reset.
func (s *Service) PurgeColl(kind string) (int, error) {
	var n int
	var quotas []string
	err := s.update(func(st *state.State) error {
		if kind == kindQuotas {
			quotas = quotaNames(st.Quotas)
		}
		var known bool
		if n, known = st.PurgeColl(kind); !known {
			return invalid("unknown kind %q to purge", kind)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.resetQuotas(quotas)
	return n, nil
}

// seenImages returns the images of Seen and of Generations, deduplicated.
func seenImages(st *state.State) map[string]bool {
	images := map[string]bool{}
	for k := range st.Seen {
		images[k] = true
	}
	for k := range st.Generations {
		images[k] = true
	}
	return images
}

// PurgeableImages is how many images PurgeImages would forget.
func (s *Service) PurgeableImages() int {
	n := 0
	s.store.View(func(st *state.State) { n = len(seenImages(st)) })
	return n
}

// PurgeImages forgets the seen images, their digests and their generations,
// and returns how many images that was. A rule that names an image literally
// keeps it listed.
func (s *Service) PurgeImages() (int, error) {
	var n int
	err := s.store.Update(func(st *state.State) error {
		n = len(seenImages(st))
		st.Seen = map[string]time.Time{}
		st.Digests = map[string]state.DigestEntry{}
		st.Generations = map[string]int{}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// DestPurgeAll deletes every tag of every repository of a proxy host.
func (s *Service) DestPurgeAll(ctx context.Context, host string) (api.PurgeResult, error) {
	res := api.PurgeResult{Repositories: []string{}}
	err := s.withDest(host, func(c *dest.Client) (err error) {
		res, err = c.PurgeAll(ctx)
		return err
	})
	return res, err
}

// PurgeAll brings the tester back to a blank state: every list, every seen
// image, digest and generation is emptied in one write, and every quota
// counter is reset. It returns what it removed. The request log is not the
// Service's: purgeAll clears it.
//
// With backends, every distinct proxy backend is then emptied, once however
// many proxy names share it. A backend that fails does not stop the others:
// its error is in the result, and the state is purged anyway.
func (s *Service) PurgeAll(ctx context.Context, backends bool) (api.PurgeAllResult, error) {
	var res api.PurgeAllResult
	// The write skips Service.update on purpose: an empty state is valid and
	// everything goes at once, so no reference can dangle.
	err := s.store.Update(func(st *state.State) error {
		res.Images = len(seenImages(st))
		res.Rules, res.Aliases, res.Divergents = len(st.Rules), len(st.Aliases), len(st.Divergents)
		res.Grants, res.Platforms, res.Users = len(st.Grants), len(st.Platforms), len(st.Users)
		res.HostAuth, res.Quotas = len(st.HostAuth), len(st.Quotas)
		*st = state.State{
			Seen:        map[string]time.Time{},
			Digests:     map[string]state.DigestEntry{},
			Generations: map[string]int{},
		}
		st.FillEmptyLists()
		return nil
	})
	if err != nil {
		return api.PurgeAllResult{}, err
	}
	s.quotas.ResetAll()
	if backends {
		for _, b := range s.proxies.Backends() {
			bp := api.BackendPurge{Backend: b.Name, Repositories: []string{}}
			r, err := b.Client.PurgeAll(ctx)
			bp.Repositories, bp.DeletedTags = r.Repositories, r.DeletedTags
			if err != nil {
				bp.Error = destErr(err).Error()
			}
			res.Backends = append(res.Backends, bp)
		}
	}
	return res, nil
}

// purgeAll is PurgeAll plus the request log, which the Service does not hold:
// the log is cleared and its stats reset, and the count of entries it held is
// in the result. The API and the UI both go through it.
func purgeAll(ctx context.Context, svc *Service, log *reqlog.Log, backends bool) (api.PurgeAllResult, error) {
	res, err := svc.PurgeAll(ctx, backends)
	if err != nil {
		return res, err
	}
	res.Requests = log.Clear()
	log.ResetStats()
	return res, nil
}
