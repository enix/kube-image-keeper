// Package auth resolves the credentials kuik reads an image with, in the order of
// docs/v3/spec.md, "No auth at all", and derives the reserved Secret names of
// docs/v3/architecture.md.
package auth

import (
	"context"
	"fmt"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

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

// Resolver resolves credentials with the caller's client. It carries no RBAC marker: the
// callers already hold the Secret reads of docs/v3/architecture.md, "Permissions".
type Resolver struct {
	reader    client.Reader
	namespace string
	mode      Mode
	fallback  atomic.Pointer[imagepath.Trie[FallbackAuthEntry]]
}

// NewResolver returns a resolver reading Secrets with reader, `secretRef`s in namespace, the
// cluster resource namespace.
func NewResolver(reader client.Reader, namespace string, mode Mode) *Resolver {
	r := &Resolver{reader: reader, namespace: namespace, mode: mode}
	r.fallback.Store(imagepath.NewTrie[FallbackAuthEntry]())
	return r
}

// SetFallbackAuth replaces the `fallbackAuth` entries, on every config load. An invalid
// entry rejects the whole list and keeps the previous one.
func (r *Resolver) SetFallbackAuth(entries []FallbackAuthEntry) error {
	trie := imagepath.NewTrie[FallbackAuthEntry]()
	for i, e := range entries {
		p, kind, err := imagepath.ValidateEntry(e.Repository, e.RepositoryGroup)
		if err != nil {
			return fmt.Errorf("fallbackAuth[%d]: %w", i, err)
		}
		trie.Insert(p, kind, e)
	}
	r.fallback.Store(trie)
	return nil
}

// Resolve returns the credentials to try for ref, in the order of docs/v3/spec.md, "No auth
// at all": the entry's auth, the pod's pull secrets, the most specific fallbackAuth entry
// (never in webhook mode, the node does not hold it), then anonymous. entryAuth and pod may
// be nil.
//
// A declared Secret that is missing or malformed is an error, for the consumer to report. A
// pod's pull secret the API server refuses, or that is missing or malformed, is skipped, as
// the kubelet does.
func (r *Resolver) Resolve(ctx context.Context, ref imagepath.Reference, entryAuth *kuikv1alpha1.Auth, pod *corev1.Pod) ([]Credential, error) {
	var creds []Credential

	if entryAuth != nil {
		c, err := r.declared(ctx, SourceEntryAuth, entryAuth.SecretRef, entryAuth.Provider)
		if err != nil {
			return nil, err
		}
		creds = append(creds, c)
	}

	if c, ok := r.podPullSecrets(ctx, pod); ok {
		creds = append(creds, c)
	}

	if r.mode == ModeReconciler {
		if matches := r.fallback.Load().Match(ref); len(matches) > 0 {
			e := matches[0].Value
			c, err := r.declared(ctx, SourceFallbackAuth, e.SecretRef, e.Provider)
			if err != nil {
				return nil, err
			}
			creds = append(creds, c)
		}
	}

	return append(creds, Credential{Source: SourceAnonymous}), nil
}

// declared resolves a credential declared on an entry or in fallbackAuth. A provider is
// deferred (notes/0010): it resolves to a pending credential and reads nothing.
func (r *Resolver) declared(ctx context.Context, source Source, secretRef *kuikv1alpha1.SecretReference, provider *kuikv1alpha1.Provider) (Credential, error) {
	if secretRef == nil {
		if provider != nil {
			return Credential{Source: SourceProvider, Pending: true}, nil
		}
		return Credential{}, fmt.Errorf("%s declares neither secretRef nor provider", source)
	}

	var secret corev1.Secret
	err := r.reader.Get(ctx, client.ObjectKey{Namespace: r.namespace, Name: secretRef.Name}, &secret)
	if apierrors.IsNotFound(err) {
		return Credential{}, &ErrSecretNotFound{Name: secretRef.Name}
	}
	if err != nil {
		return Credential{}, fmt.Errorf("reading secret %q: %w", secretRef.Name, err)
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson {
		return Credential{}, &ErrSecretMalformed{Name: secretRef.Name}
	}
	return Credential{Source: source, Secrets: []corev1.Secret{secret}}, nil
}

// podPullSecrets reads the pod's imagePullSecrets in its namespace, keeping the ones the
// kubelet would use. It reports false when none is left.
func (r *Resolver) podPullSecrets(ctx context.Context, pod *corev1.Pod) (Credential, bool) {
	if pod == nil {
		return Credential{}, false
	}
	log := logf.FromContext(ctx)

	var secrets []corev1.Secret
	for _, ref := range pod.Spec.ImagePullSecrets {
		var secret corev1.Secret
		err := r.reader.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: ref.Name}, &secret)
		switch {
		case apierrors.IsForbidden(err):
			// Not granted in this namespace: no credential here, for every Secret of the pod.
			return Credential{}, false
		case err != nil:
			log.V(1).Info("Skipped pod pull Secret", "namespace", pod.Namespace, "name", ref.Name, "error", err.Error())
			continue
		case secret.Type != corev1.SecretTypeDockerConfigJson && secret.Type != corev1.SecretTypeDockercfg:
			log.V(1).Info("Skipped pod pull Secret", "namespace", pod.Namespace, "name", ref.Name, "type", string(secret.Type))
			continue
		}
		secrets = append(secrets, secret)
	}
	if len(secrets) == 0 {
		return Credential{}, false
	}
	return Credential{Source: SourcePodPullSecret, Secrets: secrets}, true
}
