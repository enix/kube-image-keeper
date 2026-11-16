package state

import (
	"fmt"
	"slices"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Coll describes one list of the State.
type Coll[T any] struct {
	// What names an object of the list in messages: "rule", "user"...
	What string
	// KeyName is the JSON name of the key: "id", or "name" or "host" for
	// the name-keyed lists.
	KeyName string
	Slice   func(*State) *[]T
	// Key returns the ID, or the name or host of a name-keyed object.
	Key func(*T) *string
	Set func(*T) *string
}

// Find returns the index of the object of the key, or -1.
func (c Coll[T]) Find(st *State, key string) int {
	return slices.IndexFunc(*c.Slice(st), func(v T) bool { return *c.Key(&v) == key })
}

// IDKeyed reports a list keyed by a generated ID: new objects go on top.
// The other lists are keyed by a name or a host: a put replaces.
func (c Coll[T]) IDKeyed() bool { return c.KeyName == "id" }

// The lists of the State.
var (
	RuleColl = Coll[api.Rule]{
		What:    "rule",
		KeyName: "id",
		Slice:   func(st *State) *[]api.Rule { return &st.Rules },
		Key:     func(r *api.Rule) *string { return &r.ID },
		Set:     func(r *api.Rule) *string { return &r.Set },
	}
	AliasColl = Coll[api.Alias]{
		What:    "alias",
		KeyName: "id",
		Slice:   func(st *State) *[]api.Alias { return &st.Aliases },
		Key:     func(a *api.Alias) *string { return &a.ID },
		Set:     func(a *api.Alias) *string { return &a.Set },
	}
	DivergentColl = Coll[api.Divergent]{
		What:    "divergent",
		KeyName: "id",
		Slice:   func(st *State) *[]api.Divergent { return &st.Divergents },
		Key:     func(d *api.Divergent) *string { return &d.ID },
		Set:     func(d *api.Divergent) *string { return &d.Set },
	}
	UserColl = Coll[api.User]{
		What:    "user",
		KeyName: "name",
		Slice:   func(st *State) *[]api.User { return &st.Users },
		Key:     func(u *api.User) *string { return &u.Name },
		Set:     func(u *api.User) *string { return &u.Set },
	}
	GrantColl = Coll[api.Grant]{
		What:    "grant",
		KeyName: "id",
		Slice:   func(st *State) *[]api.Grant { return &st.Grants },
		Key:     func(g *api.Grant) *string { return &g.ID },
		Set:     func(g *api.Grant) *string { return &g.Set },
	}
	HostAuthColl = Coll[api.HostAuth]{
		What:    "hostAuth",
		KeyName: "host",
		Slice:   func(st *State) *[]api.HostAuth { return &st.HostAuth },
		Key:     func(h *api.HostAuth) *string { return &h.Host },
		Set:     func(h *api.HostAuth) *string { return &h.Set },
	}
	QuotaColl = Coll[api.QuotaDef]{
		What:    "quota",
		KeyName: "name",
		Slice:   func(st *State) *[]api.QuotaDef { return &st.Quotas },
		Key:     func(q *api.QuotaDef) *string { return &q.Name },
		Set:     func(q *api.QuotaDef) *string { return &q.Set },
	}
	PlatformColl = Coll[api.PlatformRule]{
		What:    "platform rule",
		KeyName: "id",
		Slice:   func(st *State) *[]api.PlatformRule { return &st.Platforms },
		Key:     func(p *api.PlatformRule) *string { return &p.ID },
		Set:     func(p *api.PlatformRule) *string { return &p.Set },
	}
)

// collection is what the State does on every list, whatever its type.
type collection interface {
	eachSet(st *State, fn func(set string))
	dropSet(st *State, set string) int
	purge(st *State) int
	keepSet(dst, src *State, set string)
	merge(dst, src *State, set string) error
	fillEmpty(st *State)
	fillIDs(st *State)
}

// collections describes every list of the State, in field order.
var collections = []collection{
	RuleColl, AliasColl, DivergentColl, UserColl, GrantColl, HostAuthColl, QuotaColl, PlatformColl,
}

// collByKind maps the API name of a list to it.
var collByKind = map[string]collection{
	"rules": RuleColl, "aliases": AliasColl, "divergents": DivergentColl, "grants": GrantColl,
	"platforms": PlatformColl, "users": UserColl, "hostauth": HostAuthColl, "quotas": QuotaColl,
}

func (c Coll[T]) eachSet(st *State, fn func(set string)) {
	for _, v := range *c.Slice(st) {
		if set := *c.Set(&v); set != "" {
			fn(set)
		}
	}
}

// dropSet removes the objects of the set, the untagged ones for "".
func (c Coll[T]) dropSet(st *State, set string) int {
	p := c.Slice(st)
	n := len(*p)
	*p = slices.DeleteFunc(*p, func(v T) bool { return *c.Set(&v) == set })
	return n - len(*p)
}

// purge empties the list, sets included, and returns how many objects it held.
func (c Coll[T]) purge(st *State) int {
	p := c.Slice(st)
	n := len(*p)
	*p = []T{}
	return n
}

// keepSet appends to dst the objects of src that belong to the set; src is
// left untouched.
func (c Coll[T]) keepSet(dst, src *State, set string) {
	for _, v := range *c.Slice(src) {
		if *c.Set(&v) == set {
			*c.Slice(dst) = append(*c.Slice(dst), v)
		}
	}
}

// merge tags the objects of src with the set and adds them to dst: on top,
// in order, for an ID-keyed list; by key otherwise (see putByKey).
func (c Coll[T]) merge(dst, src *State, set string) error {
	in := *c.Slice(src)
	for i := range in {
		*c.Set(&in[i]) = set
	}
	if c.IDKeyed() {
		p := c.Slice(dst)
		*p = slices.Concat(in, *p)
		return nil
	}
	for _, v := range in {
		if err := c.putByKey(dst, v, set); err != nil {
			return err
		}
	}
	return nil
}

// putByKey replaces the object of the same key, or appends v. An object that
// belongs to another set is never taken over: a set import must not silently
// retag what the base state or another set owns.
func (c Coll[T]) putByKey(st *State, v T, set string) error {
	p := c.Slice(st)
	i := c.Find(st, *c.Key(&v))
	if i < 0 {
		*p = append(*p, v)
		return nil
	}
	if owner := *c.Set(&(*p)[i]); owner != "" && owner != set {
		return &InvalidError{fmt.Errorf("%s %q belongs to set %q: a set import cannot take it over", c.What, *c.Key(&v), owner)}
	}
	(*p)[i] = v
	return nil
}

func (c Coll[T]) fillEmpty(st *State) {
	if p := c.Slice(st); *p == nil {
		*p = []T{}
	}
}

func (c Coll[T]) fillIDs(st *State) {
	if !c.IDKeyed() {
		return
	}
	in := *c.Slice(st)
	for i := range in {
		if id := c.Key(&in[i]); *id == "" {
			*id = NewID()
		}
	}
}

// EachSet calls fn with the set of every object that has one.
func (st *State) EachSet(fn func(set string)) {
	for _, c := range collections {
		c.eachSet(st, fn)
	}
}

// DropSet removes the objects of the set and returns how many. The empty
// set name drops nothing: the untagged objects are not a set.
func (st *State) DropSet(set string) int {
	if set == "" {
		return 0
	}
	n := 0
	for _, c := range collections {
		n += c.dropSet(st, set)
	}
	return n
}

// PurgeColl empties the list of the API kind ("rules", "aliases", "divergents",
// "grants", "platforms", "users", "hostauth" or "quotas"), set-tagged objects
// included, and returns how many objects it held. The bool is false for an
// unknown kind.
func (st *State) PurgeColl(kind string) (int, bool) {
	c, ok := collByKind[kind]
	if !ok {
		return 0, false
	}
	return c.purge(st), true
}

// FillIDs gives a new ID to every ID-keyed object without one.
func (st *State) FillIDs() {
	for _, c := range collections {
		c.fillIDs(st)
	}
}

// FillEmptyLists turns the nil lists into empty ones, so that they marshal
// as [] rather than null.
func (st *State) FillEmptyLists() {
	for _, c := range collections {
		c.fillEmpty(st)
	}
}
