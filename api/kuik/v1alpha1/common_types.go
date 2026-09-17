package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RepositoryPathPattern validates a fully qualified repository path with no tag and no
// digest: a registry host (with an optional port) followed by zero or more path components
// in the grammar of the OCI Distribution spec. The host is recognised the way the container
// runtime does: it contains a dot, carries a port, or is `localhost`. A host alone is a valid
// path: it is what `repositoryGroup: docker.io` and a `destination.path` without a
// sub-path mean.
//
// RepositoryPattern is the same grammar with at least one path component: a `repository`
// names one image with all its tags (`docker.io/library/nginx`), and a host alone is not one.
//
// Keep both in sync with the `+kubebuilder:validation:Pattern` markers that inline them: a
// marker cannot reference a Go constant.
const (
	RepositoryPathPattern = `^(localhost(:[0-9]+)?|[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+(:[0-9]+)?|[a-zA-Z0-9-]+:[0-9]+)(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*$`
	RepositoryPattern     = `^(localhost(:[0-9]+)?|[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+(:[0-9]+)?|[a-zA-Z0-9-]+:[0-9]+)(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)+$`
)

// RewritePolicy says where a resource's candidates sit relative to the original image in the
// candidate list the webhook probes.
type RewritePolicy string

const (
	// RewritePolicyOnFailure tries the original image first and the resource's candidates as
	// fallbacks.
	RewritePolicyOnFailure RewritePolicy = "OnFailure"
	// RewritePolicyAlways tries the resource's candidates ahead of the original image.
	RewritePolicyAlways RewritePolicy = "Always"
	// RewritePolicyNone never routes to the resource's candidates. Only meaningful on an
	// ImageMirror, which then copies without ever rewriting.
	RewritePolicyNone RewritePolicy = "None"
)

// ProviderName is a cloud identity kuik can authenticate to a registry with.
// +kubebuilder:validation:Enum=aws;gcp;azure
type ProviderName string

const (
	ProviderAWS   ProviderName = "aws"
	ProviderGCP   ProviderName = "gcp"
	ProviderAzure ProviderName = "azure"
)

// SecretReference names a docker-registry Secret. It carries no namespace: it always resolves
// in kuik's install namespace, the cluster resource namespace.
type SecretReference struct {
	// name of the Secret, in kuik's install namespace.
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
}

// ServiceAccountReference names a ServiceAccount whose token a provider requests, so that a
// resource carries its own cloud role instead of the controller's.
type ServiceAccountReference struct {
	// name of the ServiceAccount, in kuik's install namespace.
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
}

// Provider is an ambient cloud identity used to authenticate to a registry.
type Provider struct {
	// name of the cloud provider.
	// +required
	Name ProviderName `json:"name"`

	// serviceAccountRef requests a token for that ServiceAccount instead of using the
	// controller's own identity.
	// +optional
	ServiceAccountRef *ServiceAccountReference `json:"serviceAccountRef,omitempty"`
}

// Auth is the credential kuik reads a registry with. It is a discriminated union: exactly one
// of secretRef or provider is set.
// +kubebuilder:validation:XValidation:rule="has(self.secretRef) != has(self.provider)",message="exactly one of secretRef or provider must be set"
type Auth struct {
	// secretRef names a docker-registry Secret in kuik's install namespace.
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// provider is an ambient cloud identity (aws, gcp or azure).
	// +optional
	Provider *Provider `json:"provider,omitempty"`

	// injectPullSecret copies a pull secret into the namespaces of the pods this credential
	// serves, so the kubelet can pull the image. Defaults to true with secretRef and to false
	// with provider; with a provider and true, kuik materialises and renews a docker-registry
	// Secret from the provider's short-lived token.
	// +optional
	InjectPullSecret *bool `json:"injectPullSecret,omitempty"`
}

