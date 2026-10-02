// Package pullsecret computes the pull Secret the syncer applies for a pair (routing
// resource, namespace), as a pure function of what the informers hold. See
// docs/v3/walkthroughs/03-secret-syncer-reconciliation.md.
package pullsecret

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
	"github.com/enix/kube-image-keeper/internal/registry/credentialprovider/secrets"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
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

	// namespaces is the namespaceSelector; nil when it does not parse, selecting nothing.
	namespaces labels.Selector
	// credentials holds one item per entry, in declared order; an entry that does not
	// inject has an empty SecretRef.
	credentials []Credential
	// entries indexes the credentials of an ImageAlternative by their path.
	entries *imagepath.Trie[int]
	// destination is the destination.path of an ImageMirror.
	destination string
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

// FromImageAlternative reads an ImageAlternative. An entry that does not validate is left
// out, as the webhook leaves it out.
func FromImageAlternative(cr *kuikv1alpha1.ImageAlternative) Resource {
	r := Resource{
		Resource:   routing.Resource{Kind: routing.KindImageAlternative, Name: cr.Name},
		UID:        cr.UID,
		Policy:     policyOrDefault(cr.Spec.RewritePolicy),
		namespaces: selector(cr.Spec.NamespaceSelector),
		entries:    imagepath.NewTrie[int](),
	}
	for _, a := range cr.Spec.Alternatives {
		path, kind, err := imagepath.ValidateEntry(a.Repository, a.RepositoryGroup)
		if err != nil {
			continue
		}
		r.entries.Insert(path, kind, len(r.credentials))
		r.credentials = append(r.credentials, credential(a.Path(), a.Auth, a.Unavailable))
	}
	return r
}

// FromImageMirror reads an ImageMirror: its one injectable entry is destination.pull, keyed
// by destination.path without its trailing slash. destination.manage never injects.
func FromImageMirror(cr *kuikv1alpha1.ImageMirror) Resource {
	r := Resource{
		Resource:    routing.Resource{Kind: routing.KindImageMirror, Name: cr.Name},
		UID:         cr.UID,
		Policy:      policyOrDefault(cr.Spec.RewritePolicy),
		namespaces:  selector(cr.Spec.NamespaceSelector),
		destination: cr.Spec.Destination.Path,
	}
	if pull := cr.Spec.Destination.Pull; pull != nil {
		r.credentials = []Credential{credential(strings.TrimSuffix(cr.Spec.Destination.Path, "/"), &pull.Auth, false)}
	}
	return r
}

// credential is the credential of an entry, with no SecretRef when it does not inject: no
// auth, injectPullSecret false, a provider (deferred), or an entry never offered.
func credential(path string, a *kuikv1alpha1.Auth, unavailable bool) Credential {
	c := Credential{Path: path}
	if a.InjectsPullSecret() && a.SecretRef != nil && !unavailable {
		c.SecretRef = a.SecretRef.Name
	}
	return c
}

func policyOrDefault(policy kuikv1alpha1.RewritePolicy) kuikv1alpha1.RewritePolicy {
	if policy == "" {
		return kuikv1alpha1.RewritePolicyOnFailure
	}
	return policy
}

// selector parses a namespaceSelector: absent selects every namespace, invalid none.
func selector(s *metav1.LabelSelector) labels.Selector {
	if s == nil {
		return labels.Everything()
	}
	parsed, err := metav1.LabelSelectorAsSelector(s)
	if err != nil {
		return nil
	}
	return parsed
}

// Needed returns the entries the pair (r, namespace) needs: under Always every injecting
// entry when the namespace is in scope, under OnFailure the injecting entries that served a
// standing rewrite on a live pod of pods, once each. pods are the pods of namespace.
func (r Resource) Needed(namespace *corev1.Namespace, pods []*corev1.Pod) []Credential {
	switch r.Policy {
	case kuikv1alpha1.RewritePolicyAlways:
		if r.namespaces == nil || !r.namespaces.Matches(labels.Set(namespace.Labels)) {
			return nil
		}
		var needed []Credential
		for _, c := range r.credentials {
			if c.SecretRef != "" {
				needed = append(needed, c)
			}
		}
		return needed
	case kuikv1alpha1.RewritePolicyOnFailure:
		return r.served(pods)
	case kuikv1alpha1.RewritePolicyNone:
		// A mirror that only copies routes nothing, so nothing pulls from it.
		return nil
	default:
		return nil
	}
}

// served returns the injecting entries the standing rewrites of r on the live pods placed.
func (r Resource) served(pods []*corev1.Pod) []Credential {
	var served []Credential
	for _, pod := range pods {
		if !attribution.Live(pod) {
			continue
		}
		for _, rewrite := range podrecord.Standing(pod) {
			by, err := routing.ParseResource(rewrite.By)
			if err != nil || by != r.Resource {
				continue
			}
			c, ok := r.entryOf(rewrite.RewrittenTo)
			if ok && c.SecretRef != "" && !slices.Contains(served, c) {
				served = append(served, c)
			}
		}
	}
	return served
}

