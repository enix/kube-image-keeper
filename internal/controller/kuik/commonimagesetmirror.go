package kuik

import (
	"context"
	"encoding/json"
	"iter"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/distribution/reference"
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/filter"
	"github.com/enix/kube-image-keeper/internal/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ImageSetMirrorBaseReconciler provides a base for building ImageSetMirror and ClusterImageSetMirror reconciliers
type ImageSetMirrorBaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Config   *config.Config
	Recorder events.EventRecorder

	platforms       []v1.Platform
	globalPodFilter filter.PodFilter
}

// compileGlobalPodFilter compiles a pod filter from cfg.SkipLabels and cfg.SkipAnnotations.
// It returns the compiled filter, or an error if compilation fails.
func compileGlobalPodFilter(cfg *config.Config) (filter.PodFilter, error) {
	f, err := filter.CompilePodFilter(nil, cfg.SkipLabels, nil, cfg.SkipAnnotations)
	if err != nil {
		return filter.PodFilter{}, err
	}
	return *f, nil
}

func (r *ImageSetMirrorBaseReconciler) setupGlobalPodFilter() error {
	f, err := compileGlobalPodFilter(r.Config)
	if err != nil {
		return err
	}
	r.globalPodFilter = f
	return nil
}

func (r *ImageSetMirrorBaseReconciler) setupPlatforms() {
	r.platforms = make([]v1.Platform, len(r.Config.Mirroring.Platforms))
	for i, p := range r.Config.Mirroring.Platforms {
		r.platforms[i] = v1.Platform{
			OS:           p.OS,
			Architecture: p.Architecture,
			Variant:      p.Variant,
		}
	}
}

func (r *ImageSetMirrorBaseReconciler) getPullSecret(ctx context.Context, namespace, name string, secret *corev1.Secret) error {
	secretReference := client.ObjectKey{Namespace: namespace, Name: name}
	if err := r.Get(ctx, secretReference, secret); err != nil {
		return err
	}
	return nil
}

func (r *ImageSetMirrorBaseReconciler) getPullSecretsFromPods(ctx context.Context, podsByMatchingImages map[string]*corev1.Pod, image string) ([]corev1.Secret, error) {
	var secrets []corev1.Secret

	if pod, ok := podsByMatchingImages[image]; ok {
		secrets = make([]corev1.Secret, len(pod.Spec.ImagePullSecrets))

		for i, imagePullSecret := range pod.Spec.ImagePullSecrets {
			if err := r.getPullSecret(ctx, pod.Namespace, imagePullSecret.Name, &secrets[i]); err != nil {
				return nil, err
			}
		}
	}

	return secrets, nil
}

// matchUpstreamCredentialSecret returns the CredentialSecret of the first
// ReplicatedUpstream whose ImageFilter (compiled with its registry) matches
// image, or nil if none matches. This mirrors how the pod webhook resolves
// the upstream source credential in buildAlternativesList, so the mirror
// controller authenticates to a private upstream exactly like the webhook
// does when it checks availability.
func matchUpstreamCredentialSecret(upstreams []kuikv1alpha1.ReplicatedUpstream, image string) *kuikv1alpha1.CredentialSecret {
	for i := range upstreams {
		upstream := &upstreams[i]
		if upstream.CredentialSecret == nil {
			continue
		}
		if upstream.ImageFilter.MustBuildWithRegistry(upstream.Registry).Match(image) {
			return upstream.CredentialSecret
		}
	}
	return nil
}

