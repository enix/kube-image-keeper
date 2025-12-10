package kuik

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	RegistryIndexKey = ".spec.registry"
)

// RegistryMonitorReconciler reconciles a RegistryMonitor object
type RegistryMonitorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config *config.Config
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=registrymonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=registrymonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=registrymonitors/finalizers,verbs=update

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors/finalizers,verbs=update

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

	if !r.Config.Monitoring.Enabled {
		return ctrl.Result{}, nil
	}

	log = log.WithValues("registry", registryMonitor.Spec.Registry)

	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return ctrl.Result{}, err
	}

	// Init monitored images statuses =====================================================================================
	matchingImagesMap := imagesMatchingRegistryFromPods(ctx, registryMonitor.Spec.Registry, pods.Items)
	imageMonitorStatusMap := map[string]kuikv1alpha1.ImageMonitorStatus{}
	for image := range matchingImagesMap {
		imageMonitorStatusMap[image] = kuikv1alpha1.ImageMonitorStatus{Path: image}
	}

	for _, imageMonitor := range registryMonitor.Status.Images {
		if _, ok := matchingImagesMap[imageMonitor.Path]; !ok {
			imageMonitor.UnusedSince = metav1.NewTime(time.Now())
		} else {
			imageMonitor.UnusedSince = metav1.NewTime(time.Time{})
		}
		imageMonitorStatusMap[imageMonitor.Path] = imageMonitor
	}

	originalRegistryMonitor := registryMonitor.DeepCopy()
	registryMonitor.Status.Images = []kuikv1alpha1.ImageMonitorStatus{}
	for _, imageMonitor := range imageMonitorStatusMap {
		registryMonitor.Status.Images = append(registryMonitor.Status.Images, imageMonitor)
	}

	if err := r.Status().Patch(ctx, &registryMonitor, client.MergeFrom(originalRegistryMonitor)); err != nil {
		return ctrl.Result{}, err
	}

	// Preparing monitoring ===============================================================================================
	if len(registryMonitor.Status.Images) == 0 {
		return ctrl.Result{}, nil
	}

	monitoredDuringInterval := 0
	inUseImages := []*kuikv1alpha1.ImageMonitorStatus{}
	intervalStart := time.Now().Add(-registryMonitor.Spec.Interval.Duration)
	for i := range registryMonitor.Status.Images {
		image := &registryMonitor.Status.Images[i]
		if !(image.LastMonitor.IsZero() || image.LastMonitor.Time.Before(intervalStart)) {
			monitoredDuringInterval++
		}
		if image.UnusedSince.IsZero() {
			inUseImages = append(inUseImages, image)
		}
	}

	if len(inUseImages) == 0 {
		log.V(1).Info("no in-use image found, skipping monitoring")
		return ctrl.Result{}, nil
	}

	requeueAfter := registryMonitor.Spec.Interval.Duration / time.Duration(registryMonitor.Spec.MaxPerInterval)
	if monitoredDuringInterval >= registryMonitor.Spec.MaxPerInterval {
		log.Info("max per interval reached, retrying later", "requeueAfter", requeueAfter)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// Monitoring images ==================================================================================================
	log.V(1).Info("monitoring images", "count", len(registryMonitor.Status.Images), "monitoredDuringInterval", monitoredDuringInterval)

	imageMonitorStatus := inUseImages[0]
	for i := range inUseImages {
		ims := inUseImages[i]
		if ims.UnusedSince.IsZero() && ims.LastMonitor.Before(&imageMonitorStatus.LastMonitor) {
			imageMonitorStatus = ims
		}
	}

	logImageMonitor := log.WithValues("path", imageMonitorStatus.Path)
	logImageMonitor.Info("monitoring image")
	println("================", imageMonitorStatus.Path, imageMonitorStatus.UnusedSince.String())

	// FIXME: uncomment this
	// kuikcontroller.Metrics.InitMonitoringTaskRegistry(registryMonitor.Spec.Registry)

	if err := r.MonitorImage(logf.IntoContext(ctx, logImageMonitor), &registryMonitor, imageMonitorStatus, matchingImagesMap[imageMonitorStatus.Path]); err != nil {
		logImageMonitor.Error(err, "failed to monitor image")
	} else {
		logImageMonitor.V(1).Info("image monitored with success")
	}

	// FIXME: uncomment this
	// isImageUsed := areImagesUsed[imageMonitorStatus.Reference()]
	// kuikcontroller.Metrics.MonitoringTaskCompleted(registryMonitor.Spec.Registry, isImageUsed, &imageMonitorStatus)

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RegistryMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.RegistryMonitor{}).
		Named("kuik-registrymonitor").
		// Prevent reconciliation on status update
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

