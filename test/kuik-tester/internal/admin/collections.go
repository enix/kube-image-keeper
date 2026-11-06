package admin

import (
	"slices"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// refs holds the names other objects may reference.
type refs struct{ users, quotas map[string]bool }

// coll is a list of the state as the service edits it: how its objects are
// normalised and checked, and what follows a change.
//
// The objects a coll stores or lists share their nested maps and slices
// with the state: callers must not modify them once handed over or listed.
type coll[T any] struct {
	state.Coll[T]
	// normalize puts an object in its stored form; nil when there is
	// nothing to normalise. It never writes into the caller's slices.
	normalize func(*T)
	// check validates one object, after its key and set checks; pure.
	check func(T, refs) error
	// changed runs after a successful put or remove; nil except quotas.
	changed func(s *Service, key string)
}

var (
	ruleColl      = coll[api.Rule]{Coll: state.RuleColl, normalize: normalizeRule, check: checkRule}
	aliasColl     = coll[api.Alias]{Coll: state.AliasColl, normalize: func(a *api.Alias) { lowerHost(&a.Host) }, check: checkAlias}
	divergentColl = coll[api.Divergent]{Coll: state.DivergentColl, normalize: func(d *api.Divergent) { lowerHost(&d.Host) }, check: checkDivergent}
	grantColl     = coll[api.Grant]{Coll: state.GrantColl, normalize: func(g *api.Grant) { lowerHost(&g.Host) }, check: checkGrant}
	platformColl  = coll[api.PlatformRule]{Coll: state.PlatformColl, normalize: func(p *api.PlatformRule) { lowerHost(&p.Host) }, check: checkPlatform}
	userColl      = coll[api.User]{Coll: state.UserColl, check: checkUser}
	hostAuthColl  = coll[api.HostAuth]{Coll: state.HostAuthColl, normalize: func(h *api.HostAuth) { lowerHost(&h.Host) }, check: checkHostAuth}
	quotaColl     = coll[api.QuotaDef]{Coll: state.QuotaColl, normalize: normalizeQuota, check: checkQuota,
		changed: func(s *Service, name string) { s.quotas.Reset(name) }}
)

// collection is what a write does on every list, whatever its type.
type collection interface {
	normalizeAll(st *state.State)
	validate(st *state.State, r refs) error
}

// collections lists the lists of the state in validation order: the
// referenced lists first.
var collections = []collection{
	userColl, quotaColl, ruleColl, aliasColl, divergentColl, grantColl, hostAuthColl, platformColl,
}

// list copies the collection under View; the result is never nil. The
// objects share their nested maps and slices with the published state.
func (c coll[T]) list(s *Service) []T {
	out := []T{}
	s.store.View(func(st *state.State) { out = append(out, *c.Slice(st)...) })
	return out
}

// create inserts v at index 0 under a new ID and returns the stored element.
func (c coll[T]) create(s *Service, v T) (T, error) {
	*c.Key(&v) = state.NewID()
	c.norm(&v)
	return stored(v, s.update(func(st *state.State) error {
		p := c.Slice(st)
		*p = slices.Insert(*p, 0, v)
		return nil
	}))
}

// replace swaps the element with the same key in place and returns the
// stored element; an unknown key is ErrNotFound.
func (c coll[T]) replace(s *Service, v T) (T, error) {
	c.norm(&v)
	return stored(v, s.update(func(st *state.State) error {
		i := c.Find(st, *c.Key(&v))
		if i < 0 {
			return ErrNotFound
		}
		(*c.Slice(st))[i] = v
		return nil
	}))
}

// put replaces the element with the same key, or appends it, and returns the
// stored element.
func (c coll[T]) put(s *Service, v T) (T, error) {
	c.norm(&v)
	err := s.update(func(st *state.State) error {
		p := c.Slice(st)
		if i := c.Find(st, *c.Key(&v)); i >= 0 {
			(*p)[i] = v
		} else {
			*p = append(*p, v)
		}
		return nil
	})
	if err == nil {
		c.afterChange(s, *c.Key(&v))
	}
	return stored(v, err)
}

// remove deletes the element with the key, normalised first; an unknown key
// is ErrNotFound.
func (c coll[T]) remove(s *Service, key string) error {
	var z T
	*c.Key(&z) = key
	c.norm(&z)
	key = *c.Key(&z)
	err := s.update(func(st *state.State) error {
		i := c.Find(st, key)
		if i < 0 {
			return ErrNotFound
		}
		p := c.Slice(st)
		*p = slices.Delete(*p, i, i+1)
		return nil
	})
	if err == nil {
		c.afterChange(s, key)
	}
	return err
}

// stored returns v, or the zero value when the write failed.
func stored[T any](v T, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

func (c coll[T]) norm(v *T) {
	if c.normalize != nil {
		c.normalize(v)
	}
}

func (c coll[T]) afterChange(s *Service, key string) {
	if c.changed != nil {
		c.changed(s, key)
	}
}

func (c coll[T]) normalizeAll(st *state.State) {
	p := *c.Slice(st)
	for i := range p {
		c.norm(&p[i])
	}
}

// validate checks every object: its key (non-empty for an ID, unique), its
// set name, then check.
func (c coll[T]) validate(st *state.State, r refs) error {
	seen := map[string]bool{}
	for _, v := range *c.Slice(st) {
		key := *c.Key(&v)
		switch {
		case c.IDKeyed() && key == "":
			return invalid("%s: empty id", c.What)
		case c.IDKeyed() && seen[key]:
			return invalid("%s %s: duplicate id", c.What, key)
		case seen[key]:
			return invalid("%s %q: duplicate %s", c.What, key, c.KeyName)
		}
		seen[key] = true
		if set := *c.Set(&v); set != "" {
			if err := checkSetName(set); err != nil {
				return err
			}
		}
		if err := c.check(v, r); err != nil {
			return err
		}
	}
	return nil
}

// keys returns the set of the keys of the collection.
func (c coll[T]) keys(st *state.State) map[string]bool {
	out := map[string]bool{}
	for _, v := range *c.Slice(st) {
		out[*c.Key(&v)] = true
	}
	return out
}

// normalizeState puts every object of the candidate state in its stored
// form: host globs lower-cased, methods upper-cased.
func normalizeState(st *state.State) {
	for _, c := range collections {
		c.normalizeAll(st)
	}
}

func normalizeRule(r *api.Rule) {
	lowerHost(&r.Target.Host)
	r.Target.Methods = upperMethods(r.Target.Methods)
}

func normalizeQuota(q *api.QuotaDef) { q.Methods = upperMethods(q.Methods) }

// lowerHost lower-cases a host glob: host names are case-insensitive.
func lowerHost(h *string) { *h = strings.ToLower(*h) }

// upperMethods returns the methods upper-cased in a new slice; nil stays nil.
func upperMethods(ms []string) []string {
	if ms == nil {
		return nil
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = strings.ToUpper(m)
	}
	return out
}

// errOnly drops the value of a two-valued call.
func errOnly[T any](_ T, err error) error { return err }
