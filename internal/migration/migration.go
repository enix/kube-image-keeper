// Package migration runs the `migrate` subcommand: it guides a cluster from kuik v2 to v3,
// reading the cluster and printing what to do, never acting on it.
package migration

import (
	"context"
	"io"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultStateFile is the state file of a run that names none, in the current directory.
const DefaultStateFile = "kuik-migration-state.json"

// Options configures a run.
type Options struct {
	// StateFile is the path of the state file, DefaultStateFile when empty.
	StateFile string
	// Namespace is the namespace kuik v3 is installed in.
	Namespace string
	// Out receives what the run prints.
	Out io.Writer
	// Err receives the errors of the run.
	Err io.Writer
}

// Run reads the cluster through r, completes the state file and prints what it found. It
// returns the exit code of the subcommand.
func Run(ctx context.Context, r client.Reader, opts Options) int {
	return 0
}