// InjectsPullSecret returns whether the credential is copied into pod namespaces, applying the
// asymmetric default: true for a secretRef, false for a provider.
func (a *Auth) InjectsPullSecret() bool {
	if a == nil {
		return false
	}
	if a.InjectPullSecret != nil {
		return *a.InjectPullSecret
	}
	return a.SecretRef != nil
}

// Alternative is one entry of an ImageAlternative's list: a repository, or every repository
// under a path, that holds the same images as the other entries of the list.
// +kubebuilder:validation:XValidation:rule="has(self.repository) != has(self.repositoryGroup)",message="exactly one of repository or repositoryGroup must be set"
type Alternative struct {
	// repository matches that exact repository, whatever the tag or digest, and rewrites to it
	// preserving the tag or digest. Fully qualified, hostname included, with at least one path
	// component and no tag and no digest: `docker.io/library/nginx`, never `docker.io` alone.
	// +kubebuilder:validation:Pattern=`^(localhost(:[0-9]+)?|[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+(:[0-9]+)?|[a-zA-Z0-9-]+:[0-9]+)(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)+$`
	// +optional
	Repository string `json:"repository,omitempty"`

	// repositoryGroup matches every repository located under that path, at any depth, and
	// rewrites to it preserving the remainder of the path and the tag or digest. Fully
	// qualified, hostname included, with no tag and no digest.
	// +kubebuilder:validation:Pattern=`^(localhost(:[0-9]+)?|[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+(:[0-9]+)?|[a-zA-Z0-9-]+:[0-9]+)(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*$`
	// +optional
	RepositoryGroup string `json:"repositoryGroup,omitempty"`

	// insecure makes kuik probe and pull this registry over HTTP. The container runtime of the
	// nodes must be configured for it separately.
	// +optional
	Insecure bool `json:"insecure,omitempty"`

	// unavailable declares that this source is gone: the entry still matches images, so the
	// resource applies to them, but it is never offered as a candidate, never read as a copy
	// source and never monitored. An original image matching such an entry is tried last.
	// +optional
	Unavailable bool `json:"unavailable,omitempty"`

	// auth is the credential kuik and, when injected, the kubelet pull this repository with.
	// +optional
	Auth *Auth `json:"auth,omitempty"`
}

// Path returns the repository or repositoryGroup value, whichever is set.
func (a *Alternative) Path() string {
	if a.Repository != "" {
		return a.Repository
	}
	return a.RepositoryGroup
}

// IsGroup reports whether the entry is a repositoryGroup.
func (a *Alternative) IsGroup() bool {
	return a.RepositoryGroup != ""
}

// Condition types. Ready is the only one that is True when things are well; every other one
// names an anomaly and stays True as long as it lasts.
const (
	// ConditionReady is True when the resource is usable as declared.
	ConditionReady = "Ready"
	// ConditionListCapacityPressure is True when a capped status list is approaching or over
	// its limit.
	ConditionListCapacityPressure = "ListCapacityPressure"
	// ConditionFallbackActive is True when a rewrite is standing in for an origin that failed.
	ConditionFallbackActive = "FallbackActive"
	// ConditionAlternativesExhausted is True when a container was left untouched, no candidate
	// having answered.
	ConditionAlternativesExhausted = "AlternativesExhausted"
	// ConditionDestinationOutOfSync is True when an ImageMirror's destination does not hold the
	// desired state yet.
	ConditionDestinationOutOfSync = "DestinationOutOfSync"
	// ConditionImagesUnavailable is True when a tracked origin fails its check.
	ConditionImagesUnavailable = "ImagesUnavailable"
	// ConditionAlternativesUnavailable is True when a monitored alternative fails its check.
	ConditionAlternativesUnavailable = "AlternativesUnavailable"
	// ConditionImagesDrifted is True when a tracked tag moved upstream.
	ConditionImagesDrifted = "ImagesDrifted"
)