func (r *RegistryMonitorReconciler) MonitorImage(ctx context.Context, registryMonitor *kuikv1alpha1.RegistryMonitor, imageMonitorStatus *kuikv1alpha1.ImageMonitorStatus, pod *corev1.Pod) (err error) {
	patch := client.MergeFrom(registryMonitor.DeepCopy())
	defer func() {
		if errStatus := r.Status().Patch(ctx, registryMonitor, patch); errStatus != nil {
			err = fmt.Errorf("failed to patch image status: %w", errStatus)
		}
	}()

	imageMonitorStatus.LastMonitor = metav1.Now()

	pullSecrets, pullSecretsErr := internal.GetPullSecretsFromPod(ctx, r.Client, pod)
	client := registry.NewClient(nil, nil).WithTimeout(registryMonitor.Spec.Timeout.Duration).WithPullSecrets(pullSecrets)

	desc, err := client.ReadDescriptor(registryMonitor.Spec.Method, path.Join(registryMonitor.Spec.Registry, imageMonitorStatus.Path))
	if err != nil {
		imageMonitorStatus.LastError = err.Error()
		var te *transport.Error
		if errors.As(err, &te) {
			switch te.StatusCode {
			case http.StatusForbidden, http.StatusUnauthorized:
				if pullSecretsErr != nil {
					imageMonitorStatus.Status = kuikv1alpha1.ImageMonitorStatusUpstreamUnavailableSecret
					imageMonitorStatus.LastError = pullSecretsErr.Error()
					err = pullSecretsErr
				} else {
					imageMonitorStatus.Status = kuikv1alpha1.ImageMonitorStatusUpstreamInvalidAuth
				}
			case http.StatusTooManyRequests:
				imageMonitorStatus.Status = kuikv1alpha1.ImageMonitorStatusUpstreamQuotaExceeded
			default:
				imageMonitorStatus.Status = kuikv1alpha1.ImageMonitorStatusUpstreamUnavailable
			}
		} else {
			imageMonitorStatus.Status = kuikv1alpha1.ImageMonitorStatusUpstreamUnreachable
		}
	} else {
		imageMonitorStatus.LastSeen = metav1.Now()
		imageMonitorStatus.LastError = ""
		imageMonitorStatus.Status = kuikv1alpha1.ImageMonitorStatusUpstreamAvailable
		imageMonitorStatus.Digest = desc.Digest.String()
	}

	return err
}

func imagesMatchingRegistryFromPod(registry string, pod *corev1.Pod) ([]string, []error) {
	return imagesMatchingRegistryFromContainers(registry, append(pod.Spec.Containers, pod.Spec.InitContainers...))
}

func imagesMatchingRegistryFromContainers(registry string, containers []corev1.Container) ([]string, []error) {
	images := []string{}
	errs := []error{}

	for _, container := range containers {
		imageRegistry, path, err := internal.RegistryAndPathFromReference(container.Image)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not parse registry from reference %q: %w", container.Image, err))
		}
		if imageRegistry == registry {
			images = append(images, path)
		}
	}

	return images, errs
}

func imagesMatchingRegistryFromPods(ctx context.Context, registry string, pods []corev1.Pod) map[string]*corev1.Pod {
	log := logf.FromContext(ctx)
	matchingImagesMap := map[string]*corev1.Pod{}

	for _, pod := range pods {
		images, errs := imagesMatchingRegistryFromPod(registry, &pod)
		for _, err := range errs {
			log.Error(err, "failed to get images matching registry from pod, ignoring", "pod", klog.KObj(&pod))
		}
		for _, image := range images {
			matchingImagesMap[image] = &pod
		}
	}

	return matchingImagesMap
}
