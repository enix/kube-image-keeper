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
	"github.com/enix/kube-image-keeper/internal/registry/routing"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
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
	Routing      *routing.Routing
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

	// Ensuring ImageMonitors are created for each Images =================================================================
	var images kuikv1alpha1.ImageList
	if err := r.List(ctx, &images, client.MatchingFields{
		RegistryIndexKey: registryMonitor.Spec.Registry,
	}); err != nil {
		return ctrl.Result{}, err
	}

	areImagesUsed := map[string]bool{}
	for _, image := range images.Items {
		areImagesUsed[image.Reference()] = image.IsUsedByPods()

		registries := r.Routing.MatchingRegistries(&image.Spec.ImageReference)
		log.V(1).Info("matching registries", "registries", registries)

		for _, reg := range registries {
			imageReference := kuikv1alpha1.ImageReference{
				Registry: reg,
				Path:     image.Spec.Path,
			}

			name, err := registry.ImageNameFromReference(imageReference.Reference())
			if err != nil {
				return ctrl.Result{}, err
			}

			imageMonitor := &kuikv1alpha1.ImageMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: kuikv1alpha1.ImageMonitorSpec{
					ImageReference: imageReference,
				},
			}

			op, err := controllerutil.CreateOrPatch(ctx, r.Client, imageMonitor, func() error {
				if err := controllerutil.SetOwnerReference(&registryMonitor, imageMonitor, r.Scheme); err != nil {
					return err
				}

				if err := controllerutil.SetOwnerReference(&image, imageMonitor, r.Scheme); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			// Update status in order to fill default values
			if op == controllerutil.OperationResultCreated {
				if err := r.Status().Update(ctx, imageMonitor); err != nil {
					return ctrl.Result{}, err
				}
			}

			log.V(1).Info("ensured ImageMonitor", "operation", op, "name", imageMonitor.Name, "", imageMonitor.Reference())
		}
	}

	// Monitor pool setup =================================================================================================
	log = log.WithValues("registry", registryMonitor.Spec.Registry)
	monitorPool, ok := r.MonitorPools[registryMonitor.Name]
	if !ok {
		monitorPool = pond.NewPool(registryMonitor.Spec.Parallel)
		r.MonitorPools[registryMonitor.Name] = monitorPool
		kuikcontroller.Metrics.InitMonitoringTaskRegistry(registryMonitor.Spec.Registry)
	} else if registryMonitor.Spec.Parallel != monitorPool.MaxConcurrency() {
		log.V(1).Info("resizing monitor pool", "current", monitorPool.MaxConcurrency(), "new", registryMonitor.Spec.Parallel)
		monitorPool.Resize(registryMonitor.Spec.Parallel)
	}

	log.Info("queuing images for monitoring")

	// Preparing monitoring ===============================================================================================
	var imageMonitors kuikv1alpha1.ImageMonitorList
	if err := r.List(ctx, &imageMonitors, client.MatchingFields{
		RegistryIndexKey: registryMonitor.Spec.Registry,
	}); err != nil {
		return ctrl.Result{}, err
	}

	slices.SortFunc(imageMonitors.Items, func(i, j kuikv1alpha1.ImageMonitor) int {
		return i.Status.Upstream.LastMonitor.Compare(j.Status.Upstream.LastMonitor.Time)
	})

	monitoredDuringInterval := 0
	intervalStart := time.Now().Add(-registryMonitor.Spec.Interval.Duration)
	for _, image := range imageMonitors.Items {
		if !(image.Status.Upstream.LastMonitor.IsZero() || image.Status.Upstream.LastMonitor.Time.Before(intervalStart)) {
			monitoredDuringInterval++
		}
	}

	log.Info("found images matching registry", "count", len(imageMonitors.Items), "monitoredDuringInterval", monitoredDuringInterval)

	// Monitoring images ==================================================================================================
	for i := range min(min(registryMonitor.Spec.MaxPerInterval-monitoredDuringInterval, len(imageMonitors.Items)-monitoredDuringInterval), registryMonitor.Spec.Parallel) {
		imageMonitor := imageMonitors.Items[i]
		logImageMonitor := logf.Log.WithValues("controller", "imagemonitor", "image", klog.KObj(&imageMonitor), "reference", imageMonitor.Reference()).V(1)
		logImageMonitor.Info("queuing image for monitoring")

		task := monitorPool.Submit(func() {
			logImageMonitor.Info("monitoring image")
			if err := r.monitorAnImage(logf.IntoContext(context.Background(), logImageMonitor), registryMonitor.Spec.Method, &imageMonitor); err != nil {
				logImageMonitor.Info("failed to monitor image", "error", err.Error())
			}
			logImageMonitor.Info("image monitored with success")
			isImageUsed := areImagesUsed[imageMonitor.Reference()]
			// TODO: this should be done after task.Wait()
			kuikcontroller.Metrics.MonitoringTaskCompleted(registryMonitor.Spec.Registry, isImageUsed, &imageMonitor)
		})

		go func() {
			// TODO: rework this part, it should set the status if the tasks metric
			err := task.Wait()
			if err != nil {
				logImageMonitor.Error(err, "failed to queue image for monitoring")
			}
		}()
	}

	log.Info("queued images for monitoring with success")

	return ctrl.Result{RequeueAfter: registryMonitor.Spec.Interval.Duration / time.Duration(registryMonitor.Spec.MaxPerInterval)}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RegistryMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// create an index to list Images matching this RegistryMonitor
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kuikv1alpha1.Image{}, RegistryIndexKey, func(rawObj client.Object) []string {
		image := rawObj.(*kuikv1alpha1.Image)
		return []string{image.Spec.Registry}
	}); err != nil {
		return err
	}

	// create an index to list ImageMonitors matching this RegistryMonitor
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kuikv1alpha1.ImageMonitor{}, RegistryIndexKey, func(rawObj client.Object) []string {
		image := rawObj.(*kuikv1alpha1.ImageMonitor)
		return []string{image.Spec.Registry}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.RegistryMonitor{}).
		Named("kuik-registrymonitor").
		// We also want to react when Images are added/removed
		Watches(
			&kuikv1alpha1.Image{},
			handler.EnqueueRequestsFromMapFunc(r.mapImageToRegistryMonitors),
		).
		// ImageMonitors are children
		Owns(&kuikv1alpha1.ImageMonitor{}).
		WithOptions(controller.Options{
			// This MUST stay 1, as we are using a pool to manage concurrency per registry monitor
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

// mapImageToRegistryMonitors finds all RegistryMonitors that should care about this Image
func (r *RegistryMonitorReconciler) mapImageToRegistryMonitors(ctx context.Context, obj client.Object) []reconcile.Request {
	image, ok := obj.(*kuikv1alpha1.Image)
	if !ok {
		return nil
	}

	var registryMonitors kuikv1alpha1.RegistryMonitorList
	if err := r.List(ctx, &registryMonitors); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, registryMonitor := range registryMonitors.Items {
		if image.Spec.Registry == registryMonitor.Spec.Registry {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      registryMonitor.Name,
					Namespace: registryMonitor.Namespace,
				},
			})
		}
	}

	return requests
}

