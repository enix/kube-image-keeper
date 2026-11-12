package state

import (
	"errors"
	"time"
)

// errNothingExpired aborts the update of an Expire that changed nothing, so
// that it writes no file.
var errNothingExpired = errors.New("nothing expired")

// Expire drops what was last seen more than ttl before now: the seen images
// older than that, with their generations, and the digests older than that.
// It returns how many of each it dropped, and writes the state only when
// something changed.
//
// A state from before expiry existed, or an import, holds what has no clock:
// digests without a last-seen time, generations of a tag never seen. Those
// are marked seen now, so nothing disappears before a full ttl has passed.
func (st *Store) Expire(now time.Time, ttl time.Duration) (images, digests int, err error) {
	cutoff := now.Add(-ttl)
	err = st.Update(func(s *State) error {
		changed := false
		for k := range s.Generations {
			if _, ok := s.Seen[k]; !ok {
				s.Seen[k] = now
				changed = true
			}
		}
		for k, t := range s.Seen {
			switch {
			case t.IsZero():
				s.Seen[k] = now
				changed = true
			case t.Before(cutoff):
				delete(s.Seen, k)
				delete(s.Generations, k)
				images++
			}
		}
		for k, e := range s.Digests {
			switch {
			case e.LastSeen.IsZero():
				e.LastSeen = now
				s.Digests[k] = e
				changed = true
			case e.LastSeen.Before(cutoff):
				delete(s.Digests, k)
				digests++
			}
		}
		if !changed && images == 0 && digests == 0 {
			return errNothingExpired
		}
		return nil
	})
	if errors.Is(err, errNothingExpired) {
		err = nil
	}
	return images, digests, err
}
