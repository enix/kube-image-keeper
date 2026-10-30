// Package migration runs the `migrate` subcommand: it guides a cluster from kuik v2 to v3,
// reading the cluster and printing what to do, never acting on it.
package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/enix/kube-image-keeper/internal/migration/inventory"
	"github.com/enix/kube-image-keeper/internal/migration/state"
)

// DefaultStateFile is the state file of a run that names none, in the current directory.
const DefaultStateFile = "kuik-migration-state.json"

// The exit codes of a run.
const (
	// ExitComplete: the cluster holds no trace of v2 and no migration is under way.
	ExitComplete = 0
	// ExitIncomplete: the migration is not complete, or cannot go on.
	ExitIncomplete = 1
	// ExitError: the cluster or the state file could not be read or written.
	ExitError = 2
)

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
// returns the exit code of the subcommand: ExitComplete only when the cluster holds no trace
// of v2 and there is no state file. Errors, and what stops the migration, go to Err.
func Run(ctx context.Context, r client.Reader, opts Options) int {
	if opts.StateFile == "" {
		opts.StateFile = DefaultStateFile
	}
	out, errOut := opts.Out, opts.Err
	if errOut == nil {
		errOut = out
	}

	// The resources an earlier run saved keep their mirror paths once deleted from the cluster.
	var saved []inventory.Resource
	file, err := state.Load(opts.StateFile)
	switch {
	case err == nil:
		saved = file.Resources
	case !errors.Is(err, fs.ErrNotExist):
		_, _ = fmt.Fprintf(errOut, "Error: %v\n", err)
		return ExitError
	}

	inv, err := inventory.Read(ctx, r, inventory.Options{Namespace: opts.Namespace, MirrorResources: saved})
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error: reading the cluster: %v\n", err)
		return ExitError
	}
	printInventory(out, inv)

	outcome, err := state.Sync(opts.StateFile, inv)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error: %v\n", err)
		return ExitError
	}

	traces := hasV2Traces(inv)
	switch {
	case outcome == state.Missing && !traces:
		_, _ = fmt.Fprintln(out, "\nThe cluster holds no trace of kuik v2 and there is no state file: nothing to migrate.")
		return ExitComplete
	case outcome == state.Missing:
		_, _ = fmt.Fprintf(errOut, "kuik v2 no longer runs, it left traces on the cluster, and there is no state file at %s.\n"+
			"The migration cannot go on: v2 recorded which origin each mirrored image came from in resources it no longer "+
			"updates, and the cluster alone no longer tells it reliably. Run migrate from the directory that holds the "+
			"state file of an earlier run, or pass it with --state-file.\n", opts.StateFile)
		return ExitIncomplete
	}

	_, _ = fmt.Fprintf(out, "\nState file %s: %s.\n", opts.StateFile, outcome)
	if traces {
		_, _ = fmt.Fprintln(out, "The migration is not complete: kuik v2 still has traces on the cluster.")
	} else {
		_, _ = fmt.Fprintln(out, "The migration is not complete: keep the state file until it is.")
	}
	return ExitIncomplete
}

// hasV2Traces reports whether anything of v2 remains on the cluster.
func hasV2Traces(inv *inventory.Inventory) bool {
	return inv.V2.Running() || inv.V2.WebhookConfiguration != "" || len(inv.V2CRDs) > 0 ||
		len(inv.V2Resources) > 0 || len(inv.Pods) > 0 || len(inv.PullSecrets) > 0
}
