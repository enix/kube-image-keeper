// Package state keeps, in a local file, what a migration from kuik v2 needs once v2 is gone:
// the v2 resources with their status, the origin of each image v2 rewrote, and the outcome
// of the retag.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/enix/kube-image-keeper/internal/migration/inventory"
)

// FormatVersion is the version of the file format this package writes.
const FormatVersion = 1

// File is the content of the state file.
type File struct {
	// FormatVersion is the format the file was written in.
	FormatVersion int `json:"formatVersion"`
	// V2Version is the version of the v2 that ran when the file was last completed.
	V2Version string `json:"v2Version,omitempty"`
	// V2Namespace is the namespace v2 ran in.
	V2Namespace string `json:"v2Namespace,omitempty"`
	// Resources are the v2 resources, with the latest status read while v2 ran.
	Resources []inventory.Resource `json:"resources,omitempty"`
	// Pods are the pods v2 rewrote, every run's.
	Pods []inventory.Pod `json:"pods,omitempty"`
}

// Outcome is what Sync did to the file.
type Outcome string

const (
	// Created is a file written for the first time.
	Created Outcome = "created"
	// Updated is a file completed with what the cluster holds now.
	Updated Outcome = "updated"
	// Unchanged is a file left as it was, v2 being gone.
	Unchanged Outcome = "unchanged"
	// Missing is no file, and none written since v2 is gone.
	Missing Outcome = "missing"
)

// Sync completes the file at path with inv while v2 runs: the pods of earlier runs are kept
// and the new ones added, each v2 resource takes its latest spec and status, and none is
// dropped. Once v2 is gone the file is left as it is, or not written when there is none: the
// cluster then no longer tells reliably which copy came from which origin.
func Sync(path string, inv *inventory.Inventory) (Outcome, error) {
	file, err := Load(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if !inv.V2.Running() {
		if exists {
			return Unchanged, nil
		}
		return Missing, nil
	}

	outcome := Updated
	if !exists {
		file, outcome = &File{}, Created
	}
	file.FormatVersion = FormatVersion
	file.V2Version, file.V2Namespace = inv.V2.Version, inv.V2.Namespace
	file.Resources = merge(file.Resources, inv.V2Resources, func(r inventory.Resource) string {
		return r.Kind + "/" + r.Namespace + "/" + r.Name
	})
	file.Pods = merge(file.Pods, inv.Pods, func(p inventory.Pod) string {
		return p.Namespace + "/" + p.Name
	})
	if err := save(path, file); err != nil {
		return "", err
	}
	return outcome, nil
}

// merge returns the items of earlier and latest, an item of latest replacing the one of earlier
// with the same key, sorted by key.
func merge[T any](earlier, latest []T, key func(T) string) []T {
	byKey := map[string]T{}
	for _, item := range earlier {
		byKey[key(item)] = item
	}
	for _, item := range latest {
		byKey[key(item)] = item
	}
	keys := slices.Sorted(maps.Keys(byKey))
	merged := make([]T, 0, len(keys))
	for _, k := range keys {
		merged = append(merged, byKey[k])
	}
	return merged
}

// Load reads the file at path. It refuses a file written in a newer format than this
// package knows: completing it would drop what the newer format added. An error wraps
// fs.ErrNotExist when there is no file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the state file: %w", err)
	}
	file := &File{}
	if err := json.Unmarshal(data, file); err != nil {
		return nil, fmt.Errorf("decoding the state file %s: %w", path, err)
	}
	if file.FormatVersion < 1 {
		return nil, fmt.Errorf("%s is not a kuik migration state file: it has no format version", path)
	}
	if file.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("the state file %s has format version %d, this kuik reads up to %d: run a newer kuik",
			path, file.FormatVersion, FormatVersion)
	}
	return file, nil
}

// save writes file at path, through a temporary file flushed to disk and renamed over it, then
// flushes the directory: an interrupted run or a crash leaves either the previous state or the
// new one, never a truncated file.
func save(path string, file *File) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the state file: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing the state file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing the state file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing the state file: %w", err)
	}
	return syncDir(filepath.Dir(path))
}

// syncDir flushes the entries of dir, so that a rename in it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("writing the state file: %w", err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("writing the state file: %w", err)
	}
	return nil
}
