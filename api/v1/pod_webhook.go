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
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
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

type PodInitializer struct {
	Client client.Client
}

func (a *ImageRewriter) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := a.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	a.RewriteImages(pod, req.Operation == admissionv1.Create)

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (a *ImageRewriter) RewriteImages(pod *corev1.Pod, isNewPod bool) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}

	rewriteImages := pod.Annotations[controllers.AnnotationRewriteImagesName] == "true" || isNewPod

	pod.Labels[controllers.LabelManagedName] = "true"
	pod.Annotations[controllers.AnnotationRewriteImagesName] = fmt.Sprintf("%t", rewriteImages)

	// Handle Containers
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		a.handleContainer(pod, container, registry.ContainerAnnotationKey(container.Name, false), rewriteImages)
	}

	// Handle init containers
	for i := range pod.Spec.InitContainers {
		container := &pod.Spec.InitContainers[i]
		a.handleContainer(pod, container, registry.ContainerAnnotationKey(container.Name, true), rewriteImages)
	}
}

// InjectDecoder injects the decoder
func (a *ImageRewriter) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}

func (a *ImageRewriter) handleContainer(pod *corev1.Pod, container *corev1.Container, annotationKey string, rewriteImage bool) {
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

	if rewriteImage {
		sanitizedRegistryName := strings.ReplaceAll(sourceRef.Context().RegistryStr(), ":", "-")
		image = strings.ReplaceAll(image, sourceRef.Context().RegistryStr(), sanitizedRegistryName)
		container.Image = fmt.Sprintf("localhost:%d/%s", a.ProxyPort, image)
	}
}

func (a *ImageRewriter) isImageIgnored(container *corev1.Container) (ignored bool) {
	if strings.Contains(container.Image, "@") {
		return true
	}
	for _, r := range a.IgnoreImages {
		if r.MatchString(container.Image) {
			return true
		}
	}
	return
}

func (p *PodInitializer) Start(ctx context.Context) error {
	setupLog := ctrl.Log.WithName("setup.pods")
	pods := corev1.PodList{}
	err := p.Client.List(context.TODO(), &pods)
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		setupLog.Info("patching " + pod.Namespace + "/" + pod.Name)
		err := p.Client.Patch(context.Background(), &pod, client.RawPatch(types.JSONPatchType, []byte("[]")))
		if err != nil {
			return err
		}
	}

	return nil
}

func (t *PodInitializer) NeedLeaderElection() bool {
	return true
}
