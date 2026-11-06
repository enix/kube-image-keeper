package admin

import (
	"cmp"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// State

// StateJSON returns the whole state, digests and seen images included;
// empty lists are [], never null.
func (s *Service) StateJSON() ([]byte, error) {
	var b []byte
	var err error
	s.store.View(func(st *state.State) {
		c := *st
		c.FillEmptyLists()
		b, err = json.Marshal(c)
	})
	return b, err
}

// Export returns the settings as YAML.
func (s *Service) Export() ([]byte, error) { return s.store.Export() }

// Sets

var setNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func checkSetName(set string) error {
	if !setNameRe.MatchString(set) {
		return invalid("set name %q: letters, digits, '_', '.', '-', 1 to 64 characters", set)
	}
	return nil
}

// Sets lists the sets in use, sorted by name; never nil.
func (s *Service) Sets() []api.SetInfo {
	counts := map[string]int{}
	s.store.View(func(st *state.State) {
		st.EachSet(func(set string) { counts[set]++ })
	})
	out := make([]api.SetInfo, 0, len(counts))
	for n, c := range counts {
		out = append(out, api.SetInfo{Name: n, Objects: c})
	}
	slices.SortFunc(out, func(a, b api.SetInfo) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// ExportSet returns the objects of a set as YAML.
func (s *Service) ExportSet(set string) ([]byte, error) {
	if err := checkSetName(set); err != nil {
		return nil, err
	}
	return s.store.ExportSet(set)
}

// DeleteSet removes every object of the set in one validated update: a
// reference left dangling (a grant outside the set naming a user inside
// it) refuses the whole deletion. The counters of the set's quotas are
// reset. An unused or empty set name is ErrNotFound.
func (s *Service) DeleteSet(set string) error {
	var quotas []string
	err := s.update(func(st *state.State) error {
		quotas = setQuotaNames(st, set)
		if st.DropSet(set) == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.resetQuotas(quotas)
	return nil
}

// ImportSet merges the YAML b into the settings under the set name (see
// state.Store.Merge). The candidate is validated first; on a rejection
// nothing changes. The counters of the quotas it brings are reset.
func (s *Service) ImportSet(set string, b []byte) error {
	if err := checkSetName(set); err != nil {
		return err
	}
	var quotas []string
	err := s.store.Merge(b, set, func(st *state.State) error {
		if err := checkImported(st); err != nil {
			return err
		}
		quotas = setQuotaNames(st, set)
		return nil
	})
	if err != nil {
		return importErr(err)
	}
	s.resetQuotas(quotas)
	return nil
}

// Import replaces the settings with the YAML b. The candidate is validated
// first; on a rejection nothing changes, counters included. The counters of
// every quota named before or after the import are reset.
func (s *Service) Import(b []byte) error {
	var quotas []string
	s.store.View(func(st *state.State) { quotas = quotaNames(st.Quotas) })
	err := s.store.Import(b, func(st *state.State) error {
		if err := checkImported(st); err != nil {
			return err
		}
		quotas = append(quotas, quotaNames(st.Quotas)...)
		return nil
	})
	if err != nil {
		return importErr(err)
	}
	s.resetQuotas(quotas)
	return nil
}

func (s *Service) resetQuotas(names []string) {
	for _, n := range names {
		s.quotas.Reset(n)
	}
}

func quotaNames(qs []api.QuotaDef) []string {
	names := make([]string, len(qs))
	for i, q := range qs {
		names[i] = q.Name
	}
	return names
}

// setQuotaNames lists the quotas of a set.
func setQuotaNames(st *state.State, set string) []string {
	var names []string
	for _, q := range st.Quotas {
		if q.Set == set {
			names = append(names, q.Name)
		}
	}
	return names
}

// checkImported gives an ID to every imported rule, alias, divergent, grant
// and platform rule that has none, so hand-written YAML need not invent
// them, then normalises and validates the candidate. Duplicates are still
// refused.
func checkImported(st *state.State) error {
	st.FillIDs()
	normalizeState(st)
	return validateState(st)
}
