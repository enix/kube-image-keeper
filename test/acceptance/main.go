// Command acceptance drives the acceptance test of a kuik release on a cluster where kuik is
// already installed, possibly a shared one running real workloads. It acts and collects; it
// never judges: the agent running the release-acceptance skill reads what it prints, explores
// the cluster and decides whether kuik did what the spec says. It never installs, upgrades or
// configures kuik, and never touches an object it did not create (see guard.go).
//
//	acceptance check   --kubeconfig PATH --context NAME [--kuik-version V]   reads the installation
//	acceptance inspect --kubeconfig PATH --context NAME [--since D] [--grep RE] reads the test objects
//	acceptance setup   --kubeconfig PATH --context NAME [--kuik-version V] [--private-repo R --pull-auth S]
//	                   [--mirror-prefix P]
//	acceptance apply   --kubeconfig PATH --context NAME --file F [--private-repo R --pull-auth S]
//	                   [--mirror-prefix P]
//	acceptance delete  --kubeconfig PATH --context NAME [--namespace NS] KIND/NAME
//	acceptance cleanup --kubeconfig PATH --context NAME
//	acceptance validate                                                     checks the fixtures, no cluster
//
// check and inspect only read. setup applies the fixtures of fixtures/; setup, apply, delete and
// cleanup write test objects only. validate reaches no cluster. Run them through the
// acceptance-* tasks, see README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// env is what every subcommand works with: the clients pinned to the context given, and the
// flags common to all.
type env struct {
	kubeconfig, kubeContext string
	kuikNamespace, release  string
	client                  client.Client
	// clientset reaches what the controller-runtime client does not: logs, the pod proxy for
	// the metrics, events.
	clientset kubernetes.Interface
	scheme    *runtime.Scheme
	// pullAuth is the user's --pull-auth, the one Secret outside the test names a test CR may read.
	pullAuth string
	// mirrorPrefix is the registry path the user dedicates to test mirrors, on top of .invalid hosts.
	mirrorPrefix string
}

type command struct {
	usage string
	// flags adds the flags of the subcommand; run gets its positional arguments.
	flags func(*flag.FlagSet)
	run   func(context.Context, *env, []string) error
	// args is the number of positional arguments run takes.
	args int
	// offline runs without a cluster: env holds the scheme and the flags, no client. Only a
	// subcommand that never reads nor writes the cluster sets it.
	offline bool
}

var commands = map[string]*command{
	"check":   checkCommand(),
	"inspect": inspectCommand(),
	"setup":   setupCommand(),
	"apply":   applyCommand(),
	"delete":  deleteCommand(),
	"cleanup": {usage: "delete every test object, then check none is left", run: runCleanup},
	"validate": {
		usage: "decode every fixture with example values, run the guard on its objects and check its since line; " +
			"reaches no cluster",
		run:     runValidate,
		offline: true,
	},
}

func main() {
	if len(os.Args) < 2 || commands[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr,
			"usage: acceptance check|inspect|setup|apply|delete|cleanup --kubeconfig PATH --context NAME ...\n"+
				"       acceptance validate")
		os.Exit(2)
	}
	name, cmd := os.Args[1], commands[os.Args[1]]
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	e := &env{}
	fs.StringVar(&e.kubeconfig, "kubeconfig", "", "kubeconfig file (required)")
	fs.StringVar(&e.kubeContext, "context", "", "context of the kubeconfig (required)")
	fs.StringVar(&e.kuikNamespace, "kuik-namespace", "kuik-system", "namespace kuik is installed in")
	fs.StringVar(&e.release, "release", "kube-image-keeper", "Helm release of kuik")
	fs.StringVar(&e.mirrorPrefix, "mirror-prefix", "",
		"registry path dedicated to test ImageMirrors, where they may write; otherwise only .invalid hosts")
	if cmd.flags != nil {
		cmd.flags(fs)
	}
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "acceptance %s: %s\n", name, cmd.usage)
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[2:])
	if fs.NArg() != cmd.args {
		fmt.Fprintf(os.Stderr, "acceptance %s takes %d argument(s) after its flags, got %q\n", name, cmd.args, fs.Args())
		os.Exit(2)
	}
	connect := e.pin
	if cmd.offline {
		connect = e.offline
	}
	if err := connect(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := cmd.run(context.Background(), e, fs.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// pin builds the clients from --kubeconfig and --context only. Left to the defaults,
// client-go falls back on the current context, KUBECONFIG, ~/.kube/config or the in-cluster
// config: on a shared cluster, a run must never land on a cluster nobody named.
func (e *env) pin() error {
	if e.kubeconfig == "" || e.kubeContext == "" {
		return fmt.Errorf("--kubeconfig and --context are required")
	}
	raw, err := clientcmd.LoadFromFile(e.kubeconfig)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", e.kubeconfig, err)
	}
	if _, ok := raw.Contexts[e.kubeContext]; !ok {
		return fmt.Errorf("context %s not found in %s", e.kubeContext, e.kubeconfig)
	}
	clientConfig := clientcmd.NewNonInteractiveClientConfig(*raw, e.kubeContext, &clientcmd.ConfigOverrides{}, nil)
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("context %s: %w", e.kubeContext, err)
	}
	if err := e.offline(); err != nil {
		return err
	}
	if e.client, err = client.New(restConfig, client.Options{Scheme: e.scheme}); err != nil {
		return err
	}
	e.clientset, err = kubernetes.NewForConfig(restConfig)
	return err
}

// offline builds the scheme the fixtures decode with, and no client: what validate needs.
func (e *env) offline() error {
	e.scheme = runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(e.scheme); err != nil {
		return err
	}
	return kuikv1alpha1.AddToScheme(e.scheme)
}
