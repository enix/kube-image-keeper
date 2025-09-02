package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(kuikv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if err := kuikv1alpha1.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "failed to add scheme: %v\n", err)
		os.Exit(1)
	}

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}

	imgList := kuikv1alpha1.ImageList{}
	err = c.List(context.Background(), &imgList)
	if err != nil {
		setupLog.Error(err, "unable to list images")
		os.Exit(1)
	}

	registries := map[string]kuikv1alpha1.Upstream{}
	maxLen := 0
	for _, image := range imgList.Items {
		if upstream, ok := registries[image.Spec.Registry]; ok && upstream.Status == kuikv1alpha1.ImageStatusUpstreamAvailable {
			continue
		}

		monitorAnImage(context.Background(), c, &image)
		upstream := image.Status.Upstream
		fmt.Printf("[%s] %s: %s\n", upstream.Status, image.Reference(), upstream.Digest)
		if upstream.LastError != "" {
			fmt.Printf("\terror: %s\n", upstream.LastError)
		}

		registries[image.Spec.Registry] = upstream
		if maxLen < len(image.Spec.Registry) {
			maxLen = len(image.Spec.Registry)
		}
	}

	println("")

	for reg, upstream := range registries {
		status := "GOOD"
		details := fmt.Sprintf("%s: %s", upstream.Status, upstream.Digest)
		if upstream.Status != kuikv1alpha1.ImageStatusUpstreamAvailable || len(strings.Split(upstream.Digest, ":")) != 2 {
			status = "BAD "
			details = string(upstream.Status)
		}
		fmt.Printf("[%s] %-*s (%s)\n", status, maxLen, reg, details)
	}
}

func monitorAnImage(ctx context.Context, c client.Client, image *kuikv1alpha1.Image) {
	pullSecrets, pullSecretsErr := image.GetPullSecrets(ctx, c)

	if desc, err := registry.ReadDescriptor(http.MethodHead, image.Reference(), pullSecrets, nil, nil); err != nil {
		image.Status.Upstream.LastError = err.Error()
		var te *transport.Error
		if errors.As(err, &te) {
			if te.StatusCode == http.StatusForbidden || te.StatusCode == http.StatusUnauthorized {
				if pullSecretsErr != nil {
					image.Status.Upstream.Status = kuikv1alpha1.ImageStatusUpstreamUnavailableSecret
					image.Status.Upstream.LastError = pullSecretsErr.Error()
				} else {
					image.Status.Upstream.Status = kuikv1alpha1.ImageStatusUpstreamInvalidAuth
				}
			} else if te.StatusCode == http.StatusTooManyRequests {
				image.Status.Upstream.Status = kuikv1alpha1.ImageStatusUpstreamQuotaExceeded
			} else {
				image.Status.Upstream.Status = kuikv1alpha1.ImageStatusUpstreamUnavailable
			}
		} else {
			image.Status.Upstream.Status = kuikv1alpha1.ImageStatusUpstreamUnreachable
		}
	} else {
		image.Status.Upstream.LastSeen = metav1.Now()
		image.Status.Upstream.LastError = ""
		image.Status.Upstream.Status = kuikv1alpha1.ImageStatusUpstreamAvailable
		image.Status.Upstream.Digest = desc.Digest.String()
	}
}
