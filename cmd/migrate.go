package main

import (
	"flag"
	"fmt"
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/enix/kube-image-keeper/internal/migration"
)

// migrateCommand is the subcommand that guides a migration from kuik v2. It is not one of the
// three processes: it runs once, from an operator's machine, with their kubeconfig.
const migrateCommand = "migrate"

// exitUsage is the exit code of a migrate call with a bad flag, or -h: EX_USAGE of
// sysexits.h, apart from the codes of a run (migration.ExitComplete and the others).
const exitUsage = 64

// runMigrate runs the migrate subcommand with args, the arguments after its name, and returns
// its exit code.
func runMigrate(command string, args []string) int {
	// ContinueOnError: the flag package would exit 0 on -h and 2 on a bad flag, codes the run
	// gives a meaning of its own.
	fs := flag.NewFlagSet(command+" "+migrateCommand, flag.ContinueOnError)
	var stateFile, namespace string
	fs.StringVar(&stateFile, "state-file", migration.DefaultStateFile,
		"The state file, kept from the first run on: it holds what the migration needs once v2 is gone.")
	fs.StringVar(&namespace, "namespace", "kuik-system", "The namespace kuik v3 is, or will be, installed in.")
	// --kubeconfig, then KUBECONFIG, the in-cluster config and ~/.kube/config, as every
	// controller-runtime client loads its config.
	config.RegisterFlags(fs)
	opts := loggerOptions()
	opts.BindFlags(fs)
	// Parse prints the error, or the usage on -h, to stderr itself.
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg, err := ctrl.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: loading the kubeconfig: %v\n", err)
		return migration.ExitError
	}
	// Uncached: one read of each object, straight from the API server. Secrets are listed
	// by their metadata only, which the API server sends without their data.
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating the Kubernetes client: %v\n", err)
		return migration.ExitError
	}
	return migration.Run(ctrl.SetupSignalHandler(), c, migration.Options{
		StateFile: stateFile,
		Namespace: namespace,
		Out:       os.Stdout,
		Err:       os.Stderr,
	})
}