// getSourceSecretFromReplicatedImageSets resolves the SOURCE (upstream)
// credential for the image being mirrored from the CredentialSecret declared
// on a matching (Cluster)ReplicatedImageSet upstream.
//
// This closes the gap that breaks mirroring of private upstreams (issue #606):
// getPullSecretsFromPods can only contribute credentials while a pod still
// references the original image, but the webhook rewrites consumers to pull
// from the mirror at admission time, so in steady state no pod carries the
// original reference and srcSecrets ends up empty. The upstream credential the
// operator already declared on the (Cluster)ReplicatedImageSet — described as
// "the secret used to pull matching images" — is the natural source of truth.
//
// ClusterReplicatedImageSet (cluster-scoped) is always consulted. For a
// namespaced ImageSetMirror, ReplicatedImageSets in its namespace are also
// considered, with the secret resolved in that namespace.
func (r *ImageSetMirrorBaseReconciler) getSourceSecretFromReplicatedImageSets(ctx context.Context, image, namespace string) (*corev1.Secret, error) {
	var credentialSecret *kuikv1alpha1.CredentialSecret

	var crisList kuikv1alpha1.ClusterReplicatedImageSetList
	if err := r.List(ctx, &crisList); err != nil {
		return nil, err
	}
	for i := range crisList.Items {
		if cs := matchUpstreamCredentialSecret(crisList.Items[i].Spec.Upstreams, image); cs != nil {
			credentialSecret = cs
			break
		}
	}

	if credentialSecret == nil && namespace != "" {
		var risList kuikv1alpha1.ReplicatedImageSetList
		if err := r.List(ctx, &risList, client.InNamespace(namespace)); err != nil {
			return nil, err
		}
		for i := range risList.Items {
			ris := &risList.Items[i]
			if cs := matchUpstreamCredentialSecret(ris.Spec.Upstreams, image); cs != nil {
				// CredentialSecret.Namespace is ignored for namespaced
				// resources; the secret lives in the RIS' own namespace.
				qualified := *cs
				qualified.Namespace = ris.Namespace
				credentialSecret = &qualified
				break
			}
		}
	}

	if credentialSecret == nil {
		return nil, nil
	}

	secret := &corev1.Secret{}
	if err := r.getPullSecret(ctx, credentialSecret.Namespace, credentialSecret.Name, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func (r *ImageSetMirrorBaseReconciler) getImageSecretFromMirrors(ctx context.Context, image, namespace string, mirrors kuikv1alpha1.Mirrors) (*corev1.Secret, error) {
	destCredentialSecret := mirrors.GetCredentialSecretForImage(image)

	if destCredentialSecret == nil {
		return nil, nil
	}

	// This allows to use the same code for both ClusterImageSetMirror and ImageSetMirror
	if namespace == "" {
		namespace = destCredentialSecret.Namespace
	}

	secret := &corev1.Secret{}
	if err := r.getPullSecret(ctx, namespace, destCredentialSecret.Name, secret); err != nil {
		return nil, err
	}

	return secret, nil
}

func (r *ImageSetMirrorBaseReconciler) mirrorImage(ctx context.Context, namespace string, mirrors kuikv1alpha1.Mirrors, podsByMatchingImages map[string]*corev1.Pod, from string, to *kuikv1alpha1.MirrorStatus) (err error) {
	srcSecrets, err := r.getPullSecretsFromPods(ctx, podsByMatchingImages, from)
	if err != nil {
		return err
	}

	// When no consuming pod contributes credentials — the steady-state case
	// once the webhook has rewritten workloads to pull from the mirror — fall
	// back to the source CredentialSecret declared on a matching
	// (Cluster)ReplicatedImageSet upstream. Without this, private upstreams
	// (Docker Hub, private quay.io, ...) fail to mirror with 401 UNAUTHORIZED
	// (issue #606).
	if len(srcSecrets) == 0 {
		secret, err := r.getSourceSecretFromReplicatedImageSets(ctx, from, namespace)
		if err != nil {
			return err
		}
		if secret != nil {
			srcSecrets = []corev1.Secret{*secret}
		}
	}

	destSecrets := make([]corev1.Secret, 1)
	if secret, err := r.getImageSecretFromMirrors(ctx, to.Image, namespace, mirrors); err != nil {
		return err
	} else if secret != nil {
		destSecrets[0] = *secret
	}

	defer func() {
		if err != nil {
			client := registry.NewClient(nil, nil).WithPullSecrets(destSecrets)
			_, destErr := client.GetDescriptor(ctx, to.Image)
			if destErr == nil {
				logf.FromContext(ctx).V(1).Info("could not mirror image, but the image seems to be already mirrored")
				err = nil
				now := metav1.NewTime(time.Now())
				to.MirroredAt = &now
			}
		}
	}()

	client := registry.NewClient(nil, nil).WithPullSecrets(srcSecrets)
	srcDesc, err := client.GetDescriptor(ctx, from)
	if err != nil {
		return err
	}

	// FIXME: if a platform is added or removed, already mirrored images are not updated consequently
	if err := client.WithTimeout(0).WithPullSecrets(destSecrets).CopyImage(ctx, srcDesc, to.Image, r.platforms); err != nil {
		return err
	}

	now := metav1.NewTime(time.Now())
	to.MirroredAt = &now

	return nil
}

func (r *ImageSetMirrorBaseReconciler) cleanupMirror(ctx context.Context, image, namespace string, mirrors kuikv1alpha1.Mirrors) (success bool) {
	log := logf.FromContext(ctx)

	secret, err := r.getImageSecretFromMirrors(ctx, image, namespace, mirrors)
	if err != nil {
		log.Error(err, "could not read secret for image deletion")
		return false
	} else if secret == nil {
		log.V(1).Info("no secret is configured for deleting image, ignoring")
		return true
	}

	if err := registry.NewClient(nil, nil).WithPullSecrets([]corev1.Secret{*secret}).DeleteImage(ctx, image); err != nil {
		log.Error(err, "could not delete image")
		return false
	}

	return true
}

func mergePreviousAndCurrentMatchingImages(ctx context.Context, pods []corev1.Pod, ismSpec *kuikv1alpha1.ImageSetMirrorSpec, ismStatus *kuikv1alpha1.ImageSetMirrorStatus, mirrorPrefixes map[string][]string) (map[string]*corev1.Pod, error) {
	imageFilter := ismSpec.ImageFilter.MustBuild()
	podsByMatchingImages := podsByNormalizedMatchingImages(ctx, imageFilter, mirrorPrefixes, pods)

	matchingImagesMap := map[string]kuikv1alpha1.MatchingImage{}
	for matchingImage := range podsByMatchingImages {
		mirrors := []kuikv1alpha1.MirrorStatus{}
		for _, mirror := range ismSpec.Mirrors {
			matchingImageWithoutRegistry := strings.SplitN(matchingImage, "/", 2)[1]
			mirrors = append(mirrors, kuikv1alpha1.MirrorStatus{
				Image: path.Join(mirror.Registry, mirror.Path, matchingImageWithoutRegistry),
			})
		}
		matchingImagesMap[matchingImage] = kuikv1alpha1.MatchingImage{
			Image:   matchingImage,
			Mirrors: mirrors,
		}
	}

	inUseImages := podsInUseImages(ctx, pods)
	if err := updateUnusedSince(ctx, matchingImagesMap, inUseImages, ismStatus, imageFilter); err != nil {
		return nil, err
	}

	ismStatus.MatchingImages = make([]kuikv1alpha1.MatchingImage, 0, len(matchingImagesMap))
	for _, img := range matchingImagesMap {
		ismStatus.MatchingImages = append(ismStatus.MatchingImages, img)
	}

	return podsByMatchingImages, nil
}

func podsByNormalizedMatchingImages(ctx context.Context, filter filter.Filter, mirrorPrefixes map[string][]string, pods []corev1.Pod) map[string]*corev1.Pod {
	log := logf.FromContext(ctx)

	filteredOutImagesMap := map[string]struct{}{}
	matchingImagesMap := map[string]*corev1.Pod{}
	for i := range pods {
		pod := &pods[i]
		for image := range normalizedImageNamesFromPod(pod) {
			relevantMirrorPrefixes := append(mirrorPrefixes[""], mirrorPrefixes[pod.Namespace]...)
			if slices.ContainsFunc(relevantMirrorPrefixes, func(mirrorPrefix string) bool {
				return strings.HasPrefix(image, mirrorPrefix)
			}) {
				filteredOutImagesMap[image] = struct{}{}
				continue
			}

			if filter.Match(image) {
				matchingImagesMap[image] = pod
			}
		}
	}

	if len(filteredOutImagesMap) > 0 {
		filteredOutImages := slices.Collect(maps.Keys(filteredOutImagesMap))
		log.V(1).Info("filtering out images to prevent mirror loop", "images", filteredOutImages, "count", len(filteredOutImagesMap))
	}

	return matchingImagesMap
}

func normalizedImageNamesMapFromAnnotatedPod(ctx context.Context, pod *corev1.Pod) map[string]bool {
	log := logf.FromContext(ctx)

	originalImages := map[string]string{}
	if originalImagesStr, ok := pod.Annotations[OriginalImagesAnnotation]; ok {
		if err := json.Unmarshal([]byte(originalImagesStr), &originalImages); err != nil {
			log.Error(err, "could not unmarshal "+OriginalImagesAnnotation+" annotation")
		}
	}

	imageNames := map[string]bool{}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if strings.Contains(container.Image, "@") {
			continue // ignore digest-based images
		}
		if named, err := reference.ParseNormalizedNamed(container.Image); err == nil {
			imageNames[named.String()] = false
		}
	}
	for _, image := range originalImages {
		if strings.Contains(image, "@") {
			continue // ignore digest-based images
		}
		if named, err := reference.ParseNormalizedNamed(image); err == nil {
			imageNames[named.String()] = true
		}
	}

	return imageNames
}

func normalizedImageNamesFromAnnotatedPod(ctx context.Context, pod *corev1.Pod) iter.Seq[string] {
	return maps.Keys(normalizedImageNamesMapFromAnnotatedPod(ctx, pod))
}

func normalizedImageNamesFromPod(pod *corev1.Pod) iter.Seq[string] {
	imageNames := map[string]struct{}{}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if strings.Contains(container.Image, "@") {
			continue // ignore digest-based images
		}
		if named, err := reference.ParseNormalizedNamed(container.Image); err == nil {
			imageNames[named.String()] = struct{}{}
		}
	}

	return maps.Keys(imageNames)
}

