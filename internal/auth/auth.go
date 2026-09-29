// Package auth resolves the credentials kuik reads an image with, in the order of
// docs/v3/spec.md, "No auth at all", and derives the reserved Secret names of
// docs/v3/architecture.md.
package auth

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// errNotImplemented is returned by the stubs until the package is implemented.
var errNotImplemented = errors.New("not implemented")

// FallbackAuthEntry is one entry of the global config's `fallbackAuth`: a `repository` or a
// `repositoryGroup`, and a `secretRef` or a `provider`, with no `auth` wrapper.
type FallbackAuthEntry struct {
	Repository      string                        `json:"repository,omitempty"`
	RepositoryGroup string                        `json:"repositoryGroup,omitempty"`
	SecretRef       *kuikv1alpha1.SecretReference `json:"secretRef,omitempty"`
	Provider        *kuikv1alpha1.Provider        `json:"provider,omitempty"`
}

// Source is the step of the resolution a credential comes from.
type Source string

const (
	// SourceEntryAuth is the `auth` of the entry that routes the image.
	SourceEntryAuth Source = "EntryAuth"
	// SourcePodPullSecret is the pod's `imagePullSecrets`.
	SourcePodPullSecret Source = "PodPullSecret"
	// SourceFallbackAuth is the most specific matching `fallbackAuth` entry.
	SourceFallbackAuth Source = "FallbackAuth"
	// SourceProvider is a cloud identity, from the entry's `auth` or from `fallbackAuth`.
	SourceProvider Source = "Provider"
	// SourceAnonymous is no credential at all.
	SourceAnonymous Source = "Anonymous"
)

// Credential is what one step of the resolution produced.
type Credential struct {
	// Source is the step it comes from.
	Source Source
	// Secrets are the docker-registry Secrets selected at that step, empty for anonymous.
	Secrets []corev1.Secret
	// Pending is true for a provider whose Secret is not materialised.
	Pending bool
}

// Mode decides which steps the resolution runs.
type Mode int

const (
	// ModeReconciler runs every step: the background checks and copies.
	ModeReconciler Mode = iota
	// ModeWebhook skips fallbackAuth: the active check predicts what the node can pull.
	ModeWebhook
)

// ErrSecretNotFound is a declared Secret missing from the cluster resource namespace.
type ErrSecretNotFound struct {
	Name string
}

func (e *ErrSecretNotFound) Error() string {
	return fmt.Sprintf("secret %q not found", e.Name)
}

// ErrSecretMalformed is a declared Secret that is not a docker-registry Secret.
type ErrSecretMalformed struct {
	Name string
}

func (e *ErrSecretMalformed) Error() string {
	return fmt.Sprintf("secret %q is not a docker-registry secret", e.Name)
}

// Resolver resolves credentials with the caller's client.
type Resolver struct{}

// NewResolver returns a resolver reading Secrets with reader, `secretRef`s in namespace.
func NewResolver(reader client.Reader, namespace string, mode Mode) *Resolver {
	return &Resolver{}
}

// SetFallbackAuth replaces the `fallbackAuth` entries, on every config load.
func (r *Resolver) SetFallbackAuth(entries []FallbackAuthEntry) error {
	return errNotImplemented
}

// Resolve returns the credentials to try for ref, in order, anonymous last.
func (r *Resolver) Resolve(ctx context.Context, ref imagepath.Reference, entryAuth *kuikv1alpha1.Auth, pod *corev1.Pod) ([]Credential, error) {
	return nil, errNotImplemented
}
