package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	_ "crypto/sha256"

	"github.com/enix/kube-image-keeper/controllers"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

//+kubebuilder:webhook:path=/mutate-core-v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups=core,resources=pods,verbs=create;update,versions=v1,name=mpod.kb.io,admissionReviewVersions=v1

type ImageRewriter struct {
	Client       client.Client
	IgnoreImages []*regexp.Regexp
	ProxyPort    int
	decoder      *admission.Decoder
}

func (a *ImageRewriter) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := a.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	a.RewriteImages(pod)

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (a *ImageRewriter) RewriteImages(pod *corev1.Pod) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}

	pod.Labels[controllers.LabelImageRewrittenName] = "true"

	// Handle Containers
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		a.handleContainer(pod, container, registry.ContainerAnnotationKey(container.Name, false))
	}

	// Handle init containers
	for i := range pod.Spec.InitContainers {
		container := &pod.Spec.InitContainers[i]
		a.handleContainer(pod, container, registry.ContainerAnnotationKey(container.Name, true))
	}
}

// InjectDecoder injects the decoder
func (a *ImageRewriter) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}

func (a *ImageRewriter) handleContainer(pod *corev1.Pod, container *corev1.Container, annotationKey string) {
	if a.isImageIgnored(container) {
		return
	}

	re := regexp.MustCompile(`localhost:[0-9]+/`)
	image := re.ReplaceAllString(container.Image, "")

	sourceRef, err := name.ParseReference(image, name.Insecure)
	if err != nil {
		return // ignore rewriting invalid images
	}

	pod.Annotations[annotationKey] = image

	sanitizedRegistryName := strings.ReplaceAll(sourceRef.Context().RegistryStr(), ":", "-")
	image = strings.ReplaceAll(image, sourceRef.Context().RegistryStr(), sanitizedRegistryName)

	container.Image = fmt.Sprintf("localhost:%d/%s", a.ProxyPort, image)
}

func (a *ImageRewriter) isImageIgnored(container *corev1.Container) (ignored bool) {
	for _, r := range a.IgnoreImages {
		if r.MatchString(container.Image) {
			return true
		}
	}
	return
}
