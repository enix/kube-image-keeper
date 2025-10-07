package v1

import (
	"context"
	"fmt"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/routing"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// nolint:unused
// log is for logging in this package.
var podlog = logf.Log.WithName("pod-resource")

// SetupPodWebhookWithManager registers the webhook for Pod in the manager.
func SetupPodWebhookWithManager(mgr ctrl.Manager, d *PodCustomDefaulter) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&corev1.Pod{}).
		WithDefaulter(d).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=mpod-v1.kb.io,admissionReviewVersions=v1

// PodCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Pod when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type PodCustomDefaulter struct {
	client.Client
	Routing *routing.Routing
}

var _ webhook.CustomDefaulter = &PodCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Pod.
func (d *PodCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	request, _ := admission.RequestFromContext(ctx)
	log := podlog.WithValues("requestID", request.UID, "namespace", request.Namespace, "name", request.Name)

	pod, ok := obj.(*corev1.Pod)

	if !ok {
		return fmt.Errorf("expected an Pod object but got %T", obj)
	}

	log.Info("defaulting for Pod")

	d.RerouteImages(logf.IntoContext(ctx, log), pod)

	return nil
}

func (d *PodCustomDefaulter) RerouteImages(ctx context.Context, pod *corev1.Pod) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}

	// Handle Containers
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		d.handleContainer(ctx, container, false)
	}

	// Handle init containers
	for i := range pod.Spec.InitContainers {
		container := &pod.Spec.InitContainers[i]
		d.handleContainer(ctx, container, true)
	}
}

func (d *PodCustomDefaulter) handleContainer(ctx context.Context, container *corev1.Container, initContainer bool) {
	log := logf.FromContext(ctx)
	registry, path, err := registry.RegistryNameFromReference(container.Image)
	if err != nil {
		return
	}

	imageReference := kuikv1alpha1.NewImageReference(registry, path)

	status, err := d.getImageStatus(ctx, container.Image)
	if err != nil {
		log.Error(err, "could not get image status", "image", container.Image)
		return
	}
	if status == kuikv1alpha1.ImageMonitorStatusUpstreamAvailable {
		return
	}

	matchingStrategy := d.Routing.Match(imageReference)
	if matchingStrategy == nil {
		return
	}

	for _, reg := range matchingStrategy.Registries {
		if reg == registry {
			continue // don't check the same registry twice
		}
		alternativeRef := reg + "/" + imageReference.Path
		alternativeStatus, err := d.getImageStatus(ctx, alternativeRef)
		if err != nil {
			log.Error(err, "could not get image status", "image", alternativeRef)
			continue
		}

		if alternativeStatus == kuikv1alpha1.ImageMonitorStatusUpstreamAvailable {
			log.Info("rerouting image", "container", container.Name, "isInit", initContainer, "originalImage", container.Image, "reroutedImage", alternativeRef)
			container.Image = alternativeRef
			return
		}
	}
}

func (d *PodCustomDefaulter) getImageStatus(ctx context.Context, reference string) (kuikv1alpha1.ImageMonitorStatusUpstream, error) {
	name, err := registry.ImageNameFromReference(reference)
	if err != nil {
		return kuikv1alpha1.ImageMonitorStatusUpstream(""), err
	}

	var imageMonitor kuikv1alpha1.ImageMonitor
	if err := d.Get(ctx, types.NamespacedName{Name: name}, &imageMonitor); err != nil {
		return kuikv1alpha1.ImageMonitorStatusUpstream(""), err
	}

	if d.Routing.ActiveCheck.Enabled {
		registryMonitor, err := imageMonitor.GetRegistryMonitor(ctx, d.Client)
		if err != nil {
			return kuikv1alpha1.ImageMonitorStatusUpstream(""), err
		}

		_ = imageMonitor.Monitor(ctx, d.Client, registryMonitor.Spec.Method, d.Routing.ActiveCheck.Timeout)
	}

	return imageMonitor.Status.Upstream.Status, nil
}
