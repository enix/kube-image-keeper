// Package state keeps, in a local file, what a migration from kuik v2 needs once v2 is gone:
// the v2 resources with their status, the origin of each image v2 rewrote, and the outcome
// of the retag.
package state

import (
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

// Sync completes the file at path with inv while v2 runs.
func Sync(path string, inv *inventory.Inventory) (Outcome, error) {
	return "", nil
}

// Load reads the file at path.
func Load(path string) (*File, error) {
	return nil, nil
}
