package v1

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// nolint:unused
// log is for logging in this package.
var podlog = logf.Log.WithName("pod-resource")

// SetupPodWebhookWithManager registers the webhook for Pod in the manager.
func SetupPodWebhookWithManager(mgr ctrl.Manager, d *PodCustomDefaulter) error {
	checkCache, err := otter.MustBuilder[string, bool](1000).
		Cost(func(key string, value bool) uint32 { return 1 }).
		WithTTL(time.Second).
		Build()
	if err != nil {
		return err
	}
	alternativeCache, err := otter.MustBuilder[string, *cachedAlternativeImage](100).
		Cost(func(key string, value *cachedAlternativeImage) uint32 { return 1 }).
		WithTTL(time.Second).
		Build()
	if err != nil {
		return err
	}

	d.checkCache = checkCache
	d.alternativeCache = alternativeCache
	d.requestGroup = &singleflight.Group{}
	d.cleanupSemaphore = make(chan struct{}, d.Config.Routing.ActiveCheck.StaleMirrorCleanup.MaxConcurrent)

	return ctrl.NewWebhookManagedBy(mgr, &corev1.Pod{}).
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
	alternativeCache otter.Cache[string, *cachedAlternativeImage]
	requestGroup     *singleflight.Group
	cleanupSemaphore chan struct{}
}

type AlternativeImage struct {
	Reference        string
	CredentialSecret *kuikv1alpha1.CredentialSecret
	ImagePullSecret  *corev1.Secret
	SecretOwner      client.Object
}

type cachedAlternativeImage struct {
	*AlternativeImage
	alternativesCount int
}

type cachedAlternativeImageWithReason struct {
	*cachedAlternativeImage
	reason string
}

