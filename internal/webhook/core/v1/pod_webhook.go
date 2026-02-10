package v1

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/cespare/xxhash"
	"github.com/distribution/reference"
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	kuikcontroller "github.com/enix/kube-image-keeper/internal/controller/kuik"
	"github.com/enix/kube-image-keeper/internal/parallel"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/maypok86/otter"
	"go4.org/syncutil/singleflight"
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

var errRateLimited = errors.New("registry limit reached")

// SetupPodWebhookWithManager registers the webhook for Pod in the manager.
func SetupPodWebhookWithManager(mgr ctrl.Manager, d *PodCustomDefaulter) error {
	checkCache, err := otter.MustBuilder[string, bool](1000).
		Cost(func(key string, value bool) uint32 { return 1 }).
		WithTTL(time.Second).
		Build()
	if err != nil {
		return err
	}
	alternativeCache, err := otter.MustBuilder[string, *AlternativeImage](100).
		Cost(func(key string, value *AlternativeImage) uint32 { return 1 }).
		WithTTL(time.Second).
		Build()
	if err != nil {
		return err
	}

	d.checkCache = checkCache
	d.alternativeCache = alternativeCache
	d.requestGroup = &singleflight.Group{}

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

	checkCache       otter.Cache[string, bool]
	alternativeCache otter.Cache[string, *AlternativeImage]
	requestGroup     *singleflight.Group
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

// crTypeOrder represents the default ordering of CR types when priorities are equal.
type crTypeOrder int

const (
	crTypeOrderOriginal crTypeOrder = iota
	crTypeOrderCISM
	crTypeOrderISM
	crTypeOrderCRIS
	crTypeOrderRIS
)

// prioritizedAlternative holds an alternative image reference along with
// the metadata needed to sort it according to the two-level priority system.
type prioritizedAlternative struct {
	reference        string
	credentialSecret *kuikv1alpha1.CredentialSecret
	secretOwner      client.Object
	crPriority       int         // from spec.priority (signed, default 0)
	intraPriority    uint        // from mirror/upstream priority (unsigned, default 0)
	typeOrder        crTypeOrder // default type ordering
	declarationOrder int         // YAML declaration index within CR
}

// compareAlternatives defines the sort order for prioritized alternatives.
// Sort key: (crPriority asc, typeOrder asc, intraPriority asc, declOrder asc).
func compareAlternatives(a, b prioritizedAlternative) int {
	return cmp.Or(
		cmp.Compare(a.crPriority, b.crPriority),
		cmp.Compare(a.typeOrder, b.typeOrder),
		cmp.Compare(a.intraPriority, b.intraPriority),
		cmp.Compare(a.declarationOrder, b.declarationOrder),
	)
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

	log = log.WithValues("generateName", pod.GenerateName)

	if err := d.defaultPod(logf.IntoContext(ctx, log), pod, *request.DryRun); err != nil {
		log.Error(err, "defaulting webhook error")
		return err
	}

	return nil
}

func (d *PodCustomDefaulter) defaultPod(ctx context.Context, pod *corev1.Pod, dryRun bool) error {
	log := logf.FromContext(ctx)

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	processedContainersMap := map[string]struct{}{}
	if processedContainers, ok := pod.Annotations["kuik.enix.io/processed-containers"]; ok {
		for name := range strings.SplitSeq(processedContainers, ",") {
			processedContainersMap[name] = struct{}{}
		}
	}

	processedContainers := make([]string, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	containers := make([]Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	for i := range pod.Spec.Containers {
		name := pod.Spec.Containers[i].Name
		processedContainers = append(processedContainers, name)
		if _, ok := processedContainersMap[name]; ok {
			continue
		}
		containers = append(containers, Container{
			Container:    &pod.Spec.Containers[i],
			Alternatives: map[string]struct{}{},
		})
	}
	for i := range pod.Spec.InitContainers {
		name := "init:" + pod.Spec.InitContainers[i].Name
		processedContainers = append(processedContainers, name)
		if _, ok := processedContainersMap[name]; ok {
			continue
		}
		containers = append(containers, Container{
			Container:    &pod.Spec.InitContainers[i],
			IsInit:       true,
			Alternatives: map[string]struct{}{},
		})
	}
	pod.Annotations["kuik.enix.io/processed-containers"] = strings.Join(processedContainers, ",")

	if len(containers) == 0 {
		return nil // all containers have been processed already
	}

	log.V(1).Info("defaulting for Pod")

	// filter containers using invalid or digest-based images
	containers = slices.DeleteFunc(containers, func(container Container) bool {
		_, err := reference.Parse(container.Image)
		return err != nil || strings.Contains(container.Image, "@")
	})
	if len(containers) == 0 {
		log.V(1).Info("pod has no containers eligible for image rewriting, ignoring (digest-based images are not supported)")
		return nil
	}

	var cismList kuikv1alpha1.ClusterImageSetMirrorList
	if err := d.List(ctx, &cismList); err != nil {
		return err
	}
	var ismList kuikv1alpha1.ImageSetMirrorList
	if err := d.List(ctx, &ismList, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
		return err
	}
	var crisList kuikv1alpha1.ClusterReplicatedImageSetList
	if err := d.List(ctx, &crisList); err != nil {
		return err
	}
	var risList kuikv1alpha1.ReplicatedImageSetList
	if err := d.List(ctx, &risList, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
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
	replicatedImageSets = append(replicatedImageSets, risList.Items...)

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

	for i := range containers {
		container := &containers[i]
		log := log.WithValues("container", container.Name, "isInit", container.IsInit)

		alternativeImage, err := d.findBestAlternativeCached(logf.IntoContext(ctx, log), imageSetMirrors, replicatedImageSets, container, podImagePullSecrets)
		if err != nil {
			return err
		}

		log = log.WithValues("originalImage", container.Image)

		if alternativeImage == nil {
			log.Info("no alternative image is available, keep using the original one")
			continue
		}

		originalNamed, _ := reference.ParseNormalizedNamed(container.Image)
		if originalNamed.String() == alternativeImage.Reference {
			log.V(1).Info("original image is available, using it")
			continue
		}

		log.Info("rerouting image", "reroutedImage", alternativeImage.Reference)
		container.Image = alternativeImage.Reference

		if alternativeImage.ImagePullSecret != nil {
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

func (d *PodCustomDefaulter) findBestAlternativeCached(ctx context.Context, imageSetMirrors []kuikv1alpha1.ImageSetMirror, replicatedImageSets []kuikv1alpha1.ReplicatedImageSet, container *Container, pullSecrets []corev1.Secret) (*AlternativeImage, error) {
	if alternativeImage, ok := d.alternativeCache.Get(container.Image); ok {
		return alternativeImage, nil
	}

	alternativeImage, err := d.requestGroup.Do("alternative:"+container.Image, func() (any, error) {
		if err := d.buildAlternativesList(ctx, imageSetMirrors, replicatedImageSets, container); err != nil {
			return nil, err
		}

		alternativeImage := d.findBestAlternative(ctx, container, pullSecrets)
		d.alternativeCache.Set(container.Image, alternativeImage)
		return alternativeImage, nil
	})

	return alternativeImage.(*AlternativeImage), err
}

func (d *PodCustomDefaulter) buildAlternativesList(ctx context.Context, imageSetMirrors []kuikv1alpha1.ImageSetMirror, replicatedImageSets []kuikv1alpha1.ReplicatedImageSet, container *Container) error {
	log := logf.FromContext(ctx)

	named, _ := reference.ParseNormalizedNamed(container.Image)
	normalizedImage := named.String()

	alternatives := []prioritizedAlternative{
		{
			reference: normalizedImage,
			typeOrder: crTypeOrderOriginal,
		},
	}

	// Collect from ImageSetMirrors
	for ismIdx := range imageSetMirrors {
		ism := &imageSetMirrors[ismIdx]
		imageFilter := ism.Spec.ImageFilter.MustBuild()

		if !imageFilter.Match(normalizedImage) {
			// FIXME: if it doesn't match the filter, also check if it matches one of the mirrored images
			continue
		}

		_, imgPath, err := internal.RegistryAndPathFromReference(container.Image)
		if err != nil {
			return err
		}

		typeOrder := crTypeOrderISM
		if ism.Namespace == "" {
			typeOrder = crTypeOrderCISM
		}

		for declarationIdx, mirror := range ism.Spec.Mirrors {
			alternatives = append(alternatives, prioritizedAlternative{
				reference:        path.Join(mirror.Registry, mirror.Path, imgPath),
				credentialSecret: mirror.CredentialSecret,
				secretOwner:      ism,
				crPriority:       ism.Spec.Priority,
				intraPriority:    mirror.Priority,
				typeOrder:        typeOrder,
				declarationOrder: declarationIdx,
			})
		}
	}

	// Collect from ReplicatedImageSets
	for risIdx := range replicatedImageSets {
		ris := &replicatedImageSets[risIdx]
		index := slices.IndexFunc(ris.Spec.Upstreams, func(upstream kuikv1alpha1.ReplicatedUpstream) (match bool) {
			// TODO: use a validating admission policy to ensure the regexp is valid
			return upstream.ImageFilter.MustBuildWithRegistry(upstream.Registry).Match(normalizedImage)
		})
		if index == -1 {
			continue
		}

		match := &ris.Spec.Upstreams[index]
		prefix := path.Join(match.Registry, match.Path)
		suffix := strings.TrimPrefix(normalizedImage, prefix)

		typeOrder := crTypeOrderRIS
		if ris.Namespace == "" {
			typeOrder = crTypeOrderCRIS
		}

		for declarationIdx, upstream := range ris.Spec.Upstreams {
			alternatives = append(alternatives, prioritizedAlternative{
				reference:        path.Join(upstream.Registry, upstream.Path) + suffix,
				credentialSecret: upstream.CredentialSecret,
				secretOwner:      ris,
				crPriority:       ris.Spec.Priority,
				intraPriority:    upstream.Priority,
				typeOrder:        typeOrder,
				declarationOrder: declarationIdx,
			})
		}
	}

	// Stable sort by priority
	slices.SortStableFunc(alternatives, compareAlternatives)

	for _, alt := range alternatives {
		container.addAlternative(alt.reference, alt.credentialSecret, alt.secretOwner)
	}

	if err := container.loadAlternativesSecrets(ctx, d.Client); err != nil {
		return err
	}

	alternativeReferences := []string{}
	for _, image := range container.Images {
		alternativeReferences = append(alternativeReferences, image.Reference)
	}
	log.V(1).Info("found alternatives", "references", alternativeReferences)

	return nil
}

func (d *PodCustomDefaulter) findBestAlternative(ctx context.Context, container *Container, pullSecrets []corev1.Secret) *AlternativeImage {
	if len(container.Images) > 1 {
		if image := parallel.FirstSuccessful(container.Images, func(image *AlternativeImage) (*AlternativeImage, bool) {
			imagePullSecrets := pullSecrets
			if image.ImagePullSecret != nil {
				imagePullSecrets = append(imagePullSecrets, *image.ImagePullSecret)
			}

			if d.checkImageAvailabilityCached(ctx, image.Reference, imagePullSecrets) {
				return image, true
			}

			return nil, false
		}); image != nil {
			return image
		}
	}

	return nil
}

func (d *PodCustomDefaulter) checkImageAvailabilityCached(ctx context.Context, reference string, pullSecrets []corev1.Secret) bool {
	if result, ok := d.checkCache.Get(reference); ok {
		return result
	}

	available, _ := d.requestGroup.Do("availability:"+reference, func() (any, error) {
		available := d.checkImageAvailability(ctx, reference, pullSecrets)
		d.checkCache.Set(reference, available)
		return available, nil
	})

	return available.(bool)
}

func (d *PodCustomDefaulter) checkImageAvailability(ctx context.Context, reference string, pullSecrets []corev1.Secret) bool {
	log := logf.FromContext(ctx, "reference", reference)

	// registryMonitorName, err := internal.RegistryMonitorNameFromReference(reference)
	// if err != nil {
	// 	return false, err
	// }

	// var registryMonitor kuikv1alpha1.RegistryMonitor
	// if err := d.Get(ctx, client.ObjectKey{Name: registryMonitorName}, &registryMonitor); err != nil {
	// 	return false, err
	// }

	_, headers, err := registry.NewClient(nil, nil).
		WithTimeout(d.Config.Routing.ActiveCheck.Timeout).
		WithPullSecrets(pullSecrets).
		ReadDescriptor(http.MethodHead, reference)
		// ReadDescriptor(registryMonitor.Spec.Method, reference)

	if isRateLimited(headers) {
		err = errRateLimited
	}

	if err != nil {
		log.V(1).Info("image is not available", "error", err)
	} else {
		log.V(1).Info("image is available")
	}

	return err == nil
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

func isRateLimited(headers http.Header) bool {
	return strings.HasPrefix(headers.Get("ratelimit-remaining"), "0;")
}