func (r *RegistryMonitorReconciler) monitorAnImage(ctx context.Context, httpMethod string, image *kuikv1alpha1.ImageMonitor) error {
	patch := client.MergeFrom(image.DeepCopy())
	image.Status.Upstream.LastMonitor = metav1.Now()
	if err := r.Status().Patch(ctx, image, patch); err != nil {
		return fmt.Errorf("failed to patch image status: %w", err)
	}

	patch = client.MergeFrom(image.DeepCopy())
	pullSecrets, pullSecretsErr := image.GetPullSecrets(ctx, r.Client)

	var lastErr error
	if desc, err := registry.ReadDescriptor(httpMethod, image.Reference(), pullSecrets, nil, nil); err != nil {
		image.Status.Upstream.LastError = err.Error()
		lastErr = err
		var te *transport.Error
		if errors.As(err, &te) {
			if te.StatusCode == http.StatusForbidden || te.StatusCode == http.StatusUnauthorized {
				if pullSecretsErr != nil {
					image.Status.Upstream.Status = kuikv1alpha1.ImageMonitorStatusUpstreamUnavailableSecret
					image.Status.Upstream.LastError = pullSecretsErr.Error()
					lastErr = pullSecretsErr
				} else {
					image.Status.Upstream.Status = kuikv1alpha1.ImageMonitorStatusUpstreamInvalidAuth
				}
			} else if te.StatusCode == http.StatusTooManyRequests {
				image.Status.Upstream.Status = kuikv1alpha1.ImageMonitorStatusUpstreamQuotaExceeded
			} else {
				image.Status.Upstream.Status = kuikv1alpha1.ImageMonitorStatusUpstreamUnavailable
			}
		} else {
			image.Status.Upstream.Status = kuikv1alpha1.ImageMonitorStatusUpstreamUnreachable
		}
	} else {
		image.Status.Upstream.LastSeen = metav1.Now()
		image.Status.Upstream.LastError = ""
		image.Status.Upstream.Status = kuikv1alpha1.ImageMonitorStatusUpstreamAvailable
		image.Status.Upstream.Digest = desc.Digest.String()
	}

	if errStatus := r.Status().Patch(ctx, image, patch); errStatus != nil {
		return fmt.Errorf("failed to patch image status: %w", errStatus)
	}

	if lastErr != nil {
		return lastErr
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