// Condition reasons, about a resource rather than about a request.
const (
	ReasonIsReady                   = "IsReady"
	ReasonInvalidConfig             = "InvalidConfig"
	ReasonSecretNotFound            = "SecretNotFound"
	ReasonSecretMalformed           = "SecretMalformed"
	ReasonTokenRequestFailed        = "TokenRequestFailed"
	ReasonRegistryDeleteUnsupported = "RegistryDeleteUnsupported"
	ReasonListNearCapacity          = "ListNearCapacity"
	ReasonListTruncated             = "ListTruncated"
	ReasonOriginUnavailable         = "OriginUnavailable"
	ReasonAllCandidatesFailed       = "AllCandidatesFailed"
	ReasonMissingImages             = "MissingImages"
	ReasonChecksFailed              = "ChecksFailed"
	ReasonUpstreamDigestMoved       = "UpstreamDigestMoved"
)

// CheckFailureReason is what a failed availability check observed on one image.
// +kubebuilder:validation:Enum=ManifestNotFound;Unauthorized;QuotaExceeded;Unreachable
type CheckFailureReason string

const (
	// CheckManifestNotFound is a 404 on that manifest.
	CheckManifestNotFound CheckFailureReason = "ManifestNotFound"
	// CheckUnauthorized is a 401 or 403 on that request.
	CheckUnauthorized CheckFailureReason = "Unauthorized"
	// CheckQuotaExceeded is a 429 on that request, or exhausted rate-limit headers.
	CheckQuotaExceeded CheckFailureReason = "QuotaExceeded"
	// CheckUnreachable means the endpoint did not answer at all: DNS, connection, TLS, timeout.
	CheckUnreachable CheckFailureReason = "Unreachable"
)

// CopyFailureReason is what a failed copy observed on one image. A copy touches two endpoints,
// so where both sides can fail the same way the reason names the side that did.
// +kubebuilder:validation:Enum=SourceNotFound;PushRejected;Unauthorized;QuotaExceeded;SourceUnreachable;DestinationUnreachable
type CopyFailureReason string

const (
	// CopySourceNotFound is a 404 on the manifest at every source.
	CopySourceNotFound CopyFailureReason = "SourceNotFound"
	// CopyPushRejected means the destination refused that manifest: size, media type, policy.
	CopyPushRejected CopyFailureReason = "PushRejected"
	// CopyUnauthorized is a 401 or 403 on that request.
	CopyUnauthorized CopyFailureReason = "Unauthorized"
	// CopyQuotaExceeded is a 429 on that request.
	CopyQuotaExceeded CopyFailureReason = "QuotaExceeded"
	// CopySourceUnreachable means the source did not answer at all.
	CopySourceUnreachable CopyFailureReason = "SourceUnreachable"
	// CopyDestinationUnreachable means the destination did not answer at all.
	CopyDestinationUnreachable CopyFailureReason = "DestinationUnreachable"
)

// PodCounts are gauges on the living pods a resource selects. Neither field sums across
// resources: a pod selected by two resources counts in both.
type PodCounts struct {
	// tracked counts the pods selected by podSelector and namespaceSelector.
	Tracked int32 `json:"tracked"`

	// rewritten counts the pods carrying at least one container this resource rewrote whose
	// rewrite still stands.
	Rewritten int32 `json:"rewritten"`
}

// ContainerCounts partition the containers of the pods a resource selects into five states.
// rewritten, conceded and stale are attributed to exactly one resource and sum across them.
type ContainerCounts struct {
	// tracked is untouched + rewritten + conceded + stale + noAlternatives.
	Tracked int32 `json:"tracked"`

	// untouched counts the containers no kuik annotation names: the original answered.
	Untouched int32 `json:"untouched"`

	// rewritten counts the containers this resource rewrote, under either policy.
	Rewritten int32 `json:"rewritten"`

	// conceded counts the containers another mutating webhook rewrote after kuik did.
	Conceded int32 `json:"conceded"`

	// stale counts the containers edited after admission, whose record no longer describes
	// what they run. Counted out of rewritten, never alongside it.
	Stale int32 `json:"stale"`

	// noAlternatives counts the containers left untouched because no candidate answered.
	NoAlternatives int32 `json:"noAlternatives"`
}

