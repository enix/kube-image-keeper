package kuik

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/alitto/pond/v2"
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	kuikcontroller "github.com/enix/kube-image-keeper/internal/controller"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	RegistryIndexKey = ".spec.registry"
)

// RegistryMonitorReconciler reconciles a RegistryMonitor object
type RegistryMonitorReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	MonitorPools map[string]pond.Pool
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=registrymonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=registrymonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=registrymonitors/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RegistryMonitor object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *RegistryMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var registryMonitor kuikv1alpha1.RegistryMonitor
	if err := r.Get(ctx, req.NamespacedName, &registryMonitor); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log = log.WithValues("registry", registryMonitor.Spec.Registry)
	monitorPool, ok := r.MonitorPools[registryMonitor.Name]
	if !ok {
		monitorPool = pond.NewPool(registryMonitor.Spec.Parallel)
		r.MonitorPools[registryMonitor.Name] = monitorPool
	} else if registryMonitor.Spec.Parallel != monitorPool.MaxConcurrency() {
		log.V(1).Info("resizing monitor pool", "current", monitorPool.MaxConcurrency(), "new", registryMonitor.Spec.Parallel)
		monitorPool.Resize(registryMonitor.Spec.Parallel)
	}

	log.Info("queuing images for monitoring")

	var images kuikv1alpha1.ImageList
	if err := r.List(ctx, &images, client.MatchingFields{
		RegistryIndexKey: registryMonitor.Spec.Registry,
	}); err != nil {
		return ctrl.Result{}, err
	}

	slices.SortFunc(images.Items, func(i, j kuikv1alpha1.Image) int {
		return i.Status.Upstream.LastMonitor.Compare(j.Status.Upstream.LastMonitor.Time)
	})

	monitoredDuringInterval := 0
	intervalStart := time.Now().Add(-registryMonitor.Spec.Interval.Duration)
	for _, image := range images.Items {
		if !(image.Status.Upstream.LastMonitor.IsZero() || image.Status.Upstream.LastMonitor.Time.Before(intervalStart)) {
			monitoredDuringInterval++
		}
	}

	log.Info("found images matching registry", "count", len(images.Items), "monitoredDuringInterval", monitoredDuringInterval)

	patch := client.MergeFrom(registryMonitor.DeepCopy())
	registryMonitor.Status.RegistryStatus = kuikv1alpha1.RegistryStatusUp
	err := registry.HealthCheck(registryMonitor.Spec.Registry, nil, nil)
	if err != nil {
		registryMonitor.Status.RegistryStatus = kuikv1alpha1.RegistryStatusDown
	}

	if errStatus := r.Status().Patch(ctx, &registryMonitor, patch); errStatus != nil {
		return reconcile.Result{}, fmt.Errorf("failed to patch registrymonitor status: %w", errStatus)
	}

	if err != nil {
		return reconcile.Result{}, fmt.Errorf("registry seems to be down, skipping monitoring of images: %w", err)
	}

	for i := range min(min(registryMonitor.Spec.MaxPerInterval-monitoredDuringInterval, registryMonitor.Spec.Parallel), len(images.Items)) {
		image := images.Items[i]
		logImage := logf.Log.WithValues("controller", "imagemonitor", "image", klog.KObj(&image), "reference", image.Reference()).V(1)
		logImage.Info("queuing image for monitoring")

		err := monitorPool.Go(func() {
			logImage.Info("monitoring image")
			if err := r.monitorAnImage(logf.IntoContext(context.Background(), logImage), &image); err != nil {
				logImage.Info("failed to monitor image", "error", err.Error())
			}
			logImage.Info("image monitored with success")
			kuikcontroller.Metrics.MonitoringTaskCompleted(registryMonitor.Name, image.Status.Upstream.Status)
		})
		if err != nil {
			logImage.Error(err, "failed to queue image for monitoring")
		}
	}

	log.Info("queued images for monitoring with success", "completed", monitorPool.CompletedTasks(), "failed", monitorPool.FailedTasks())

	return ctrl.Result{RequeueAfter: registryMonitor.Spec.Interval.Duration / time.Duration(registryMonitor.Spec.MaxPerInterval)}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RegistryMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// create an index to list Images by registry
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kuikv1alpha1.Image{}, RegistryIndexKey, func(rawObj client.Object) []string {
		image := rawObj.(*kuikv1alpha1.Image)

		return []string{image.Spec.Registry}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.RegistryMonitor{}).
		Named("kuik-registrymonitor").
		WithOptions(controller.Options{
			// This MUST stay 1, as we are using a pool to manage concurrency per registry monitor
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

func (r *RegistryMonitorReconciler) monitorAnImage(ctx context.Context, image *kuikv1alpha1.Image) error {
	patch := client.MergeFrom(image.DeepCopy())
	image.Status.Upstream.LastMonitor = metav1.Now()
	if err := r.Status().Patch(ctx, image, patch); err != nil {
		return fmt.Errorf("failed to patch image status: %w", err)
	}

	patch = client.MergeFrom(image.DeepCopy())

	desc, err := registry.GetDescriptor(image.Reference(), nil, nil)
	if err != nil {
		image.Status.Upstream.LastError = err.Error()
		var te *transport.Error
		if errors.As(err, &te) {
			if te.StatusCode == http.StatusForbidden || te.StatusCode == http.StatusUnauthorized {
				image.Status.Upstream.Status = kuikv1alpha1.ImageStatusUpstreamInvalidAuth
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

	if errStatus := r.Status().Patch(ctx, image, patch); errStatus != nil {
		return fmt.Errorf("failed to patch image status: %w", errStatus)
	}

	if err != nil {
		return err
	}

	return nil

	// TODO: add to SetupWithManager Watches(&source.Channel{Source: eventChannel}, &handler.EnqueueRequestForObject{})
	// push an event in the channel when this function is done
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
