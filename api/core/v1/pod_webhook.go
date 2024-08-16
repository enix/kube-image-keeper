package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	_ "crypto/sha256"

	"github.com/enix/kube-image-keeper/internal/controller/core"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/google/go-containerregistry/pkg/name"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

//+kubebuilder:webhook:path=/mutate-core-v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups=core,resources=pods,verbs=create;update,versions=v1,name=mpod.kb.io,admissionReviewVersions=v1

var (
	errImageContainsDigests = errors.New("image contains a digest")
	errPullPolicyAlways     = errors.New("container is configured with imagePullPolicy: Always")
	errPullPolicyNever      = errors.New("container is configured with imagePullPolicy: Never")
)

type ImageRewriter struct {
	Client                 client.Client
	IgnoreImages           []*regexp.Regexp
	IgnorePullPolicyAlways bool
	ProxyPort              int
	Decoder                *admission.Decoder
}

type PodInitializer struct {
	Client client.Client
}

type RewrittenImage struct {
	Original            string
	Rewritten           string
	NotRewrittenBecause string
}

func (a *ImageRewriter) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := log.
		FromContext(ctx).
		WithName("webhook.pod")

	pod := &corev1.Pod{}
	err := a.Decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	rewrittenImages := a.RewriteImages(pod, req.Operation == admissionv1.Create)

	log.Info("rewriting pod images", "rewrittenImages", rewrittenImages)

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (a *ImageRewriter) RewriteImages(pod *corev1.Pod, isNewPod bool) []RewrittenImage {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}

	rewriteImages := pod.Annotations[core.AnnotationRewriteImagesName] == "true" || isNewPod

	pod.Labels[core.LabelManagedName] = "true"
	pod.Annotations[core.AnnotationRewriteImagesName] = fmt.Sprintf("%t", rewriteImages)

	rewrittenImages := []RewrittenImage{}

	// Handle Containers
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		rewrittenImage := a.handleContainer(pod, container, registry.ContainerAnnotationKey(container.Name, false), rewriteImages)
		rewrittenImages = append(rewrittenImages, rewrittenImage)
	}

	// Handle init containers
	for i := range pod.Spec.InitContainers {
		container := &pod.Spec.InitContainers[i]
		rewrittenImage := a.handleContainer(pod, container, registry.ContainerAnnotationKey(container.Name, true), rewriteImages)
		rewrittenImages = append(rewrittenImages, rewrittenImage)
	}

	return rewrittenImages
}

func (a *ImageRewriter) handleContainer(pod *corev1.Pod, container *corev1.Container, annotationKey string, rewriteImage bool) RewrittenImage {
	if err := a.isImageRewritable(container); err != nil {
		return RewrittenImage{
			Original:            container.Image,
			NotRewrittenBecause: err.Error(),
		}
	}

	re := regexp.MustCompile(`localhost:[0-9]+/`)
	image := re.ReplaceAllString(container.Image, "")

	sourceRef, err := name.ParseReference(image, name.Insecure)
	if err != nil {
		return RewrittenImage{
			Original:            container.Image,
			NotRewrittenBecause: err.Error(),
		} // ignore rewriting invalid images
	}

	pod.Annotations[annotationKey] = image

	if !rewriteImage {
		return RewrittenImage{
			Original:            container.Image,
			NotRewrittenBecause: "pod doesn't allow to rewrite its images",
		}
	}

	sanitizedRegistryName := strings.ReplaceAll(sourceRef.Context().RegistryStr(), ":", "-")
	image = strings.ReplaceAll(image, sourceRef.Context().RegistryStr(), sanitizedRegistryName)

	originalImage := container.Image
	container.Image = fmt.Sprintf("localhost:%d/%s", a.ProxyPort, image)

	return RewrittenImage{
		Original:  originalImage,
		Rewritten: container.Image,
	}
}

func (a *ImageRewriter) isImageRewritable(container *corev1.Container) error {
	if strings.Contains(container.Image, "@") {
		return errImageContainsDigests
	}

	if container.ImagePullPolicy == corev1.PullNever {
		return errPullPolicyNever
	}

	if a.IgnorePullPolicyAlways {
		pullAlways := container.ImagePullPolicy == corev1.PullAlways
		isLatestWithoutPullPolicy := container.ImagePullPolicy == "" && (!strings.Contains(container.Image, ":") || strings.HasSuffix(container.Image, ":latest"))
		if pullAlways || isLatestWithoutPullPolicy {
			return errPullPolicyAlways
		}
	}

	for _, r := range a.IgnoreImages {
		if r.MatchString(container.Image) {
			return fmt.Errorf("image matches %s", r.String())
		}
	}

	return nil
}

func (p *PodInitializer) Start(ctx context.Context) error {
	setupLog := ctrl.Log.WithName("setup.pods")
	pods := corev1.PodList{}
	err := p.Client.List(ctx, &pods)
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		setupLog.Info("patching", "pod", pod.Namespace+"/"+pod.Name)
		err := p.Client.Patch(ctx, &pod, client.RawPatch(types.JSONPatchType, []byte("[]")))
		if err != nil && !apierrors.IsNotFound(err) {
			setupLog.Info("patching failed", "pod", pod.Namespace+"/"+pod.Name, "err", err)
		}
	}
	setupLog.Info("completed")

	return nil
}

func (t *PodInitializer) NeedLeaderElection() bool {
	return true
}
