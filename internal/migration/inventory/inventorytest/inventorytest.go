// Package inventorytest builds the synthetic clusters the migration specs read: a fake client
// that refuses every write and every read of a Secret's data, and the v2 and v3 objects found
// on a cluster that runs either. Every name and registry is made up.
package inventorytest

import (
	"context"
	"errors"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Group and Version are the API group and version of every kuik CRD, v2 and v3 alike.
const (
	Group   = "kuik.enix.io"
	Version = "v1alpha1"
)

// The labels and words the fixtures repeat.
const (
	labelName     = "app.kubernetes.io/name"
	labelInstance = "app.kubernetes.io/instance"
	labelChart    = "helm.sh/chart"
	chart         = "kube-image-keeper"
	manager       = "manager"
	schemaType    = "type"
	schemaObject  = "object"
)

// Guard counts what the fake client refused: any write, and any read of a full Secret.
type Guard struct {
	Writes      int
	SecretReads int
}

var (
	errWrite      = errors.New("the migration must not write to the cluster")
	errSecretRead = errors.New("the migration must not read the data of a Secret")
)

// Reader returns a fake client holding objects. It refuses, and counts in the Guard, every
// write and every Get or List of Secrets other than by their metadata.
func Reader(objects ...client.Object) (client.WithWatch, *Guard) {
	g := &Guard{}
	write := func() error {
		g.Writes++
		return errWrite
	}
	c := fake.NewClientBuilder().WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if isSecretData(obj) {
				g.SecretReads++
				return errSecretRead
			}
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if isSecretData(list) {
				g.SecretReads++
				return errSecretRead
			}
			return c.List(ctx, list, opts...)
		},
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return write()
		},
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return write()
		},
		DeleteAllOf: func(context.Context, client.WithWatch, client.Object, ...client.DeleteAllOfOption) error {
			return write()
		},
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			return write()
		},
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return write()
		},
		Apply: func(context.Context, client.WithWatch, runtime.ApplyConfiguration, ...client.ApplyOption) error {
			return write()
		},
		SubResourceCreate: func(context.Context, client.Client, string, client.Object, client.Object, ...client.SubResourceCreateOption) error {
			return write()
		},
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			return write()
		},
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return write()
		},
		SubResourceApply: func(context.Context, client.Client, string, runtime.ApplyConfiguration, ...client.SubResourceApplyOption) error {
			return write()
		},
	}).Build()
	return c, g
}

// isSecretData reports whether obj reads Secrets with their data: a typed or unstructured
// Secret or SecretList. A PartialObjectMetadata read carries no data.
func isSecretData(obj runtime.Object) bool {
	switch o := obj.(type) {
	case *corev1.Secret, *corev1.SecretList:
		return true
	case *unstructured.Unstructured:
		return o.GetKind() == "Secret"
	case *unstructured.UnstructuredList:
		return o.GetKind() == "SecretList"
	}
	return false
}

// CRD returns a kuik CustomResourceDefinition whose spec schema declares specFields.
func CRD(plural, kind string, namespaced bool, specFields ...string) *unstructured.Unstructured {
	scope := "Cluster"
	if namespaced {
		scope = "Namespaced"
	}
	properties := map[string]any{}
	for _, f := range specFields {
		properties[f] = map[string]any{schemaType: "string"}
	}
	crd := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"group": Group,
			"names": map[string]any{"plural": plural, "kind": kind, "listKind": kind + "List"},
			"scope": scope,
			"versions": []any{map[string]any{
				"name":    Version,
				"served":  true,
				"storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					schemaType: schemaObject,
					"properties": map[string]any{
						"spec":   map[string]any{schemaType: schemaObject, "properties": properties},
						"status": map[string]any{schemaType: schemaObject},
					},
				}},
			}},
		},
	}}
	crd.SetAPIVersion("apiextensions.k8s.io/v1")
	crd.SetKind("CustomResourceDefinition")
	crd.SetName(plural + "." + Group)
	return crd
}

// V2CRDs returns the CRDs of kuik 2.3.
func V2CRDs() []client.Object {
	return []client.Object{
		CRD("clusterimagesetmirrors", "ClusterImageSetMirror", false, "priority", "imageFilter", "cleanup", "mirrors"),
		CRD("imagesetmirrors", "ImageSetMirror", true, "priority", "imageFilter", "cleanup", "mirrors"),
		CRD("clusterreplicatedimagesets", "ClusterReplicatedImageSet", false, "priority", "upstreams"),
		CRD("replicatedimagesets", "ReplicatedImageSet", true, "priority", "upstreams"),
		CRD("clusterimagesetavailabilities", "ClusterImageSetAvailability", false, "unusedImageExpiry", "imageFilter"),
	}
}

// V3CRDs returns the CRDs of kuik v3, with a spec schema that carries their v3 fields.
func V3CRDs() []client.Object {
	return []client.Object{
		CRD("imagemirrors", "ImageMirror", false, "podSelector", "namespaceSelector", "excludeImages", "rewritePolicy", "destination", "cleanup", "driftPolicy"),
		CRD("imagealternatives", "ImageAlternative", false, "podSelector", "namespaceSelector", "rewritePolicy", "alternatives"),
		CRD("imagemonitors", "ImageMonitor", false, "podSelector", "namespaceSelector", "unusedImageRetention", "driftDetection", "monitorAlternatives"),
	}
}