type Container struct {
	*corev1.Container
	IsInit          bool
	NormalizedImage string
	Images          []AlternativeImage
	Alternatives    map[string]struct{}
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

var _ admission.Defaulter[*corev1.Pod] = &PodCustomDefaulter{}

// Default implements admission.Defaulter so a webhook will be registered for the Kind Pod.
func (d *PodCustomDefaulter) Default(ctx context.Context, pod *corev1.Pod) error {
	request, _ := admission.RequestFromContext(ctx)
	log := podlog.WithValues("requestID", request.UID, "namespace", request.Namespace, "name", request.Name)

	log = log.WithValues("generateName", pod.GenerateName)

	if err := d.defaultPod(logf.IntoContext(ctx, log), pod, *request.DryRun); err != nil {
		log.Error(err, "defaulting webhook error")
		return err
	}

	return nil
}

// FIXME: split this defaultPod into smaller steps to drop the gocyclo exemption.
//
//nolint:gocyclo
func (d *PodCustomDefaulter) defaultPod(ctx context.Context, pod *corev1.Pod, dryRun bool) error {
	log := logf.FromContext(ctx)

	if _, isMirrorPod := pod.Annotations[kuikcontroller.MirrorPodAnnotation]; isMirrorPod {
		log.V(1).Info("skipping mirror pod (static pod representation), kubelet would reject mutations")
		return nil
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	originalImages := map[string]string{}
	if originalImagesStr, ok := pod.Annotations[kuikcontroller.OriginalImagesAnnotation]; ok {
		if err := json.Unmarshal([]byte(originalImagesStr), &originalImages); err != nil {
			log.Error(err, "could not unmarshal "+kuikcontroller.OriginalImagesAnnotation+" annotation")
		}
	}

	containers := make([]Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		if _, ok := originalImages[container.Name]; ok {
			continue
		}
		originalImages[container.Name] = container.Image
		containers = append(containers, Container{
			Container:    &pod.Spec.Containers[i],
			Alternatives: map[string]struct{}{},
		})
	}
	for i := range pod.Spec.InitContainers {
		name := "init:" + pod.Spec.InitContainers[i].Name
		if _, ok := originalImages[name]; ok {
			continue
		}
		containers = append(containers, Container{
			Container:    &pod.Spec.InitContainers[i],
			IsInit:       true,
			Alternatives: map[string]struct{}{},
		})
	}

	if originalImagesStr, err := json.Marshal(originalImages); err != nil {
		log.Error(err, "could not marshal "+kuikcontroller.OriginalImagesAnnotation+" annotation")
	} else {
		pod.Annotations[kuikcontroller.OriginalImagesAnnotation] = string(originalImagesStr)
	}

	if len(containers) == 0 {
		return nil // all containers have been processed already
	}

	log.V(1).Info("defaulting for Pod")

	// normalize and filter out containers with invalid, digest-based, or non-rewritable images
	for i := range containers {
		named, err := reference.ParseNormalizedNamed(containers[i].Image)
		if err == nil {
			containers[i].NormalizedImage = named.String()
		}
	}
	containers = slices.DeleteFunc(containers, func(container Container) bool {
		return container.NormalizedImage == "" || strings.Contains(container.Image, "@") || (!d.Config.Routing.RewriteOnNeverImagePullPolicy && container.ImagePullPolicy == corev1.PullNever)
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
			Status:     kuikv1alpha1.ImageSetMirrorStatus(cism.Status),
		})
	}
	for _, ism := range ismList.Items {
		for i := range ism.Spec.Mirrors {
			mirror := &ism.Spec.Mirrors[i]
			if mirror.CredentialSecret != nil {
				mirror.CredentialSecret.Namespace = ism.Namespace
			}
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
		for i := range ris.Spec.Upstreams {
			upstream := &ris.Spec.Upstreams[i]
			if upstream.CredentialSecret != nil {
				upstream.CredentialSecret.Namespace = ris.Namespace
			}
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

	podImagePullSecrets := make([]corev1.Secret, 0, len(podCredentialSecrets))
	for _, podCredentialSecret := range podCredentialSecrets {
		objectKey := client.ObjectKey{Namespace: podCredentialSecret.Namespace, Name: podCredentialSecret.Name}
		var secret corev1.Secret
		if err := d.Get(ctx, objectKey, &secret); err != nil {
			if apiErrors.IsNotFound(err) {
				log.Error(err, "pod has invalid image pull secret", "secret", objectKey)
				continue
			}
			return err
		}
		podImagePullSecrets = append(podImagePullSecrets, secret)
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

		alternativeImage, alternativesCount, reason, err := d.findBestAlternativeCached(logf.IntoContext(ctx, log), imageSetMirrors, replicatedImageSets, container, podImagePullSecrets)
		if err != nil {
			return err
		}

		log = log.WithValues("originalImage", container.Image)

		if alternativeImage == nil {
			if alternativesCount > 1 {
				log.V(1).Info("no alternative image is available, keep using the original one")
			}
			continue
		}

		if container.NormalizedImage == alternativeImage.Reference {
			log.V(1).Info("original image is available, using it")
			continue
		}

		log.Info("rerouting image", "reroutedImage", alternativeImage.Reference, "reason", reason)
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

func (d *PodCustomDefaulter) findBestAlternativeCached(ctx context.Context, imageSetMirrors []kuikv1alpha1.ImageSetMirror, replicatedImageSets []kuikv1alpha1.ReplicatedImageSet, container *Container, pullSecrets []corev1.Secret) (*AlternativeImage, int, string, error) {
	if cached, ok := d.alternativeCache.Get(container.NormalizedImage); ok {
		return cached.AlternativeImage, cached.alternativesCount, "cached", nil
	}

	result, err := d.requestGroup.Do("alternative:"+container.NormalizedImage, func() (any, error) {
		if err := d.buildAlternativesList(ctx, imageSetMirrors, replicatedImageSets, container); err != nil {
			return nil, err
		}

		alternativeImage, errs := d.findBestAlternative(ctx, container, pullSecrets)
		cachedAlternativeImage := &cachedAlternativeImage{
			AlternativeImage:  alternativeImage,
			alternativesCount: len(container.Alternatives),
		}
		d.alternativeCache.Set(container.NormalizedImage, cachedAlternativeImage)
		return &cachedAlternativeImageWithReason{
			cachedAlternativeImage: cachedAlternativeImage,
			reason:                 fmt.Sprintf("alternatives: %+v, errors:\n%v", slices.Collect(maps.Keys(container.Alternatives)), errors.Join(errs...)),
		}, nil
	})
	if err != nil {
		return nil, 0, "", err
	}

	typedResult := result.(*cachedAlternativeImageWithReason)
	return typedResult.AlternativeImage, typedResult.alternativesCount, typedResult.reason, nil
}

func (d *PodCustomDefaulter) buildAlternativesList(ctx context.Context, imageSetMirrors []kuikv1alpha1.ImageSetMirror, replicatedImageSets []kuikv1alpha1.ReplicatedImageSet, container *Container) error {
	log := logf.FromContext(ctx)
	normalizedImage := container.NormalizedImage
	alternatives := []prioritizedAlternative{}

	// Collect original image (and skip sorting when imagePullPolicy == "Always")
	if container.ImagePullPolicy == "Always" {
		container.addAlternative(normalizedImage, nil, nil)
	} else {
		alternatives = append(alternatives, prioritizedAlternative{
			reference: normalizedImage,
			typeOrder: crTypeOrderOriginal,
		})
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

	// Collect from ImageSetMirrors
	for ismIdx := range imageSetMirrors {
		ism := &imageSetMirrors[ismIdx]
		imageFilter := ism.Spec.ImageFilter.MustBuild()

		for _, alternative := range alternatives {
			imgRegistry, imgPath, err := internal.RegistryAndPathFromReference(alternative.reference)
			if err != nil {
				return err
			}

			if !imageFilter.Match(path.Join(imgRegistry, imgPath)) {
				// FIXME: if it doesn't match the filter, also check if it matches one of the mirrored images
				continue
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

func (d *PodCustomDefaulter) findBestAlternative(ctx context.Context, container *Container, pullSecrets []corev1.Secret) (*AlternativeImage, []error) {
	if len(container.Images) > 1 {
		if image, errs := parallel.FirstSuccessful(container.Images, func(image *AlternativeImage) (*AlternativeImage, error) {
			imagePullSecrets := pullSecrets
			if image.ImagePullSecret != nil {
				imagePullSecrets = append(imagePullSecrets, *image.ImagePullSecret)
			}

			return image, d.checkImageAvailabilityCached(ctx, image, imagePullSecrets)
		}); image != nil {
			return image, errs
		}
	}

	return nil, nil
}

func (d *PodCustomDefaulter) checkImageAvailabilityCached(ctx context.Context, image *AlternativeImage, pullSecrets []corev1.Secret) error {
	if result, ok := d.checkCache.Get(image.Reference); ok {
		if result {
			return nil
		}
		return errors.New("cached")
	}

	err, _ := d.requestGroup.Do("availability:"+image.Reference, func() (any, error) {
		log := logf.FromContext(ctx, "reference", image.Reference)

		result, err := registry.CheckImageAvailability(ctx, image.Reference, http.MethodHead, d.Config.Routing.ActiveCheck.Timeout, pullSecrets)
		if err != nil {
			log.V(1).Info("image is not available", "error", err)
		} else {
			log.V(1).Info("image is available")
		}

		d.checkCache.Set(image.Reference, err == nil)

		if result == kuikv1alpha1.ImageAvailabilityNotFound && image.SecretOwner != nil {
			d.tryCleanupStaleMirrorStatus(ctx, image)
		}

		return err, nil
	})

	if err != nil {
		return err.(error)
	}

	return nil
}

// tryCleanupStaleMirrorStatus attempts to launch clearStaleMirrorStatus in a
// bounded goroutine. Returns a channel that is closed when the goroutine
// finishes, or nil if the semaphore was full and the cleanup was dropped.
//
// Dropped cleanups are not retried here; the next availability check on the
// same reference will hit this path again (no positive cache entry is stored
// for NotFound results), so the status will eventually reconcile.
func (d *PodCustomDefaulter) tryCleanupStaleMirrorStatus(parentCtx context.Context, image *AlternativeImage) <-chan struct{} {
	select {
	case d.cleanupSemaphore <- struct{}{}:
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { <-d.cleanupSemaphore }()
			ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), d.Config.Routing.ActiveCheck.StaleMirrorCleanup.Timeout)
			defer cancel()
			d.clearStaleMirrorStatus(ctx, image)
		}()
		return done
	default:
		podlog.V(1).Info("cleanup semaphore full, skipping stale mirror status clear", "reference", image.Reference)
		return nil
	}
}

// clearStaleMirrorStatus clears the mirroredAt field on the ISM/CISM status entry
// matching the given mirror reference. This signals the controller to re-mirror the image.
func (d *PodCustomDefaulter) clearStaleMirrorStatus(ctx context.Context, image *AlternativeImage) {
	log := podlog.WithValues("reference", image.Reference)

	ism, ok := image.SecretOwner.(*kuikv1alpha1.ImageSetMirror)
	if !ok {
		return // secret owner is a ReplicatedImageSet
	}

	// Determine the actual CR kind: CISMs have no namespace
	var obj client.Object
	if ism.Namespace == "" {
		obj = &kuikv1alpha1.ClusterImageSetMirror{
			ObjectMeta: ism.ObjectMeta,
		}
	} else {
		obj = &kuikv1alpha1.ImageSetMirror{
			ObjectMeta: ism.ObjectMeta,
		}
	}

	gvk, err := apiutil.GVKForObject(obj, d.Scheme())
	if err != nil {
		log.Error(err, "failed to get GVK")
		return
	}

	for _, matchingImage := range ism.Status.MatchingImages {
		for _, mirror := range matchingImage.Mirrors {
			if mirror.Image != image.Reference || mirror.MirroredAt == nil {
				continue
			}

			// take ownership on mirroredAt
			if err := d.patchMirror(ctx, obj, gvk, matchingImage.Image, map[string]any{
				"image":      image.Reference,
				"mirroredAt": metav1.Time{Time: time.Now()},
			}); err != nil {
				log.Error(err, "failed to take ownership on stale mirroredAt", "owner", client.ObjectKeyFromObject(obj))
				continue
			}

			// remove mirroredAt
			if err := d.patchMirror(ctx, obj, gvk, matchingImage.Image, map[string]any{
				"image": image.Reference,
			}); err != nil {
				log.Error(err, "failed to clear stale mirroredAt", "owner", client.ObjectKeyFromObject(obj))
				continue
			}

			log.Info("cleared stale mirroredAt", "owner", client.ObjectKeyFromObject(obj), "matchingImage", matchingImage)
		}
	}
}

func (d *PodCustomDefaulter) patchMirror(ctx context.Context, obj client.Object, gvk schema.GroupVersionKind, matchingImage string, mirror map[string]any) error {
	patch := map[string]any{
		"apiVersion": gvk.Group + "/" + gvk.Version,
		"kind":       gvk.Kind,
		"metadata": map[string]any{
			"name": obj.GetName(),
		},
		"status": map[string]any{
			"matchingImages": []map[string]any{
				{
					"image":   matchingImage,
					"mirrors": []map[string]any{mirror},
				},
			},
		},
	}

	if ns := obj.GetNamespace(); ns != "" {
		patch["metadata"].(map[string]any)["namespace"] = ns
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal SSA patch: %w", err)
	}

	if err := d.Status().Patch(ctx, obj,
		client.RawPatch(apimachinerytypes.ApplyPatchType, patchData),
		client.FieldOwner("kuik-webhook"),
		client.ForceOwnership,
	); err != nil {
		return err
	}

	return nil
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
			kuikcontroller.OwnerVersionLabel: gvk.Version,
			kuikcontroller.OwnerGroupLabel:   gvk.Group,
			kuikcontroller.OwnerKindLabel:    gvk.Kind,
			kuikcontroller.OwnerUIDLabel:     ownerUID,
			kuikcontroller.OwnerNameLabel:    owner.GetName(),
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
