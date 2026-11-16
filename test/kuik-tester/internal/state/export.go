package state

import (
	"maps"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
	"sigs.k8s.io/yaml"
)

// exported is the part of the state that export and import carry: every
// field except the digest index and the seen images.
type exported struct {
	Rules       []api.Rule         `json:"rules"`
	Aliases     []api.Alias        `json:"aliases"`
	Divergents  []api.Divergent    `json:"divergents"`
	Users       []api.User         `json:"users"`
	Grants      []api.Grant        `json:"grants"`
	HostAuth    []api.HostAuth     `json:"hostAuth"`
	Quotas      []api.QuotaDef     `json:"quotas"`
	Platforms   []api.PlatformRule `json:"platforms"`
	Generations map[string]int     `json:"generations"`
}

// exportedOf returns the exported fields of st; the lists are shared.
func exportedOf(st *State) exported {
	return exported{st.Rules, st.Aliases, st.Divergents, st.Users, st.Grants, st.HostAuth, st.Quotas, st.Platforms, st.Generations}
}

// applyTo replaces the exported fields of st with those of e.
func (e exported) applyTo(st *State) {
	st.Rules, st.Aliases, st.Divergents, st.Users = e.Rules, e.Aliases, e.Divergents, e.Users
	st.Grants, st.HostAuth, st.Quotas, st.Platforms = e.Grants, e.HostAuth, e.Quotas, e.Platforms
	st.Generations = e.Generations
}

// InvalidError is an import that is not a valid settings document, or a set
// import that would take over an entry of another set.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return "invalid import: " + e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

// decode reads a settings document; unknown fields are refused.
func decode(b []byte) (exported, error) {
	var e exported
	if err := yaml.UnmarshalStrict(b, &e); err != nil {
		return e, &InvalidError{err}
	}
	return e, nil
}

// Export returns the state as YAML, without digests and seen images.
func (s *Store) Export() ([]byte, error) {
	return yaml.Marshal(exportedOf(s.Snapshot()))
}

// Import replaces every exported field with the YAML document b, keeping
// digests and seen images. A non-nil check runs on the candidate state; if
// it fails nothing changes.
func (s *Store) Import(b []byte, check func(*State) error) error {
	e, err := decode(b)
	if err != nil {
		return err
	}
	return s.Update(func(st *State) error {
		e.applyTo(st)
		if check != nil {
			return check(st)
		}
		return nil
	})
}

// ExportSet returns the objects of the set as YAML; generations are not
// part of a set.
func (s *Store) ExportSet(set string) ([]byte, error) {
	sub, src := &State{}, s.Snapshot()
	for _, c := range collections {
		c.keepSet(sub, src, set)
	}
	sub.FillEmptyLists()
	return yaml.Marshal(exportedOf(sub))
}

// Merge adds the YAML document b to the state under the set name: ID-keyed
// objects are inserted on top in file order, name-keyed objects replace the
// entry of the same key (unless another set owns it: then the merge is
// refused) or are appended, generations are merged. A non-nil check runs on
// the candidate state; if it fails nothing changes.
func (s *Store) Merge(b []byte, set string, check func(*State) error) error {
	e, err := decode(b)
	if err != nil {
		return err
	}
	in := &State{}
	e.applyTo(in)
	return s.Update(func(st *State) error {
		for _, c := range collections {
			if err := c.merge(st, in, set); err != nil {
				return err
			}
		}
		maps.Copy(st.Generations, in.Generations)
		if check != nil {
			return check(st)
		}
		return nil
	})
}
