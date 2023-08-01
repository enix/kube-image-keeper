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

	kuikenixiov1 "github.com/enix/kube-image-keeper/api/v1"
	"github.com/enix/kube-image-keeper/controllers"
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
	var maxConcurrentCachedImageReconciles int
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.UintVar(&expiryDelay, "expiry-delay", 30, "The delay in days before deleting an unused CachedImage.")
	flag.IntVar(&proxyPort, "proxy-port", 8082, "The port where the proxy is listening on this machine.")
	flag.StringVar(&registry.Endpoint, "registry-endpoint", "kube-image-keeper-registry:5000", "The address of the registry where cached images are stored.")
	flag.IntVar(&maxConcurrentCachedImageReconciles, "max-concurrent-cached-image-reconciles", 3, "Maximum number of CachedImages that can be handled and reconciled at the same time (put or removed from cache).")

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

	if err = (&controllers.CachedImageReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("cachedimage-controller"),
	}).SetupWithManager(mgr, maxConcurrentCachedImageReconciles); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CachedImage")
		os.Exit(1)
	}
	if err = (&controllers.PodReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ExpiryDelay: time.Duration(expiryDelay*24) * time.Hour,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}
	imageRewriter := kuikenixiov1.ImageRewriter{
		Client:    mgr.GetClient(),
		ProxyPort: proxyPort,
	}
	mgr.GetWebhookServer().Register("/mutate-core-v1-pod", &webhook.Admission{Handler: &imageRewriter})
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", controllers.MakeChecker(controllers.Healthz)); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", controllers.MakeChecker(controllers.Readyz)); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	controllers.SetLeader(false)
	go func() {
		<-mgr.Elected()
		controllers.SetLeader(true)
	}()

	controllers.ProbeAddr = probeAddr
	controllers.RegisterMetrics(mgr.GetClient())

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
