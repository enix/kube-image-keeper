package core

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/cespare/xxhash"
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
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

	registries, errs := registriesFromPod(&pod)
	for _, err := range errs {
		log.Error(err, "failed to get registry from pod, ignoring", "pod", klog.KObj(&pod))
	}

	for _, registry := range registries {
		var registryMonitors kuikv1alpha1.RegistryMonitorList
		if err := r.List(ctx, &registryMonitors, client.MatchingFields{
			RegistryIndexKey: registry,
		}); err != nil {
			return ctrl.Result{}, err
		}
		if len(registryMonitors.Items) > 0 {
			continue
		}

		registryMonitor := &kuikv1alpha1.RegistryMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%016x", xxhash.Sum64String(registry)),
			},
			Spec: kuikv1alpha1.RegistryMonitorSpec{
				Registry: registry,
				// TODO: use default values from configuration
				MaxPerInterval: 1,
				Interval:       metav1.Duration{Duration: 10 * time.Minute},
				Method:         http.MethodHead,
				Timeout:        metav1.Duration{Duration: 10 * time.Second},
			},
		}

		if err := r.Create(ctx, registryMonitor); err != nil {
			if apiErrors.IsAlreadyExists(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		} else {
			log.Info("no registry monitor found for image, created one with default values", "registry", registry)
		}
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

	p := predicate.Not(predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			new := e.ObjectNew.(*kuikv1alpha1.RegistryMonitor)
			old := e.ObjectOld.(*kuikv1alpha1.RegistryMonitor)
			return new.Spec.Registry == old.Spec.Registry
		},
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Named("core-pod").
		Watches(
			&kuikv1alpha1.RegistryMonitor{},
			handler.EnqueueRequestsFromMapFunc(r.podsFromRegistryMonitors),
			builder.WithPredicates(p),
		).
		Complete(r)
}

func (r *PodReconciler) podsFromRegistryMonitors(ctx context.Context, obj client.Object) []ctrl.Request {
	log := logf.
		FromContext(ctx).
		WithName("controller-runtime.manager.controller.pod.watch-registrymonitors").
		WithValues("registrymonitor", klog.KObj(obj))

	registryMonitor := obj.(*kuikv1alpha1.RegistryMonitor)
	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		log.Error(err, "could not list pods")
		return nil
	}

	requests := []ctrl.Request{}
	for _, pod := range pods.Items {
		registries, errs := registriesFromPod(&pod)
		for _, err := range errs {
			log.Error(err, "failed to get registry from pod, ignoring", "pod", klog.KObj(&pod))
		}
		if slices.Index(registries, registryMonitor.Spec.Registry) != -1 {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&pod),
			})
		}
	}

	return requests
}

func registriesFromPod(pod *corev1.Pod) ([]string, []error) {
	return registriesFromContainers(append(pod.Spec.Containers, pod.Spec.InitContainers...))
}

func registriesFromContainers(containers []corev1.Container) ([]string, []error) {
	registries := []string{}
	errs := []error{}

	for _, container := range containers {
		registry, _, err := internal.RegistryAndPathFromReference(container.Image)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not parse registry from reference %q: %w", container.Image, err))
		}
		registries = append(registries, registry)
	}

	return registries, errs
}
