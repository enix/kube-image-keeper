//go:build smoke

// Package smoke is the smoke test of a kuik release on a cluster where kuik is already
// installed, possibly a shared one running real workloads. It never installs, upgrades or
// configures kuik, and never touches an object it did not create: see guard_test.go.
// Run it through the tasks smoke-check (TestInstall), smoke-run (TestRouting) and
// smoke-cleanup, see .claude/skills/preprod-smoke:
//
//	go test -tags smoke ./test/smoke/ -count=1 -v -run '^TestInstall$' -args \
//	  --kubeconfig PATH --context NAME [--kuik-namespace NS] [--release NAME] [--kuik-version V]
//
// TestInstall only reads. TestRouting refuses to start while resources of an earlier run are
// left, creates its own in the test namespaces and deletes them, unless a test failed: they
// are then kept for the diagnosis until --cleanup-only removes them.
package smoke

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// e2e-framework owns --kubeconfig, --context and --namespace, hence the kuik- prefixes.
var (
	kuikNamespace = flag.String("kuik-namespace", "kuik-system", "namespace kuik is installed in")
	release       = flag.String("release", "kube-image-keeper", "Helm release of kuik")
	kuikVersion   = flag.String("kuik-version", "", "expected kuik version, for example 3.0.0-alpha.3")
	privateRepo   = flag.String("private-repo", "",
		"private repository holding nginx-unprivileged:1.31.6-alpine, for the real pull")
	pullAuth = flag.String("pull-auth", "",
		"docker config Secret in the kuik namespace that can pull --private-repo; the user's, never deleted")
	cleanupOnly = flag.Bool("cleanup-only", false, "delete the resources an earlier run left, then stop")

	testenv env.Environment
	// clientset reaches what klient does not cover: the pod proxy for the metrics, the logs,
	// the events.k8s.io events, discovery.
	clientset kubernetes.Interface
)

func TestMain(m *testing.M) {
	cfg, err := envconf.NewFromFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := pinContext(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *cleanupOnly {
		if err := cleanup(context.Background(), cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("no test resource left")
		os.Exit(0)
	}
	testenv = env.NewWithConfig(cfg)
	os.Exit(testenv.Run(m))
}

// pinContext builds the client from the --kubeconfig and --context given on the command
// line, and nothing else. e2e-framework alone falls back on the current context, KUBECONFIG,
// ~/.kube/config or the in-cluster config: on a shared cluster, a run must never land on a
// cluster nobody named.
func pinContext(cfg *envconf.Config) error {
	kubeconfig, kubeContext := cfg.KubeconfigFile(), cfg.KubeContext()
	if kubeconfig == "" || kubeContext == "" {
		return fmt.Errorf("--kubeconfig and --context are required")
	}
	raw, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", kubeconfig, err)
	}
	if _, ok := raw.Contexts[kubeContext]; !ok {
		return fmt.Errorf("context %s not found in %s", kubeContext, kubeconfig)
	}
	overrides := &clientcmd.ConfigOverrides{}
	restConfig, err := clientcmd.NewNonInteractiveClientConfig(*raw, kubeContext, overrides, nil).ClientConfig()
	if err != nil {
		return fmt.Errorf("context %s: %w", kubeContext, err)
	}
	client, err := klient.New(restConfig)
	if err != nil {
		return err
	}
	if err := kuikv1alpha1.AddToScheme(client.Resources().GetScheme()); err != nil {
		return err
	}
	if clientset, err = kubernetes.NewForConfig(restConfig); err != nil {
		return err
	}
	cfg.WithClient(client)
	return nil
}
