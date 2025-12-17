package v1

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/cespare/xxhash"
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	kuikcontroller "github.com/enix/kube-image-keeper/internal/controller/kuik"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
// +kubebuilder:rbac:groups=kuik.enix.io,resources=replicatedimagesets;clusterreplicatedimagesets,verbs=get;list;watch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=replicatedimagesets/status;clusterreplicatedimagesets/status,verbs=get

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
	Reference        string
	CredentialSecret *kuikv1alpha1.CredentialSecret
	ImagePullSecret  *corev1.Secret
	SecretOwner      client.Object
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
		return fmt.Errorf("expected a Pod object but got %T", obj)
	}

	if err := d.defaultPod(logf.IntoContext(ctx, log), pod, *request.DryRun); err != nil {
		log.Error(err, "defaulting webhook error")
		return err
	}

	return nil
}

func (d *PodCustomDefaulter) defaultPod(ctx context.Context, pod *corev1.Pod, dryRun bool) error {
	log := logf.FromContext(ctx)
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
		for i := range ism.Spec.Mirrors {
			mirror := &ism.Spec.Mirrors[i]
			mirror.CredentialSecret.Namespace = pod.Namespace
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

	log.V(1).Info("reviewing alternatives",
		"clusterImageSetMirrors", len(cismList.Items),
		"imageSetMirrors", len(imageSetMirrors)-len(cismList.Items),
		"clusterReplicatedImageSet", len(crisList.Items),
		"replicatedImageSet", len(replicatedImageSets)-len(crisList.Items),
		"podImagePullSecrets", len(podCredentialSecrets),
	)

	if err := d.buildAlternativesList(logf.IntoContext(ctx, log), imageSetMirrors, replicatedImageSets, containers); err != nil {
		return err
	}

	for i := range containers {
		container := &containers[i]
		if alternativeImage := d.rerouteContainerImage(ctx, container, podImagePullSecrets); alternativeImage != nil {
			if alternativeImage == nil || alternativeImage.ImagePullSecret == nil {
				continue
			}

			if !dryRun {
				if err := d.ensureSecret(ctx, pod.Namespace, alternativeImage); err != nil {
					return err
				}
			}

			// Inject rerouted image pull secret if not already present in the pod
			containsAlternativeSecret := slices.ContainsFunc(pod.Spec.ImagePullSecrets, func(localObjectReference corev1.LocalObjectReference) bool {
				return localObjectReference.Name == alternativeImage.ImagePullSecret.Name
			})
			if !containsAlternativeSecret {
				pod.Spec.ImagePullSecrets = append(pod.Spec.ImagePullSecrets, corev1.LocalObjectReference{
					Name: alternativeImage.ImagePullSecret.Name,
				})
			}
		}
	}

	return nil
}

func (d *PodCustomDefaulter) buildAlternativesList(ctx context.Context, imageSetMirrors []kuikv1alpha1.ImageSetMirror, replicatedImageSets []kuikv1alpha1.ReplicatedImageSet, containers []Container) error {
	log := logf.FromContext(ctx)

	for _, ism := range imageSetMirrors {
		imageFilter := ism.Spec.ImageFilter.MustBuild()

		for i := range containers {
			container := &containers[i]
			if !internal.MatchNormalized(imageFilter, container.Image) {
				continue
			}

			container.Images = make([]AlternativeImage, 0, 1+len(ism.Spec.Mirrors))
			container.addAlternative(container.Image, nil, nil)

			_, imgPath, err := internal.RegistryAndPathFromReference(container.Image)
			if err != nil {
				return err
			}

			for _, mirror := range ism.Spec.Mirrors {
				ism.GetObjectKind()
				container.addAlternative(path.Join(mirror.Registry, mirror.Path, imgPath), mirror.CredentialSecret, &ism)
			}
		}
	}

	for i := range containers {
		container := &containers[i]
		for _, ris := range replicatedImageSets {
			index := slices.IndexFunc(ris.Spec.Upstreams, func(upstream kuikv1alpha1.ReplicatedUpstream) bool {
				// TODO: use a validating admission policy to ensure the regexp is valid
				return internal.MatchNormalized(upstream.ImageFilter.MustBuildWithRegistry(upstream.Registry), container.Image)
			})
			if index == -1 {
				continue
			}

			match := &ris.Spec.Upstreams[index]
			prefix := path.Join(match.Registry, match.Path)
			suffix := strings.TrimPrefix(container.Image, prefix)

			for _, upstream := range ris.Spec.Upstreams {
				reference := path.Join(upstream.Registry, upstream.Path) + suffix
				container.addAlternative(reference, upstream.CredentialSecret, &ris)
			}
		}

		if err := container.loadAlternativesSecrets(ctx, d.Client); err != nil {
			return err
		}

		alternativeReferences := []string{}
		for _, image := range container.Images {
			alternativeReferences = append(alternativeReferences, image.Reference)
		}
		log.V(1).Info("found alternatives", "references", alternativeReferences)
	}

	return nil
}

func (d *PodCustomDefaulter) rerouteContainerImage(ctx context.Context, container *Container, pullSecrets []corev1.Secret) *AlternativeImage {
	log := logf.FromContext(ctx)

	for _, image := range container.Images {
		imagePullSecrets := pullSecrets
		if image.ImagePullSecret != nil {
			imagePullSecrets = append(imagePullSecrets, *image.ImagePullSecret)
		}

		if available, err := d.checkImageAvailability(ctx, image.Reference, imagePullSecrets); err != nil {
			log.Error(err, "could not check image availability", "image", image.Reference)
			continue
		} else if available {
			if container.Image != image.Reference {
				log.Info("rerouting image", "container", container.Name, "isInit", container.IsInit, "originalImage", container.Image, "reroutedImage", image.Reference)
				container.Image = image.Reference
			}
			return &image
		}
	}

	log.Info("no alternative image is available, keep using the default one")

	return nil
}

func (d *PodCustomDefaulter) checkImageAvailability(ctx context.Context, reference string, pullSecrets []corev1.Secret) (bool, error) {
	log := logf.FromContext(ctx, "reference", reference)

	// registryMonitorName, err := internal.RegistryMonitorNameFromReference(reference)
	// if err != nil {
	// 	return false, err
	// }

	// var registryMonitor kuikv1alpha1.RegistryMonitor
	// if err := d.Get(ctx, client.ObjectKey{Name: registryMonitorName}, &registryMonitor); err != nil {
	// 	return false, err
	// }

	_, err := registry.NewClient(nil, nil).
		WithTimeout(d.Config.ActiveCheck.Timeout).
		WithPullSecrets(pullSecrets).
		ReadDescriptor(http.MethodHead, reference)
		// ReadDescriptor(registryMonitor.Spec.Method, reference)
	if err != nil {
		log.V(1).Info("image is not available", "error", err)
	} else {
		log.V(1).Info("image is available")
	}

	return err == nil, nil
}

func (d *PodCustomDefaulter) ensureSecret(ctx context.Context, namespace string, alternativeImage *AlternativeImage) error {
	secret := alternativeImage.ImagePullSecret
	owner := alternativeImage.SecretOwner

	// We don't need to recreate the secret if it is already in the right namespace
	if namespace == secret.Namespace {
		return nil
	}

	ownerUID := string(owner.GetUID())
	target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		// Create a unique secret per source namespace + Cluster[ImageSetMirror,ReplicatedImageSet]
		Name:      makeSecretName("kuik", secret.Name, secret.Namespace+"/"+ownerUID),
		Namespace: namespace,
	}}

	gvk, err := apiutil.GVKForObject(owner, d.Scheme())
	if err != nil {
		return err
	}

	_, err = controllerutil.CreateOrUpdate(ctx, d.Client, target, func() error {
		target.Data = secret.Data
		target.Type = secret.Type
		target.Labels = map[string]string{
			"kuik.enix.io/owner-version": gvk.Version,
			"kuik.enix.io/owner-group":   gvk.Group,
			"kuik.enix.io/owner-kind":    gvk.Kind,
			kuikcontroller.OwnerUIDLabel: ownerUID,
			"kuik.enix.io/owner-name":    owner.GetName(),
		}
		return nil
	})

	alternativeImage.ImagePullSecret = target

	return err
}

func (c *Container) addAlternative(reference string, credentialSecret *kuikv1alpha1.CredentialSecret, secretOwner client.Object) {
	if _, ok := c.Alternatives[reference]; ok {
		return
	}

	c.Alternatives[reference] = struct{}{}
	c.Images = append(c.Images, AlternativeImage{
		Reference:        reference,
		CredentialSecret: credentialSecret,
		SecretOwner:      secretOwner,
	})
}

func (c *Container) loadAlternativesSecrets(ctx context.Context, cl client.Client) error {
	for i := range c.Images {
		image := &c.Images[i]
		if image.CredentialSecret == nil || image.ImagePullSecret != nil {
			continue
		}
		objectKey := client.ObjectKey{Namespace: image.CredentialSecret.Namespace, Name: image.CredentialSecret.Name}
		image.ImagePullSecret = &corev1.Secret{}
		if err := cl.Get(ctx, objectKey, image.ImagePullSecret); err != nil {
			return err
		}
	}
	return nil
}

func makeSecretName(prefix, name, hashInput string) string {
	nameLength := min(253-len(prefix)-16-2, len(name))
	return fmt.Sprintf("%s-%s-%016x", prefix, name[:nameLength], xxhash.Sum64String(hashInput))
}
