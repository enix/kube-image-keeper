package main

import (
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap/zapcore"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kuikenixiov1 "github.com/enix/kube-image-keeper/api/core/v1"
	kuikv1alpha1ext1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1ext1"
	"github.com/enix/kube-image-keeper/internal"
	kuikController "github.com/enix/kube-image-keeper/internal/controller"
	"github.com/enix/kube-image-keeper/internal/controller/core"
	"github.com/enix/kube-image-keeper/internal/controller/kuik"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/scheme"
	//+kubebuilder:scaffold:imports
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var expiryDelay uint
	var proxyPort int
	var ignoreImages internal.RegexpArrayFlags
	var ignorePullPolicyAlways bool
	var architectures internal.ArrayFlags
	var maxConcurrentCachedImageReconciles int
	var insecureRegistries internal.ArrayFlags
	var rootCAPaths internal.ArrayFlags
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.UintVar(&expiryDelay, "expiry-delay", 30, "The delay in days before deleting an unused CachedImage.")
	flag.IntVar(&proxyPort, "proxy-port", 8082, "The port on which the registry proxy accepts connections on each host.")
	flag.Var(&ignoreImages, "ignore-images", "Regex that represents images to be excluded (this flag can be used multiple times).")
	flag.BoolVar(&ignorePullPolicyAlways, "ignore-pull-policy-always", true, "Ignore containers that are configured with imagePullPolicy: Always")
	flag.Var(&architectures, "arch", "Architecture of image to put in cache (this flag can be used multiple times).")
	flag.StringVar(&registry.Endpoint, "registry-endpoint", "kube-image-keeper-registry:5000", "The address of the registry where cached images are stored.")
	flag.IntVar(&maxConcurrentCachedImageReconciles, "max-concurrent-cached-image-reconciles", 3, "Maximum number of CachedImages that can be handled and reconciled at the same time (put or removed from cache).")
	flag.Var(&insecureRegistries, "insecure-registries", "Insecure registries to allow to cache and proxify images from (this flag can be used multiple times).")
	flag.Var(&rootCAPaths, "root-certificate-authorities", "Root certificate authorities to trust.")

	opts := zap.Options{
		Development:     true,
		TimeEncoder:     zapcore.ISO8601TimeEncoder,
		StacktraceLevel: zapcore.DPanicLevel,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme.NewScheme(),
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "a046788b.kuik.enix.io",
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

	if err = (&kuik.CachedImageReconciler{
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
	if err = (&core.PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}
	imageRewriter := kuikenixiov1.ImageRewriter{
		Client:                 mgr.GetClient(),
		IgnoreImages:           ignoreImages,
		IgnorePullPolicyAlways: ignorePullPolicyAlways,
		ProxyPort:              proxyPort,
		Decoder:                admission.NewDecoder(mgr.GetScheme()),
	}
	mgr.GetWebhookServer().Register("/mutate-core-v1-pod", &webhook.Admission{Handler: &imageRewriter})
	if err = (&kuikv1alpha1ext1.CachedImage{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "CachedImage")
		os.Exit(1)
	}
	if err = (&kuik.RepositoryReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("epository-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Repository")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	err = mgr.Add(&kuikenixiov1.PodInitializer{Client: mgr.GetClient()})
	if err != nil {
		setupLog.Error(err, "unable to setup PodInitializer")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", kuikController.MakeChecker(kuikController.Healthz)); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", kuikController.MakeChecker(kuikController.Readyz)); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	kuikController.SetLeader(false)
	go func() {
		<-mgr.Elected()
		kuikController.SetLeader(true)
	}()

	kuikController.ProbeAddr = probeAddr
	kuikController.RegisterMetrics(mgr.GetClient())

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
