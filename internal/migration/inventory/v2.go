package inventory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// The labels of the v2 chart and of the pull secrets v2 copies (2.3.x:
// helm/kube-image-keeper/templates/_helpers.tpl, internal/controller/kuik/field_names.go).
const (
	labelComponent    = "app.kubernetes.io/component"
	labelVersion      = "app.kubernetes.io/version"
	labelChart        = "helm.sh/chart"
	labelControlPlane = "control-plane"
	chartPrefix       = "kube-image-keeper-"
	imageRepository   = "kube-image-keeper"

	// OwnerUIDLabel marks a pull secret v2 copied into a namespace.
	OwnerUIDLabel   = "kuik.enix.io/owner-uid"
	ownerKindLabel  = "kuik.enix.io/owner-kind"
	ownerNameLabel  = "kuik.enix.io/owner-name"
	webhookName     = "mpod-v1.kb.io"
	webhookPath     = "/mutate--v1-pod"
	injectCAFrom    = "cert-manager.io/inject-ca-from"
	webhookSuffix   = "-webhook"
	servingCertName = "-serving-cert"
)

// isV2Manager reports whether d is the manager Deployment of the v2 chart: the component and
// control-plane labels it shares with any kubebuilder operator, plus the kuik chart label or
// image. The name is not relied on, since fullnameOverride changes it.
func isV2Manager(d *appsv1.Deployment) bool {
	if d.Labels[labelComponent] != "manager" || d.Labels[labelControlPlane] != "controller-manager" {
		return false
	}
	if strings.HasPrefix(d.Labels[labelChart], chartPrefix) {
		return true
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		if repository(c.Image) == imageRepository {
			return true
		}
	}
	return false
}

// repository returns the last path segment of image, without tag or digest.
func repository(image string) string {
	ref, err := imagepath.Parse(image)
	if err != nil || len(ref.Segments) == 0 {
		return ""
	}
	return ref.Segments[len(ref.Segments)-1]
}

// v2Version returns the version of a v2 manager: the appVersion its chart records, or the tag
// of its image when the label is missing.
func v2Version(d *appsv1.Deployment) string {
	if v := d.Labels[labelVersion]; v != "" {
		return v
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		if ref, err := imagepath.Parse(c.Image); err == nil && repository(c.Image) == imageRepository {
			return ref.Tag
		}
	}
	return ""
}

// podWebhookNamespace returns the namespace of the Service a kuik pod webhook calls, false
// when w is not a kuik pod webhook configuration. Both charts write the same webhook, named
// and routed as kubebuilder scaffolds it, with the Service `<fullname>-webhook` and the
// certificate `<fullname>-serving-cert` of the same release: the pair tells it from another
// operator's scaffold.
func podWebhookNamespace(w *admissionregistrationv1.MutatingWebhookConfiguration) (string, bool) {
	for _, hook := range w.Webhooks {
		svc := hook.ClientConfig.Service
		if hook.Name != webhookName || svc == nil || svc.Path == nil || *svc.Path != webhookPath {
			continue
		}
		fullname, ok := strings.CutSuffix(svc.Name, webhookSuffix)
		if ok && w.Annotations[injectCAFrom] == svc.Namespace+"/"+fullname+servingCertName {
			return svc.Namespace, true
		}
	}
	return "", false
}

// isV3Webhook reports whether a kuik pod webhook configuration is v3's: the v3 chart labels
// it, the v2 chart never did (2.3.x: templates/mutatingwebhookconfiguration.yaml).
func isV3Webhook(w *admissionregistrationv1.MutatingWebhookConfiguration) bool {
	return w.Labels[labelChart] != ""
}

// readV2Installation finds the v2 manager and its webhook configuration.
func readV2Installation(deployments []appsv1.Deployment, webhooks []admissionregistrationv1.MutatingWebhookConfiguration) V2Installation {
	var v V2Installation
	for i := range deployments {
		if d := &deployments[i]; isV2Manager(d) {
			v.Namespace, v.Deployment, v.Version = d.Namespace, d.Name, v2Version(d)
			break
		}
	}
	for i := range webhooks {
		w := &webhooks[i]
		namespace, ok := podWebhookNamespace(w)
		if !ok || isV3Webhook(w) || (v.Namespace != "" && namespace != v.Namespace) {
			continue
		}
		v.Namespace, v.WebhookConfiguration = namespace, w.Name
		break
	}
	return v
}

// readV2Resources reads every object of the v2 CRDs.
func readV2Resources(ctx context.Context, r client.Reader, crds []crd) ([]Resource, error) {
	var resources []Resource
	for _, c := range crds {
		if !c.v2 {
			continue
		}
		items, err := listObjects(ctx, r, c)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			spec, _, _ := unstructured.NestedMap(item.Object, "spec")
			status, _, _ := unstructured.NestedMap(item.Object, "status")
			resources = append(resources, Resource{
				Kind:       c.Kind,
				Namespace:  item.GetNamespace(),
				Name:       item.GetName(),
				Finalizers: item.GetFinalizers(),
				Spec:       spec,
				Status:     status,
			})
		}
	}
	slices.SortFunc(resources, func(a, b Resource) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return resources, nil
}

// readPullSecrets lists the pull secrets v2 copied, by their metadata: their data is never
// read.
func readPullSecrets(ctx context.Context, r client.Reader) ([]PullSecret, error) {
	list := &metav1.PartialObjectMetadataList{}
	list.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("SecretList"))
	if err := r.List(ctx, list, client.HasLabels{OwnerUIDLabel}); err != nil {
		return nil, fmt.Errorf("listing Secrets labelled %s: %w", OwnerUIDLabel, err)
	}
	secrets := make([]PullSecret, 0, len(list.Items))
	for _, s := range list.Items {
		secrets = append(secrets, PullSecret{
			Namespace: s.Namespace,
			Name:      s.Name,
			Owner: PullSecretOwner{
				UID:  s.Labels[OwnerUIDLabel],
				Kind: s.Labels[ownerKindLabel],
				Name: s.Labels[ownerNameLabel],
			},
		})
	}
	slices.SortFunc(secrets, func(a, b PullSecret) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return secrets, nil
}
