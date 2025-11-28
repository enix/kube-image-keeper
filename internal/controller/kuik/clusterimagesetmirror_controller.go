package kuik

import (
	"context"
	"errors"
	"path"
	"regexp"
	"strings"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ClusterImageSetMirrorReconciler reconciles a ClusterImageSetMirror object
type ClusterImageSetMirrorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ClusterImageSetMirror object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *ClusterImageSetMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cism kuikv1alpha1.ClusterImageSetMirror
	if err := r.Get(ctx, req.NamespacedName, &cism); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{Namespace: cism.Namespace}); err != nil {
		return ctrl.Result{}, err
	}

	podsByMatchingImages, err := r.podsByNormalizedMatchingImages(&cism, pods.Items)
	if err != nil {
		return ctrl.Result{}, err
	}

	matchedImagesMap := map[string]kuikv1alpha1.MatchedImage{}
	for _, matchedImage := range cism.Status.MatchedImages {
		matchedImagesMap[matchedImage.Image] = matchedImage
	}

	for matchingImage := range podsByMatchingImages {
		mirrors := []kuikv1alpha1.MirrorStatus{}
		for _, mirror := range cism.Spec.Mirrors {
			matchingImageWithoutRegistry := strings.SplitN(matchingImage, "/", 2)[1]
			mirrors = append(mirrors, kuikv1alpha1.MirrorStatus{
				Image: path.Join(mirror.Registry, mirror.Path, matchingImageWithoutRegistry),
			})
		}
		if _, ok := matchedImagesMap[matchingImage]; !ok {
			matchedImagesMap[matchingImage] = kuikv1alpha1.MatchedImage{
				Image:   matchingImage,
				Mirrors: mirrors,
			}
		}
	}

	originalCism := cism.DeepCopy()
	cism.Status.MatchedImages = []kuikv1alpha1.MatchedImage{}
	for _, matchedImage := range matchedImagesMap {
		cism.Status.MatchedImages = append(cism.Status.MatchedImages, matchedImage)
	}

	if err := r.Status().Patch(ctx, &cism, client.MergeFrom(originalCism)); err != nil {
		return ctrl.Result{}, err
	}

	someMirrorFailed := false
	for i := range cism.Status.MatchedImages {
		matchedImage := &cism.Status.MatchedImages[i]

		if matchedImage.UnusedSince != nil {
			continue
		}

		originalCism = cism.DeepCopy()

		for j := range matchedImage.Mirrors {
			mirror := &matchedImage.Mirrors[j]

			if mirror.MirroredAt == nil {
				mirrorLog := log.WithValues("from", matchedImage.Image, "to", mirror.Image)
				mirrorLog.Info("mirroring image")

				err := r.MirrorImage(ctx, &cism, podsByMatchingImages, matchedImage.Image, mirror)
				if err != nil {
					mirrorLog.Error(err, "could not mirror image")
					someMirrorFailed = true
					mirror.LastError = err.Error()
				} else {
					mirrorLog.Info("successfully mirrored image")
					mirror.LastError = ""
				}
			}
		}

		if err := r.Status().Patch(ctx, &cism, client.MergeFrom(originalCism)); err != nil {
			return ctrl.Result{}, err
		}
	}

	if someMirrorFailed {
		return ctrl.Result{}, errors.New("one or more image(s) could not be mirrored")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterImageSetMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ClusterImageSetMirror{}).
		Named("kuik-clusterimagesetmirror").
		Complete(r)
}

func (r *ClusterImageSetMirrorReconciler) getPullSecretsFromPods(ctx context.Context, podsByMatchingImages map[string]*corev1.Pod, image string) ([]corev1.Secret, error) {
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

func (r *ClusterImageSetMirrorReconciler) getPullSecret(ctx context.Context, namespace, name string, secret *corev1.Secret) error {
	secretReference := client.ObjectKey{Namespace: namespace, Name: name}
	if err := r.Get(ctx, secretReference, secret); err != nil {
		return err
	}
	return nil
}

func (r *ClusterImageSetMirrorReconciler) MirrorImage(ctx context.Context, cism *kuikv1alpha1.ClusterImageSetMirror, podsByMatchingImages map[string]*corev1.Pod, from string, to *kuikv1alpha1.MirrorStatus) error {
	srcSecrets, err := r.getPullSecretsFromPods(ctx, podsByMatchingImages, from)
	if err != nil {
		return err
	}

	destCredentialSecret := cism.Spec.Mirrors.GetCredentialSecretForImage(to.Image)
	destSecrets := make([]corev1.Secret, 1)
	namespace := cism.Namespace
	// This allows to use the same code for both ClusterImageSetMirror and ImageSetMirror
	if namespace == "" {
		namespace = destCredentialSecret.Namespace
	}
	if err := r.getPullSecret(ctx, namespace, destCredentialSecret.Name, &destSecrets[0]); err != nil {
		return err
	}

	registry := registry.NewClient(nil, nil).WithPullSecrets(srcSecrets)
	srcDesc, err := registry.GetDescriptor(from)
	if err != nil {
		return err
	}

	if err := registry.WithTimeout(0).WithPullSecrets(destSecrets).CopyImage(srcDesc, to.Image, []string{"amd64"}); err != nil {
		return err
	}

	now := metav1.NewTime(time.Now())
	to.MirroredAt = &now

	return nil
}

func (r *ClusterImageSetMirrorReconciler) podsByNormalizedMatchingImages(cism *kuikv1alpha1.ClusterImageSetMirror, pods []corev1.Pod) (map[string]*corev1.Pod, error) {
	// TODO: validating webhook for the regexp
	matcher, err := regexp.Compile(cism.Spec.ImageMatcher)
	if err != nil {
		return nil, err
	}

	matchingImagesMap := map[string]*corev1.Pod{}
	for _, pod := range pods {
		for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
			if matcher.Match([]byte(container.Image)) {
				// FIXME: normalize image name
				matchingImagesMap[container.Image] = &pod
			}
		}
	}

	return matchingImagesMap, nil
}
