// Package admin is the service layer behind the admin API and UI: every
// write is normalised and validated on the candidate state before it is
// stored.
package admin

import (
	"slices"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Service edits the state, the quota counters and the proxy backends.
type Service struct {
	cfg     *testerconfig.Config
	store   *state.Store
	quotas  *quota.Counters
	proxies *dest.Proxies
}

// NewService returns a Service; proxies resolves a host label to its backend
// client.
func NewService(cfg *testerconfig.Config, st *state.Store, q *quota.Counters, proxies *dest.Proxies) *Service {
	return &Service{cfg: cfg, store: st, quotas: q, proxies: proxies}
}

// update runs fn on a candidate state, normalises it and validates it
// before it is stored.
func (s *Service) update(fn func(*state.State) error) error {
	return s.store.Update(func(st *state.State) error {
		if err := fn(st); err != nil {
			return err
		}
		normalizeState(st)
		return validateState(st)
	})
}

// Rules: ordered, the first match wins, new rules go on top.

// ListRules returns the rules in order, never nil.
func (s *Service) ListRules() []api.Rule { return ruleColl.list(s) }

// CreateRule generates the ID and inserts the rule at index 0.
func (s *Service) CreateRule(r api.Rule) (api.Rule, error) { return ruleColl.create(s, r) }

// UpdateRule replaces the rule with the same ID in place.
func (s *Service) UpdateRule(r api.Rule) (api.Rule, error) { return ruleColl.replace(s, r) }

// DeleteRule removes a rule; an unknown ID is ErrNotFound.
func (s *Service) DeleteRule(id string) error { return ruleColl.remove(s, id) }

// MoveRule moves a rule to index, clamped to the list.
func (s *Service) MoveRule(id string, index int) error {
	return s.update(func(st *state.State) error {
		i := ruleColl.Find(st, id)
		if i < 0 {
			return ErrNotFound
		}
		r := st.Rules[i]
		st.Rules = slices.Delete(st.Rules, i, i+1)
		st.Rules = slices.Insert(st.Rules, max(0, min(index, len(st.Rules))), r)
		return nil
	})
}

// SetRuleEnabled turns a rule on or off in place.
func (s *Service) SetRuleEnabled(id string, on bool) error {
	return s.update(func(st *state.State) error {
		i := ruleColl.Find(st, id)
		if i < 0 {
			return ErrNotFound
		}
		st.Rules[i].Enabled = on
		return nil
	})
}

// ID-keyed settings: new ones go on top, so the newest wins.

// ListAliases returns the aliases, newest first, never nil.
func (s *Service) ListAliases() []api.Alias { return aliasColl.list(s) }

// CreateAlias generates the ID and inserts the alias at index 0.
func (s *Service) CreateAlias(a api.Alias) (api.Alias, error) { return aliasColl.create(s, a) }

// DeleteAlias removes an alias; an unknown ID is ErrNotFound.
func (s *Service) DeleteAlias(id string) error { return aliasColl.remove(s, id) }

// ListDivergents returns the divergent rules, newest first, never nil.
func (s *Service) ListDivergents() []api.Divergent { return divergentColl.list(s) }

// CreateDivergent generates the ID and inserts the rule at index 0.
func (s *Service) CreateDivergent(d api.Divergent) (api.Divergent, error) {
	return divergentColl.create(s, d)
}

// DeleteDivergent removes a divergent rule; an unknown ID is ErrNotFound.
func (s *Service) DeleteDivergent(id string) error { return divergentColl.remove(s, id) }

// ListGrants returns the grants, newest first, never nil.
func (s *Service) ListGrants() []api.Grant { return grantColl.list(s) }

// CreateGrant generates the ID and inserts the grant at index 0.
func (s *Service) CreateGrant(g api.Grant) (api.Grant, error) { return grantColl.create(s, g) }

// DeleteGrant removes a grant; an unknown ID is ErrNotFound.
func (s *Service) DeleteGrant(id string) error { return grantColl.remove(s, id) }

// Platform rules: ordered, the first match wins, new rules go on top.

// ListPlatforms returns the platform rules, newest first, never nil.
func (s *Service) ListPlatforms() []api.PlatformRule { return platformColl.list(s) }

// CreatePlatform generates the ID and inserts the rule at index 0.
func (s *Service) CreatePlatform(p api.PlatformRule) (api.PlatformRule, error) {
	return platformColl.create(s, p)
}

// DeletePlatform removes a platform rule; an unknown ID is ErrNotFound.
func (s *Service) DeletePlatform(id string) error { return platformColl.remove(s, id) }

// Name-keyed settings: Put creates or replaces.

// ListUsers returns the users, never nil.
func (s *Service) ListUsers() []api.User { return userColl.list(s) }

// PutUser creates or replaces a user.
func (s *Service) PutUser(u api.User) error { return errOnly(userColl.put(s, u)) }

// DeleteUser removes a user, unless a grant names it.
func (s *Service) DeleteUser(user string) error { return userColl.remove(s, user) }

// Host auth: hosts are lower-cased, a host in another case is the same one.

// ListHostAuth returns the host auth entries, never nil.
func (s *Service) ListHostAuth() []api.HostAuth { return hostAuthColl.list(s) }

// PutHostAuth creates or replaces the entry of a host.
func (s *Service) PutHostAuth(h api.HostAuth) error { return errOnly(hostAuthColl.put(s, h)) }

// DeleteHostAuth removes the entry of a host, whatever the case of the host.
func (s *Service) DeleteHostAuth(host string) error { return hostAuthColl.remove(s, host) }

// ListQuotas returns the quotas, never nil.
func (s *Service) ListQuotas() []api.QuotaDef { return quotaColl.list(s) }

// PutQuota creates or replaces a quota and resets its counters.
func (s *Service) PutQuota(q api.QuotaDef) error { return errOnly(quotaColl.put(s, q)) }

// DeleteQuota removes a quota, unless a rule names it, and resets its counters.
func (s *Service) DeleteQuota(key string) error { return quotaColl.remove(s, key) }

// QuotaUsage returns the used count per quota and key, current windows only.
func (s *Service) QuotaUsage() map[string]map[string]int { return s.quotas.Snapshot(time.Now()) }

// ResetQuota clears the counters of a defined quota.
func (s *Service) ResetQuota(key string) error {
	var found bool
	s.store.View(func(st *state.State) { found = quotaColl.Find(st, key) >= 0 })
	if !found {
		return ErrNotFound
	}
	s.quotas.Reset(key)
	return nil
}
