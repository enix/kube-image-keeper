// Package inventory reads what a cluster holds of kuik v2 and v3, for the migration from one
// to the other. It only reads: Secrets by their metadata, every other object through the API.
package inventory

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Options tells Read where v3 lives.
type Options struct {
	// Namespace is the namespace kuik v3 is installed in.
	Namespace string
}

// Inventory is what Read found of kuik v2 and v3.
type Inventory struct {
	V2          V2Installation `json:"v2"`
	V2CRDs      []CRD          `json:"v2CRDs,omitempty"`
	V2Resources []Resource     `json:"v2Resources,omitempty"`
	// Pods are the pods that run a container v2 rewrote or reference a v2 pull secret.
	Pods        []Pod          `json:"pods,omitempty"`
	PullSecrets []PullSecret   `json:"pullSecrets,omitempty"`
	V3          V3Installation `json:"v3"`
}

// V2Installation is the v2 controller and its webhook configuration.
type V2Installation struct {
	// Namespace is the namespace v2 runs, or ran, in.
	Namespace string `json:"namespace,omitempty"`
	// Deployment is the name of the manager Deployment, empty when it is gone.
	Deployment string `json:"deployment,omitempty"`
	// Version is the version of v2, empty when the manager Deployment is gone.
	Version string `json:"version,omitempty"`
	// WebhookConfiguration is the name of the v2 MutatingWebhookConfiguration, empty when it
	// is gone.
	WebhookConfiguration string `json:"webhookConfiguration,omitempty"`
}

// Running reports whether the v2 manager Deployment is there.
func (v V2Installation) Running() bool { return false }

// CRD is a v2 CustomResourceDefinition left on the cluster.
type CRD struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Namespaced bool   `json:"namespaced,omitempty"`
	// Prerelease is true for the CRDs of 2.0 prereleases, which 2.3 no longer ships.
	Prerelease bool `json:"prerelease,omitempty"`
}

// Resource is a v2 resource with its spec and status, as read.
type Resource struct {
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace,omitempty"`
	Name       string         `json:"name"`
	Finalizers []string       `json:"finalizers,omitempty"`
	Spec       map[string]any `json:"spec,omitempty"`
	Status     map[string]any `json:"status,omitempty"`
}

// Pod is a pod that runs a container v2 rewrote or references a v2 pull secret.
type Pod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Owner is the workload the pod belongs to, nil for a bare pod.
	Owner *Owner `json:"owner,omitempty"`
	// Containers are the containers whose image v2 rewrote.
	Containers []Container `json:"containers,omitempty"`
	// PullSecrets are the v2 pull secrets the pod references.
	PullSecrets []string `json:"pullSecrets,omitempty"`
}

// Owner is the workload a pod belongs to: a ReplicaSet resolves to its Deployment.
type Owner struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// OriginSource tells where the origin of a container comes from.
type OriginSource string

const (
	// FromAnnotation is the kuik.enix.io/original-images annotation v2 sets on a pod.
	FromAnnotation OriginSource = "annotation"
	// FromTemplate is the pod template of the pod's owner.
	FromTemplate OriginSource = "template"
)

// Container is a container v2 rewrote.
type Container struct {
	Name string `json:"name"`
	Init bool   `json:"init,omitempty"`
	// Image is the image the container runs, under a v2 mirror path.
	Image string `json:"image"`
	// Origin is the canonical reference v2 rewrote, empty when nothing records it.
	Origin     string       `json:"origin,omitempty"`
	OriginFrom OriginSource `json:"originFrom,omitempty"`
	// TemplateOrigin is the canonical image of the owner's template when it disagrees with
	// the annotation, empty otherwise.
	TemplateOrigin string `json:"templateOrigin,omitempty"`
}

// PullSecret is a pull secret v2 copied into a namespace.
type PullSecret struct {
	Namespace string          `json:"namespace"`
	Name      string          `json:"name"`
	Owner     PullSecretOwner `json:"owner"`
}

// PullSecretOwner is the v2 resource a pull secret was copied for, from its labels.
type PullSecretOwner struct {
	UID  string `json:"uid"`
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

// V3Installation is what runs of kuik v3.
type V3Installation struct {
	// Namespace is the namespace v3 is read in.
	Namespace string `json:"namespace"`
	// ClusterID is the cluster ID, empty when the ConfigMap is missing or invalid.
	ClusterID string `json:"clusterID,omitempty"`
	// ClusterIDError says why the stored cluster ID is invalid.
	ClusterIDError string `json:"clusterIDError,omitempty"`
	Webhook        Mode   `json:"webhook"`
	Reconciler     Mode   `json:"reconciler"`
	SecretSyncer   Mode   `json:"secretSyncer"`
	// WebhookConfiguration is the name of the v3 MutatingWebhookConfiguration, empty when
	// there is none.
	WebhookConfiguration string       `json:"webhookConfiguration,omitempty"`
	Resources            []V3Resource `json:"resources,omitempty"`
}

// Combined reports whether one Deployment runs several modes.
func (v V3Installation) Combined() bool {
	return false
}

// Mode is one of the three v3 modes.
type Mode struct {
	// Deployment is the Deployment that runs the mode, empty when none does.
	Deployment string `json:"deployment,omitempty"`
	// Ready is true when a pod of the Deployment is ready.
	Ready bool `json:"ready,omitempty"`
}

// V3Resource is a v3 resource and its Ready condition.
type V3Resource struct {
	Kind    string                 `json:"kind"`
	Name    string                 `json:"name"`
	Ready   metav1.ConditionStatus `json:"ready,omitempty"`
	Reason  string                 `json:"reason,omitempty"`
	Message string                 `json:"message,omitempty"`
	// MissingImages is true for an ImageMirror whose destination misses images.
	MissingImages bool `json:"missingImages,omitempty"`
}

// Read reads the cluster through r.
func Read(ctx context.Context, r client.Reader, opts Options) (*Inventory, error) {
	return &Inventory{}, nil
}