// ActiveFallback is an origin image the resource is currently standing in for.
type ActiveFallback struct {
	// image is the origin reference.
	Image string `json:"image"`

	// rewrittenTo is the candidate serving in its place.
	RewrittenTo string `json:"rewrittenTo"`

	// pods counts the pods currently carrying the rewrite.
	Pods int32 `json:"pods"`

	// since is when the fallback was first observed. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`
}

// NoAlternative is an image no candidate of this resource could serve.
type NoAlternative struct {
	// image is the origin reference.
	Image string `json:"image"`

	// pods counts the pods left untouched for it.
	Pods int32 `json:"pods"`

	// since is when it was first observed. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`
}

// ReplacedRewrite is a rewrite this resource made and something replaced: another mutating
// webhook at admission (conceded), or an edit after admission (stale).
type ReplacedRewrite struct {
	// image is the origin reference.
	Image string `json:"image"`

	// rewrittenTo is the reference kuik had placed.
	RewrittenTo string `json:"rewrittenTo"`

	// replacedBy is the reference the pod actually runs now.
	ReplacedBy string `json:"replacedBy"`

	// pods counts the pods concerned.
	Pods int32 `json:"pods"`

	// since is when it was first observed. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`
}

// RoutingStatus is the status of the routing side of a resource, shared field for field by
// ImageAlternative and ImageMirror.
type RoutingStatus struct {
	// pods are gauges on the living pods this resource selects.
	// +optional
	Pods *PodCounts `json:"pods,omitempty"`

	// containers partition the containers of the selected pods by what happened to them.
	// +optional
	Containers *ContainerCounts `json:"containers,omitempty"`

	// activeFallbacks lists the origin images this resource is currently standing in for.
	// Only populated under rewritePolicy OnFailure. Capped, see ListCapacityPressure.
	// +optional
	// +listType=atomic
	ActiveFallbacks []ActiveFallback `json:"activeFallbacks,omitempty"`

	// noAlternatives lists the images no candidate could serve, that were left untouched.
	// Capped, see ListCapacityPressure.
	// +optional
	// +listType=atomic
	NoAlternatives []NoAlternative `json:"noAlternatives,omitempty"`

	// concededRewrites lists the rewrites another mutating webhook overwrote at admission.
	// Capped, see ListCapacityPressure.
	// +optional
	// +listType=atomic
	ConcededRewrites []ReplacedRewrite `json:"concededRewrites,omitempty"`

	// staleRewrites lists the rewrites something replaced after admission. Rolling the workload
	// sends the pod back through admission. Capped, see ListCapacityPressure.
	// +optional
	// +listType=atomic
	StaleRewrites []ReplacedRewrite `json:"staleRewrites,omitempty"`
}

// RegistryCheck is the health of the check schedule of one (resource, registry host) ring.
type RegistryCheck struct {
	// registry is the host the ring reads from.
	Registry string `json:"registry"`

	// images is the size of the ring.
	Images int32 `json:"images"`

	// cursor is the last reference checked; the ring resumes at its successor on restart.
	// +optional
	Cursor string `json:"cursor,omitempty"`

	// cycleStarted is when the current lap started.
	// +optional
	CycleStarted *metav1.Time `json:"cycleStarted,omitempty"`

	// cycleDuration is the measured duration of the last completed lap, hence how often each
	// image of this host comes back. Absent until a first lap completes.
	// +optional
	CycleDuration *metav1.Duration `json:"cycleDuration,omitempty"`
}

// ChecksStatus is the health of the paced checks a resource runs against source registries.
type ChecksStatus struct {
	// registries holds one ring per source host. A mirror destination never appears here.
	// +optional
	// +listType=map
	// +listMapKey=registry
	Registries []RegistryCheck `json:"registries,omitempty"`
}