// podsInUseImages returns the set of normalized image names that are still
// referenced by the cluster, considering BOTH the current container image and
// the original image stashed in the kuik.enix.io/original-images annotation.
// This is the signal used to decide whether a previously-mirrored image is
// still in use: a pod that the webhook has rewritten to pull from the mirror
// still logically requires its original reference.
func podsInUseImages(ctx context.Context, pods []corev1.Pod) map[string]struct{} {
	inUse := map[string]struct{}{}
	for i := range pods {
		for image := range normalizedImageNamesFromAnnotatedPod(ctx, &pods[i]) {
			inUse[image] = struct{}{}
		}
	}
	return inUse
}

func updateUnusedSince(ctx context.Context, matchingImagesMap map[string]kuikv1alpha1.MatchingImage, inUseImages map[string]struct{}, ismStatus *kuikv1alpha1.ImageSetMirrorStatus, imageFilter filter.Filter) error {
	log := logf.FromContext(ctx)
	unusedSinceNotMatching := metav1.Time{Time: (time.Time{}).Add(time.Hour)}

	for i := range ismStatus.MatchingImages {
		img := &ismStatus.MatchingImages[i]

		named, match, err := internal.NormalizeAndMatch(imageFilter, img.Image)
		if err != nil {
			return err
		} else if !match {
			// The image isn't matching anymore, which is different from matching but stopped to be used in the cluster.
			// Thus, we set UnusedSince to 0001-01-01 01:00:00 +0000 UTC to trigger instant expiry and deletion.
			// We add 1 hour to the zero value to prevent the patch to be ignored (zero value is considered == to nil)
			if !img.UnusedSince.Equal(&unusedSinceNotMatching) {
				img.UnusedSince = &unusedSinceNotMatching
				log.Info("image is not matching anymore, queuing it for deletion", "image", img.Image)
			}
		} else if _, ok := inUseImages[named.String()]; !ok {
			if img.UnusedSince.IsZero() {
				img.UnusedSince = &metav1.Time{Time: time.Now()}
				log.Info("image is not used anymore, marking it as unused", "image", img.Image)
			}
		} else {
			img.UnusedSince = nil
		}

		img.Mirrors = mergeMirrors(img.Mirrors, matchingImagesMap[named.String()].Mirrors)
		matchingImagesMap[named.String()] = *img
	}

	return nil
}

