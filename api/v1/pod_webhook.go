package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	_ "crypto/sha256"

	"github.com/docker/distribution/reference"
	"gitlab.enix.io/products/docker-cache-registry/controllers"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mutate-core-v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups=core,resources=pods,verbs=create;update,versions=v1,name=mpod.kb.io,admissionReviewVersions={v1,v1beta1}

type ImageRewriter struct {
	Client          client.Client
	IgnoreNamespace string
	ProxyPort       int
	decoder         *admission.Decoder
}

func (a *ImageRewriter) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := log.
		FromContext(ctx).
		WithName("controller-runtime.webhook.pod")

	pod := &corev1.Pod{}
	err := a.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if req.Namespace != a.IgnoreNamespace {
		err := a.RewriteImages(pod)
		if err != nil {
			return admission.Errored(http.StatusUnprocessableEntity, err)
		}
	} else {
		log.Info("Ignoring pod from ignored namespace", "namespace", req.Namespace)
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (a *ImageRewriter) RewriteImages(pod *corev1.Pod) error {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}

	pod.Labels[controllers.LabelImageRewrittenName] = "true"

	// Handle Containers
	invalidImages := []string{}
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		err := a.handleContainer(pod, container, fmt.Sprintf(controllers.AnnotationOriginalImageTemplate, container.Name))
		if err != nil {
			invalidImages = append(invalidImages, pod.Spec.Containers[i].Image)
		}
	}

	// Handle init containers
	for i := range pod.Spec.InitContainers {
		container := &pod.Spec.InitContainers[i]
		err := a.handleContainer(pod, container, fmt.Sprintf(controllers.AnnotationOriginalInitImageTemplate, container.Name))
		if err != nil {
			invalidImages = append(invalidImages, pod.Spec.InitContainers[i].Image)
		}
	}

	if len(invalidImages) > 0 {
		return fmt.Errorf("some images are incorrectly formatted: %v", invalidImages)
	}

	return nil
}

// InjectDecoder injects the decoder
func (a *ImageRewriter) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}

func (a *ImageRewriter) handleContainer(pod *corev1.Pod, container *corev1.Container, annotationKey string) error {
	pod.Annotations[annotationKey] = container.Image

	ref, err := reference.ParseAnyReference(container.Image)
	if err != nil {
		return err
	}

	prefix := fmt.Sprintf("localhost:%d/", a.ProxyPort)
	if strings.HasPrefix(ref.String(), prefix) {
		return nil
	}

	container.Image = prefix + ref.String()

	return nil
}