// entryOf finds the entry of r a rewritten reference was placed under: the most specific
// matching entry of an ImageAlternative, the destination of an ImageMirror.
func (r Resource) entryOf(rewrittenTo string) (Credential, bool) {
	ref, err := imagepath.Parse(rewrittenTo)
	if err != nil {
		return Credential{}, false
	}
	if r.entries == nil {
		if len(r.credentials) == 0 || !mirrorpath.UnderDestination(r.destination, ref) {
			return Credential{}, false
		}
		return r.credentials[0], true
	}
	matches := r.entries.Match(ref)
	if len(matches) == 0 {
		return Credential{}, false
	}
	return r.credentials[matches[0].Value], true
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

// dockerConfigJSON is the content of a kubernetes.io/dockerconfigjson Secret.
type dockerConfigJSON struct {
	Auths map[string]authEntry `json:"auths"`
}

type authEntry struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

// Build returns the Secret to apply for the pair (r, namespace) holding entries, and the
// entries it had to leave out. Each auths entry is keyed by the path of its entry and holds
// the credential the kubelet would pick from the source Secret for that path.
func Build(r Resource, namespace string, entries []Credential, sources Sources) (*corev1ac.SecretApplyConfiguration, []Failure) {
	config := dockerConfigJSON{Auths: map[string]authEntry{}}
	var failures []Failure
	for _, c := range entries {
		entry, failure := resolve(c, sources)
		if failure != nil {
			failures = append(failures, *failure)
			continue
		}
		config.Auths[c.Path] = entry
	}
	// A map of plain strings: marshalling cannot fail.
	data, _ := json.Marshal(config)

	secret := corev1ac.Secret(auth.InjectedSecretName(r.Kind, r.Name), namespace).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithLabels(map[string]string{LabelManagedBy: ManagedBy}).
		WithOwnerReferences(metav1ac.OwnerReference().
			WithAPIVersion(kuikv1alpha1.GroupVersion.String()).
			WithKind(r.Kind).
			WithName(r.Name).
			WithUID(r.UID)).
		WithData(map[string][]byte{corev1.DockerConfigJsonKey: data})
	return secret, failures
}

// resolve reads the credential the kubelet would use for c.Path from its source Secret.
func resolve(c Credential, sources Sources) (authEntry, *Failure) {
	fail := func(reason, format string, args ...any) (authEntry, *Failure) {
		return authEntry{}, &Failure{Credential: c, Reason: reason, Message: fmt.Sprintf(format, args...)}
	}

	source, ok := sources(c.SecretRef)
	if !ok {
		return fail(ReasonSecretNotFound, "secret %q not found", c.SecretRef)
	}
	if source.Type != corev1.SecretTypeDockerConfigJson {
		return fail(ReasonSecretMalformed, "secret %q is not a docker-registry secret", c.SecretRef)
	}
	keyring, err := secrets.MakeDockerKeyring([]corev1.Secret{*source})
	if err != nil || keyring == nil {
		return fail(ReasonSecretMalformed, "secret %q holds no docker config", c.SecretRef)
	}
	creds, _ := keyring.Lookup(c.Path)
	if len(creds) == 0 {
		return fail(ReasonSecretMalformed, "secret %q holds no credential for %s", c.SecretRef, c.Path)
	}
	// The keyring lists the most specific credential first, the one the kubelet tries first.
	picked := creds[0]
	return authEntry{
		Username: picked.Username,
		Password: picked.Password,
		Email:    picked.Email,
		Auth:     base64.StdEncoding.EncodeToString([]byte(picked.Username + ":" + picked.Password)),
	}, nil
}

// Pair is the unit the syncer reconciles: one routing resource in one namespace.
type Pair struct {
	Resource  routing.Resource
	Namespace string
}

// Grace keeps the entries that left the desired set of a pair for GraceDuration. It lives in
// memory only: losing it drops the entries in their grace at the next reconcile.
type Grace struct {
	now func() time.Time

	mu       sync.Mutex
	lastSeen map[Pair]map[Credential]time.Time
}

// NewGrace returns an empty grace tracker reading the time from now.
func NewGrace(now func() time.Time) *Grace {
	return &Grace{now: now, lastSeen: map[Pair]map[Credential]time.Time{}}
}

// Retain returns entries, plus the entries of pair that left them less than GraceDuration
// ago.
func (g *Grace) Retain(pair Pair, entries []Credential) []Credential {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	seen := g.lastSeen[pair]
	if seen == nil {
		seen = map[Credential]time.Time{}
		g.lastSeen[pair] = seen
	}
	for _, c := range entries {
		seen[c] = now
	}
	retained := slices.Clone(entries)
	for c, at := range seen {
		switch {
		case slices.Contains(entries, c):
		case now.Sub(at) < GraceDuration:
			retained = append(retained, c)
		default:
			delete(seen, c)
		}
	}
	if len(seen) == 0 {
		delete(g.lastSeen, pair)
	}
	return retained
}

// Forget drops what Grace holds for pair, once the pair no longer exists.
func (g *Grace) Forget(pair Pair) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.lastSeen, pair)
}
