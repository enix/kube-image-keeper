package core

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	RegistryIndexKey = ".spec.registry"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	defaultRegistryMonitorSpec kuikv1alpha1.RegistryMonitorSpec
}

// +kubebuilder:rbac:groups=core,resources=pods;secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Pod object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	images, errs := kuikv1alpha1.ImagesFromPod(&pod)
	for _, err := range errs {
		log.Error(err, "failed to create image from pod, ignoring", "pod", klog.KObj(&pod))
	}

	hasDeletingImages := false
	for _, image := range images {
		var img kuikv1alpha1.Image
		err := r.Get(ctx, client.ObjectKeyFromObject(&image), &img)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if !img.DeletionTimestamp.IsZero() {
			hasDeletingImages = true
			// image is already scheduled for deletion, thus we don't have to handle it here and will enqueue it back later
			log.Info("image is already being deleted, skipping", "image", klog.KObj(&image))
			continue
		}

		var registryMonitors kuikv1alpha1.RegistryMonitorList
		if err := r.List(ctx, &registryMonitors, client.MatchingFields{
			RegistryIndexKey: image.Spec.Registry,
		}); err != nil {
			return ctrl.Result{}, err
		}
		if len(registryMonitors.Items) == 0 {
			log.Info("no registry monitor found for image, creating one with default values", "image", klog.KObj(&image), "registry", image.Spec.Registry)
			spec := r.defaultRegistryMonitorSpec.DeepCopy()
			spec.Registry = image.Spec.Registry
			err := r.Create(ctx, &kuikv1alpha1.RegistryMonitor{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: fmt.Sprintf("%016x-", xxhash.Sum64String(spec.Registry)),
				},
				Spec: *spec,
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		// create or update Image depending on weather it already exists or not
		if apierrors.IsNotFound(err) {
			log.Info("new image found on a pod", "image", klog.KObj(&image))
			err = r.Create(ctx, &image)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else if img.Reference() != image.Reference() {
			log.Info("image found with an invalid reference, patching it", "image", klog.KObj(&image))

			patch := client.MergeFrom(img.DeepCopy())

			img.Spec.Registry = image.Spec.Registry
			img.Spec.Image = image.Spec.Image

			if err = r.Patch(ctx, &img, patch); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	if hasDeletingImages {
		log.Info("some images are being deleted, requeuing later")
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// create an index to list RegistryMonitors by registry
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kuikv1alpha1.RegistryMonitor{}, RegistryIndexKey, func(rawObj client.Object) []string {
		image := rawObj.(*kuikv1alpha1.RegistryMonitor)

		return []string{image.Spec.Registry}
	}); err != nil {
		return err
	}

	r.defaultRegistryMonitorSpec = kuikv1alpha1.RegistryMonitorSpec{
		Interval:       metav1.Duration{Duration: 10 * time.Minute},
		MaxPerInterval: 1,
		Parallel:       1,
	}

	if env := os.Getenv("KUIK_REGISTRY_MONITOR_DEFAULT_INTERVAL"); env != "" {
		interval, err := time.ParseDuration(env)
		if err != nil {
			return err
		}
		r.defaultRegistryMonitorSpec.Interval = metav1.Duration{Duration: interval}
	}
	if env := os.Getenv("KUIK_REGISTRY_MONITOR_DEFAULT_MAX_PER_INTERVAL"); env != "" {
		maxPerInterval, err := strconv.Atoi(env)
		if err != nil {
			return err
		}
		r.defaultRegistryMonitorSpec.MaxPerInterval = maxPerInterval
	}
	if env := os.Getenv("KUIK_REGISTRY_MONITOR_DEFAULT_PARALLEL"); env != "" {
		parallel, err := strconv.Atoi(env)
		if err != nil {
			return err
		}
		r.defaultRegistryMonitorSpec.Parallel = parallel
	}

	p := predicate.Not(predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newImage := e.ObjectNew.(*kuikv1alpha1.Image)
			oldImage := e.ObjectOld.(*kuikv1alpha1.Image)
			return newImage.Reference() == oldImage.Reference()
		},
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Named("core-pod").
		Watches(
			&kuikv1alpha1.Image{},
			handler.EnqueueRequestsFromMapFunc(r.podsWithDeletingImages),
			builder.WithPredicates(p),
		).
		Complete(r)
}

func (r *PodReconciler) podsWithDeletingImages(ctx context.Context, obj client.Object) []ctrl.Request {
	log := logf.
		FromContext(ctx).
		WithName("controller-runtime.manager.controller.pod.deletingImages").
		WithValues("image", klog.KObj(obj))

	image := obj.(*kuikv1alpha1.Image)
	res := make([]ctrl.Request, len(image.Status.UsedByPods.Items))

	for i, pod := range image.Status.UsedByPods.Items {
		log.Info("image in use, reconciling related pod", "pod", pod)
		name := strings.SplitN(pod, "/", 2)
		res[i].NamespacedName = client.ObjectKey{Namespace: name[0], Name: name[1]}
	}

	return res
}
