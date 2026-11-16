package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Store holds the State and persists it as JSON on every change (RecordImage
// excepted when the write fails: see its doc). Readers get the current state
// without waiting: a published state is never modified, each write publishes
// a new one. Writers are serialised.
type Store struct {
	wmu  sync.Mutex // serialises writers; readers never take it
	st   atomic.Pointer[State]
	path string
	// refresh is the nanoseconds a last-seen time may lag before a request
	// writes it again; 0 never writes it again.
	refresh atomic.Int64
}

// SetRefresh sets how stale a last-seen time may get before RecordImage or
// TouchDigest write it again: at most one write per image or digest per
// interval. 0 (the default) never refreshes it, which suits a store that
// never expires anything.
func (s *Store) SetRefresh(d time.Duration) {
	s.refresh.Store(int64(d))
}

// staleBefore returns the time before which a last-seen time is stale, and
// false when the store never refreshes.
func (s *Store) staleBefore() (time.Time, bool) {
	r := time.Duration(s.refresh.Load())
	if r <= 0 {
		return time.Time{}, false
	}
	return time.Now().UTC().Add(-r), true
}

// Open loads the state from path. An empty path keeps the state in memory
// only; a missing or zero-length file gives an empty state; a file that
// cannot be parsed is moved to path+".corrupt" (replacing an older one) and
// the state starts empty. Failing to move it aside, or any other read error,
// is an error.
func Open(path string) (*Store, error) {
	st := &State{}
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("read state %s: %w", path, err)
		case len(b) == 0:
		default:
			if err := json.Unmarshal(b, st); err != nil {
				corrupt := path + ".corrupt"
				if rerr := os.Rename(path, corrupt); rerr != nil {
					return nil, fmt.Errorf("parse state %s: %w (and moving it aside failed: %v)", path, err, rerr)
				}
				slog.Error("Moved the unreadable state file aside, starting from an empty state", "path", path, "movedTo", corrupt, "error", err)
				st = &State{}
			}
		}
	}
	st.init()
	s := &Store{path: path}
	s.st.Store(st)
	return s, nil
}

// View calls fn with the current state. It never waits for a write in
// progress: fn sees the state as of its call. fn must not keep or modify
// the state.
func (s *Store) View(fn func(*State)) {
	fn(s.st.Load())
}

// Update calls fn on a copy of the state, with every map non-nil. If fn
// returns nil the copy is persisted, then published to readers; if fn or
// the write fails, nothing changes and the error is returned. Writers run
// one at a time; readers do not wait for them.
func (s *Store) Update(fn func(*State) error) error {
	return s.update(fn, false)
}

// update is Update; with publishOnPersistError a failed write still
// publishes the new state, logs the failure and returns nil.
func (s *Store) update(fn func(*State) error, publishOnPersistError bool) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	next, err := clone(s.st.Load())
	if err != nil {
		return err
	}
	if err := fn(next); err != nil {
		return err
	}
	next.init()
	if err := s.persist(next); err != nil {
		if !publishOnPersistError {
			return err
		}
		slog.Error("Kept the new state in memory only, the state file could not be written", "path", s.path, "error", err)
	}
	s.st.Store(next)
	return nil
}

// Snapshot returns the current state. A published state is never modified:
// later writes leave it alone. Callers must not modify it.
func (s *Store) Snapshot() *State {
	return s.st.Load()
}

// persist writes st to a temporary file and renames it over the state file.
// On failure the temporary file is removed.
func (s *Store) persist(st *State) error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	renamed = true
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func clone(st *State) (*State, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	out := &State{}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, err
	}
	out.init()
	return out, nil
}
