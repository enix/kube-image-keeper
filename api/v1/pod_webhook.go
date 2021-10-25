package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mutate-core-v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups=core,resources=pods,verbs=create;update,versions=v1,name=mpod.kb.io,admissionReviewVersions={v1,v1beta1}

type ImageRewriter struct {
	Client          client.Client
	IgnoreNamespace string
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
		a.RewriteImages(pod)
	} else {
		log.Info("Ignoring pod from ignored namespace", "namespace", req.Namespace)
	}

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

	pod.Labels["dcr-images-rewritten"] = "true"

	// Handle Containers
	for i := range pod.Spec.Containers {
		handleContainer(pod, &pod.Spec.Containers[i], fmt.Sprintf("original-image-%d", i))
	}

	// Handle init containers
	for i := range pod.Spec.InitContainers {
		handleContainer(pod, &pod.Spec.InitContainers[i], fmt.Sprintf("original-init-image-%d", i))
	}
}

// InjectDecoder injects the decoder
func (a *ImageRewriter) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}

func handleContainer(pod *corev1.Pod, container *v1.Container, annotationKey string) {
	pod.Annotations[annotationKey] = container.Image
	container.Image = "localhost:7439/" + container.Image
}
