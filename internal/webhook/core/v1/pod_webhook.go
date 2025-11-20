package v1

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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

// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=ignore,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=mpod-v1.kb.io,admissionReviewVersions=v1

// PodCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Pod when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type PodCustomDefaulter struct {
	client.Client
	Config *config.Config
}

type AlternativeImage struct {
	Reference string
}

type Container struct {
	*corev1.Container
	IsInit       bool
	Images       []AlternativeImage
	Alternatives map[string]struct{}
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

	containers := make([]Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	for i := range pod.Spec.Containers {
		containers = append(containers, Container{
			Container:    &pod.Spec.Containers[i],
			Alternatives: map[string]struct{}{},
		})
	}
	for i := range pod.Spec.InitContainers {
		containers = append(containers, Container{
			Container:    &pod.Spec.InitContainers[i],
			IsInit:       true,
			Alternatives: map[string]struct{}{},
		})
	}

	var cismList kuikv1alpha1.ClusterImageSetMirrorList
	if err := d.List(ctx, &cismList); err != nil {
		return err
	}
	var ismList kuikv1alpha1.ImageSetMirrorList
	if err := d.List(ctx, &ismList); err != nil {
		return err
	}
	var crisList kuikv1alpha1.ClusterReplicatedImageSetList
	if err := d.List(ctx, &crisList); err != nil {
		return err
	}
	var risList kuikv1alpha1.ReplicatedImageSetList
	if err := d.List(ctx, &risList); err != nil {
		return err
	}

	imageSetMirrors := make([]kuikv1alpha1.ImageSetMirror, 0, len(cismList.Items))
	for _, cism := range cismList.Items {
		imageSetMirrors = append(imageSetMirrors, kuikv1alpha1.ImageSetMirror{
			ObjectMeta: cism.ObjectMeta,
			Spec:       kuikv1alpha1.ImageSetMirrorSpec(cism.Spec),
		})
	}
	for _, ism := range ismList.Items {
		if ism.Namespace != pod.Namespace {
			continue
		}
		imageSetMirrors = append(imageSetMirrors, ism)
	}

	replicatedImageSets := make([]kuikv1alpha1.ReplicatedImageSet, 0, len(crisList.Items))
	for _, cris := range crisList.Items {
		replicatedImageSets = append(replicatedImageSets, kuikv1alpha1.ReplicatedImageSet{
			ObjectMeta: cris.ObjectMeta,
			Spec:       kuikv1alpha1.ReplicatedImageSetSpec(cris.Spec),
		})
	}
	for _, ris := range risList.Items {
		if ris.Namespace != pod.Namespace {
			continue
		}
		replicatedImageSets = append(replicatedImageSets, ris)
	}

	podCredentialSecrets := make([]*kuikv1alpha1.CredentialSecret, 0, len(pod.Spec.ImagePullSecrets))
	for _, imagePullSecret := range pod.Spec.ImagePullSecrets {
		podCredentialSecrets = append(podCredentialSecrets, &kuikv1alpha1.CredentialSecret{
			Namespace: pod.Namespace,
			Name:      imagePullSecret.Name,
		})
	}

	podImagePullSecrets := make([]corev1.Secret, len(podCredentialSecrets))
	for i, podCredentialSecret := range podCredentialSecrets {
		objectKey := client.ObjectKey{Namespace: podCredentialSecret.Namespace, Name: podCredentialSecret.Name}
		if err := d.Get(ctx, objectKey, &podImagePullSecrets[i]); err != nil {
			if apiErrors.IsNotFound(err) {
				log.Error(err, "pod has invalid image pull secret", "secret", objectKey)
			} else {
				return err
			}
		}
	}

	for _, ism := range imageSetMirrors {
		matcher := regexp.MustCompile(ism.Spec.ImageMatcher)

		for i := range containers {
			container := &containers[i]
			if !matcher.MatchString(container.Image) {
				continue
			}

			container.Images = make([]AlternativeImage, 0, 1+len(ism.Spec.Mirrors))
			container.addAlternative(container.Image)

			_, imgPath, err := internal.RegistryAndPathFromReference(container.Image)
			if err != nil {
				return err
			}

			for _, mirror := range ism.Spec.Mirrors {
				container.addAlternative(path.Join(mirror.Registry, mirror.Path, imgPath))
			}
		}
	}

	for _, container := range containers {
		for _, ris := range replicatedImageSets {
			index := slices.IndexFunc(ris.Spec.Upstreams, func(upstream kuikv1alpha1.ReplicatedUpstream) bool {
				// TODO: use a validating admission policy to ensure the regexp is valid
				matcher := regexp.MustCompile(upstream.ImageMatcher)
				return matcher.MatchString(container.Image)
			})
			if index == -1 {
				continue
			}

			match := &ris.Spec.Upstreams[index]
			prefix := path.Join(match.Registry, match.Path)
			suffix := strings.TrimPrefix(container.Image, prefix)

			for _, upstream := range ris.Spec.Upstreams {
				reference := path.Join(upstream.Registry, upstream.Path, suffix)
				// TODO: handle using CredentialSecret from upstream configuration
				container.addAlternative(reference)
			}
		}

		d.rerouteContainerImage(ctx, &container, podImagePullSecrets)
	}

	return nil
}

func (d *PodCustomDefaulter) rerouteContainerImage(ctx context.Context, container *Container, pullSecrets []corev1.Secret) {
	log := logf.FromContext(ctx)

	for _, image := range container.Images {
		if available, err := d.checkImageAvailability(ctx, image.Reference, pullSecrets); err != nil {
			log.Error(err, "could not check image availability", "image", image.Reference)
			continue
		} else if available {
			if container.Image != image.Reference {
				log.Info("rerouting image", "container", container.Name, "isInit", container.IsInit, "originalImage", container.Image, "reroutedImage", image.Reference)
				container.Image = image.Reference
			}
			return
		}
	}
}

func (d *PodCustomDefaulter) checkImageAvailability(ctx context.Context, reference string, pullSecrets []corev1.Secret) (bool, error) {
	log := logf.FromContext(ctx, "reference", reference)

	registryMonitorName, err := internal.RegistryMonitorNameFromReference(reference)
	if err != nil {
		return false, err
	}

	var registryMonitor kuikv1alpha1.RegistryMonitor
	if err := d.Get(ctx, client.ObjectKey{Name: registryMonitorName}, &registryMonitor); err != nil {
		return false, err
	}

	_, err = registry.NewClient(nil, nil).
		WithTimeout(d.Config.ActiveCheck.Timeout).
		WithPullSecrets(pullSecrets).
		ReadDescriptor(registryMonitor.Spec.Method, reference)
	if err != nil {
		log.V(1).Info("image is not available", "error", err)
	}

	return err == nil, nil
}

func (c *Container) addAlternative(reference string) {
	if _, ok := c.Alternatives[reference]; ok {
		return
	}

	c.Alternatives[reference] = struct{}{}
	c.Images = append(c.Images, AlternativeImage{Reference: reference})
}