func mergeMirrors(currentMirrors, expectedMirrors []kuikv1alpha1.MirrorStatus) []kuikv1alpha1.MirrorStatus {
	// FIXME: remove and cleanup images that are present in current but not in expected
	currentImages := map[string]struct{}{}
	for _, mirror := range currentMirrors {
		currentImages[mirror.Image] = struct{}{}
	}

	for _, mirror := range expectedMirrors {
		if _, ok := currentImages[mirror.Image]; !ok {
			currentMirrors = append(currentMirrors, mirror)
		}
	}

	return currentMirrors
}

func newMirroringRateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	// based on workqueue.DefaultTypedControllerRateLimiter
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
}

func (r *ImageSetMirrorBaseReconciler) getAllMirrorPrefixes(ctx context.Context, ignoreNamespaces bool) (map[string][]string, error) {
	var cismList kuikv1alpha1.ClusterImageSetMirrorList
	if err := r.List(ctx, &cismList); err != nil {
		return nil, err
	}
	var ismList kuikv1alpha1.ImageSetMirrorList
	if err := r.List(ctx, &ismList); err != nil {
		return nil, err
	}

	clusterwideMirrorPrefixes := make([]string, 0, len(cismList.Items)) // preallocate at least 1 mirror slot per CISM
	for _, cism := range cismList.Items {
		for _, mirror := range cism.Spec.Mirrors {
			clusterwideMirrorPrefixes = append(clusterwideMirrorPrefixes, mirror.Prefix())
		}
	}

	mirrorPrefixes := map[string][]string{
		"": clusterwideMirrorPrefixes,
	}

	for _, ism := range ismList.Items {
		if ignoreNamespaces {
			ism.Namespace = ""
		}
		for _, mirror := range ism.Spec.Mirrors {
			mirrorPrefixes[ism.Namespace] = append(mirrorPrefixes[ism.Namespace], mirror.Prefix())
		}
	}

	return mirrorPrefixes, nil
}
