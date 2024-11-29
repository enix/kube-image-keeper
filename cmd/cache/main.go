package main

import (
	"crypto/tls"
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap/zapcore"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/enix/kube-image-keeper/internal"
	kuikController "github.com/enix/kube-image-keeper/internal/controller"
	corecontroller "github.com/enix/kube-image-keeper/internal/controller/core"
	kuikcontroller "github.com/enix/kube-image-keeper/internal/controller/kuik"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/scheme"
	webhookcorev1 "github.com/enix/kube-image-keeper/internal/webhook/core/v1"
	webhookkuikv1 "github.com/enix/kube-image-keeper/internal/webhook/kuik/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var expiryDelay uint
	var proxyPort int
	var ignoreImages internal.RegexpArrayFlags
	var acceptImages internal.RegexpArrayFlags
	var ignorePullPolicyAlways bool
	var architectures internal.ArrayFlags
	var maxConcurrentCachedImageReconciles int
	var insecureRegistries internal.ArrayFlags
	var rootCAPaths internal.ArrayFlags
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.UintVar(&expiryDelay, "expiry-delay", 30, "The delay in days before deleting an unused CachedImage.")
	flag.IntVar(&proxyPort, "proxy-port", 8082, "The port on which the registry proxy accepts connections on each host.")
	flag.Var(&ignoreImages, "ignore-images", "Regex that represents images to be excluded (this flag can be used multiple times).")
	flag.Var(&acceptImages, "accept-images", "Regex that represents images to be whitelisted (this flag can be used multiple times).")
	flag.BoolVar(&ignorePullPolicyAlways, "ignore-pull-policy-always", true, "Ignore containers that are configured with imagePullPolicy: Always")
	flag.Var(&architectures, "arch", "Architecture of image to put in cache (this flag can be used multiple times).")
	flag.StringVar(&registry.Endpoint, "registry-endpoint", "kube-image-keeper-registry:5000", "The address of the registry where cached images are stored.")
	flag.IntVar(&maxConcurrentCachedImageReconciles, "max-concurrent-cached-image-reconciles", 3, "Maximum number of CachedImages that can be handled and reconciled at the same time (put or removed from cache).")
	flag.Var(&insecureRegistries, "insecure-registries", "Insecure registries to allow to cache and proxify images from (this flag can be used multiple times).")
	flag.Var(&rootCAPaths, "root-certificate-authorities", "Root certificate authorities to trust.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")

	opts := zap.Options{
		Development:     true,
		TimeEncoder:     zapcore.ISO8601TimeEncoder,
		StacktraceLevel: zapcore.DPanicLevel,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization

		// TODO(user): If CertDir, CertName, and KeyName are not specified, controller-runtime will automatically
		// generate self-signed certificates for the metrics server. While convenient for development and testing,
		// this setup is not recommended for production.
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme.NewScheme(),
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "a046788b.kuik.enix.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	rootCAs, err := registry.LoadRootCAPoolFromFiles(rootCAPaths)
	if err != nil {
		setupLog.Error(err, "could not load root certificate authorities")
		os.Exit(1)
	}

	if err = (&kuikcontroller.CachedImageReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		Recorder:           mgr.GetEventRecorderFor("cachedimage-controller"),
		ApiReader:          mgr.GetAPIReader(),
		ExpiryDelay:        time.Duration(expiryDelay*24) * time.Hour,
		Architectures:      []string(architectures),
		InsecureRegistries: []string(insecureRegistries),
		RootCAs:            rootCAs,
	}).SetupWithManager(mgr, maxConcurrentCachedImageReconciles); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CachedImage")
		os.Exit(1)
	}
	// nolint:goconst
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err = webhookkuikv1.SetupCachedImageWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "CachedImage")
			os.Exit(1)
		}
	}

	if err = (&corecontroller.PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}
	// nolint:goconst
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		admissionDecoder := admission.NewDecoder(mgr.GetScheme())
		imageRewriter := webhookcorev1.ImageRewriter{
			Client:                 mgr.GetClient(),
			IgnoreImages:           ignoreImages,
			AcceptImages:           acceptImages,
			IgnorePullPolicyAlways: ignorePullPolicyAlways,
			ProxyPort:              proxyPort,
			Decoder:                admissionDecoder,
		}
		mgr.GetWebhookServer().Register("/mutate-core-v1-pod", &webhook.Admission{Handler: &imageRewriter})
	}

	if err = (&kuikcontroller.RepositoryReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("repository-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Repository")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	err = mgr.Add(&webhookcorev1.PodInitializer{Client: mgr.GetClient()})
	if err != nil {
		setupLog.Error(err, "unable to setup PodInitializer")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	kuikController.SetLeader(false)
	go func() {
		<-mgr.Elected()
		kuikController.SetLeader(true)
	}()

	kuikController.RegisterMetrics(mgr.GetClient())

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