// Object returns a kuik resource of kind; namespace is empty for a cluster-scoped one.
func Object(kind, namespace, name string, spec, status map[string]any) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]any{}}
	if spec != nil {
		o.Object["spec"] = spec
	}
	if status != nil {
		o.Object["status"] = status
	}
	o.SetAPIVersion(Group + "/" + Version)
	o.SetKind(kind)
	o.SetNamespace(namespace)
	o.SetName(name)
	return o
}

// Mirrors returns the spec of a v2 ClusterImageSetMirror or ImageSetMirror that mirrors to
// path under registry.
func Mirrors(registry, path string) map[string]any {
	return map[string]any{
		"mirrors": []any{map[string]any{"registry": registry, "path": path}},
	}
}

// V2Manager returns the manager Deployment the v2 chart deploys, under release name prefix.
func V2Manager(namespace, prefix, version string) *appsv1.Deployment {
	labels := map[string]string{
		labelName:                      chart,
		labelInstance:                  prefix,
		"app.kubernetes.io/component":  manager,
		"app.kubernetes.io/version":    version,
		"app.kubernetes.io/managed-by": "Helm",
		"control-plane":                "controller-manager",
		labelChart:                     "kube-image-keeper-" + version,
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: prefix + "-manager", Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:    manager,
				Image:   "quay.io/enix/kube-image-keeper:" + version,
				Command: []string{manager, "-metrics-bind-address=:8080", "-zap-log-level=INFO"},
			}}}},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
}

// V3Deployment returns a Deployment of the v3 chart that runs modes, ready or not.
func V3Deployment(namespace, name string, ready bool, modes ...string) *appsv1.Deployment {
	var readyReplicas int32
	if ready {
		readyReplicas = 1
	}
	command := append([]string{manager}, modes...)
	command = append(command, "-health-probe-bind-address=:8081", "-cluster-resource-namespace="+namespace)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: map[string]string{
			labelName:                     chart,
			labelInstance:                 "kuik",
			"app.kubernetes.io/component": strings.Join(modes, "-"),
			labelChart:                    "kube-image-keeper-3.0.0",
		}},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:    modes[0],
				Image:   "quay.io/enix/kube-image-keeper:3.0.0",
				Command: command,
			}}}},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: readyReplicas},
	}
}

// V2Webhook returns the MutatingWebhookConfiguration of the v2 chart, under release name
// prefix: it carries no label.
func V2Webhook(namespace, prefix string) *admissionregistrationv1.MutatingWebhookConfiguration {
	return podWebhook(namespace, prefix, nil)
}

// V3Webhook returns the MutatingWebhookConfiguration of the v3 chart, under release name
// prefix: it carries the chart's labels.
func V3Webhook(namespace, prefix string) *admissionregistrationv1.MutatingWebhookConfiguration {
	return podWebhook(namespace, prefix, map[string]string{
		labelName:     chart,
		labelInstance: prefix,
		labelChart:    "kube-image-keeper-3.0.0",
	})
}

func podWebhook(namespace, prefix string, labels map[string]string) *admissionregistrationv1.MutatingWebhookConfiguration {
	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:        prefix + "-mutating-webhook",
			Labels:      labels,
			Annotations: map[string]string{"cert-manager.io/inject-ca-from": namespace + "/" + prefix + "-serving-cert"},
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{{
			Name: "mpod-v1.kb.io",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{Service: &admissionregistrationv1.ServiceReference{
				Namespace: namespace,
				Name:      prefix + "-webhook",
				Path:      new("/mutate--v1-pod"),
			}},
			FailurePolicy:           ptr.To(admissionregistrationv1.Ignore),
			ReinvocationPolicy:      ptr.To(admissionregistrationv1.IfNeededReinvocationPolicy),
			AdmissionReviewVersions: []string{"v1"},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"pods"},
				},
			}},
		}},
	}
}

// V2PullSecret returns a pull secret v2 copied into namespace for the resource of kind and
// name, with the labels v2 sets on it.
func V2PullSecret(namespace, name, ownerKind, ownerName, ownerUID string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: map[string]string{
			"kuik.enix.io/owner-uid":     ownerUID,
			"kuik.enix.io/owner-kind":    ownerKind,
			"kuik.enix.io/owner-name":    ownerName,
			"kuik.enix.io/owner-group":   Group,
			"kuik.enix.io/owner-version": Version,
		}},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
	}
}

// ClusterIDConfigMap returns the ConfigMap the v3 reconciler keeps the cluster ID in.
func ClusterIDConfigMap(namespace, id string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "kuik-cluster-id"},
		Data:       map[string]string{"clusterID": id},
	}
}

// Controller returns the controller owner reference of an object of kind and name.
func Controller(apiVersion, kind, name string) []metav1.OwnerReference {
	return []metav1.OwnerReference{{
		APIVersion: apiVersion, Kind: kind, Name: name, UID: types.UID("uid-" + name), Controller: new(true),
	}}
}

// Template returns a pod template running containers named and imaged as given, in pairs.
func Template(initContainers, containers map[string]string) corev1.PodTemplateSpec {
	var t corev1.PodTemplateSpec
	for name, image := range initContainers {
		t.Spec.InitContainers = append(t.Spec.InitContainers, corev1.Container{Name: name, Image: image})
	}
	for name, image := range containers {
		t.Spec.Containers = append(t.Spec.Containers, corev1.Container{Name: name, Image: image})
	}
	return t
}
