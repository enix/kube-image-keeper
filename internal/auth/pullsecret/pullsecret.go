// Package pullsecret computes the pull Secret the syncer applies for a pair (routing
// resource, namespace), as a pure function of what the informers hold. See
// docs/v3/walkthroughs/03-secret-syncer-reconciliation.md.
package pullsecret

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/routing"
)

const (
	// LabelManagedBy marks the Secrets the syncer writes, so that they are identifiable
	// without reading them.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// ManagedBy is the value of LabelManagedBy, and the field manager of the applies.
	ManagedBy = "kuik-secret-syncer"

	// GraceDuration is how long an entry that left the desired set of an OnFailure pair is
	// kept: long enough for the pods of a rollout or a scale-up during an outage to come back
	// and find the credential.
	GraceDuration = 15 * time.Minute
)

// The reasons a source credential could not be resolved, as PullSecretInjectionFailed
// reports them.
const (
	ReasonSecretNotFound  = "SecretNotFound"
	ReasonSecretMalformed = "SecretMalformed"
)

// Resource is a routing resource as the syncer reads it.
type Resource struct {
	routing.Resource
	// UID is the resource's UID, for the owner reference.
	UID types.UID
	// Policy is the resource's rewritePolicy, defaulted.
	Policy kuikv1alpha1.RewritePolicy
}

// Credential is one injecting entry of a resource: the registry path it covers and the
// Secret it is read from.
type Credential struct {
	// Path is the registry path the entry covers, the key of its auths entry.
	Path string
	// SecretRef names the source Secret in the install namespace, empty when the entry does
	// not inject.
	SecretRef string
}

// FromImageAlternative reads an ImageAlternative.
func FromImageAlternative(cr *kuikv1alpha1.ImageAlternative) Resource {
	return Resource{}
}

// FromImageMirror reads an ImageMirror.
func FromImageMirror(cr *kuikv1alpha1.ImageMirror) Resource {
	return Resource{}
}

// Needed returns the entries the pair (r, namespace) needs: under Always every injecting
// entry when the namespace is in scope, under OnFailure the injecting entries that served a
// standing rewrite on a live pod of pods, once each. pods are the pods of namespace.
func (r Resource) Needed(namespace *corev1.Namespace, pods []*corev1.Pod) []Credential {
	return nil
}

// Failure is an entry whose source credential could not be resolved.
type Failure struct {
	// Credential is the entry left out of the Secret.
	Credential Credential
	// Reason is ReasonSecretNotFound or ReasonSecretMalformed.
	Reason string
	// Message says what failed.
	Message string
}

// Sources looks a source Secret up by name in the install namespace.
type Sources func(name string) (*corev1.Secret, bool)

// Build returns the Secret to apply for the pair (r, namespace) holding entries, and the
// entries it had to leave out.
func Build(r Resource, namespace string, entries []Credential, sources Sources) (*corev1ac.SecretApplyConfiguration, []Failure) {
	return nil, nil
}

// Pair is the unit the syncer reconciles: one routing resource in one namespace.
type Pair struct {
	Resource  routing.Resource
	Namespace string
}

// Grace keeps the entries that left the desired set of a pair for GraceDuration.
type Grace struct{}

// NewGrace returns an empty grace tracker reading the time from now.
func NewGrace(now func() time.Time) *Grace {
	return &Grace{}
}

// Retain returns entries, plus the entries of pair that left them less than GraceDuration
// ago.
func (g *Grace) Retain(pair Pair, entries []Credential) []Credential {
	return nil
}
