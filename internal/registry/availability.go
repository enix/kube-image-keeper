package registry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	corev1 "k8s.io/api/core/v1"
)

// CheckImageAvailability performs an HTTP HEAD or GET request against the registry
// and returns the availability status of the image along with any error encountered.
// The error is non-nil for all non-Available statuses and contains the underlying
// cause (e.g. HTTP status text, transport error).
//
// When resolveDigest is true and the reference is a tag, a second request (using
// the same HTTP method) is made for the manifest digest the registry advertised
// for that tag. This emulates the pull path of container runtimes (resolve tag
// to digest, then fetch the manifest by digest) and catches registries that keep
// answering tag requests while the manifest behind the digest is gone (typically
// pull-through proxies with a stale tag cache): on such registries, tag HEAD/GET
// return 200 but every node that does not already have the image in its local
// store fails to pull.
//
// Note: when resolveDigest is true the check makes two requests, which doubles
// rate-limit quota consumption for that image. Halve monitoring.registries.*.maxPerInterval
// for rate-constrained registries (e.g. docker.io) when enabling this flag.
func CheckImageAvailability(ctx context.Context, reference string, method string, timeout time.Duration, pullSecrets []corev1.Secret, resolveDigest bool) (kuikv1alpha1.ImageAvailabilityStatus, error) {
	// When resolveDigest is enabled, both the tag and the by-digest requests must
	// complete within a single shared timeout envelope so that the total wall time
	// is bounded to `timeout`, not `2 × timeout`.
	if resolveDigest && timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
		// Clear the per-call timeout on the client; the envelope context above
		// is now the sole deadline for both requests.
		timeout = 0
	}

	client := NewClient(nil, nil).
		WithTimeout(timeout).
		WithPullSecrets(pullSecrets)

	desc, headers, err := client.ReadDescriptor(ctx, method, reference)

	if IsRateLimited(headers) {
		return kuikv1alpha1.ImageAvailabilityQuotaExceeded, fmt.Errorf("rate limit exceeded")
	}

	if err != nil {
		return availabilityFromError(err)
	}

	if resolveDigest {
		return checkDigestPath(ctx, client, method, reference, desc)
	}

	return kuikv1alpha1.ImageAvailabilityAvailable, nil
}

// checkDigestPath verifies that the manifest digest advertised for a reference
// can actually be fetched by digest. References that are already digest-addressed
// are not checked again: the initial request covered the exact manifest the
// runtime will pull.
//
// The same HTTP method used for the tag request is used for the digest request so
// that registries requiring GET for manifest-by-digest are handled correctly.
func checkDigestPath(ctx context.Context, client *Client, method string, reference string, desc *v1.Descriptor) (kuikv1alpha1.ImageAvailabilityStatus, error) {
	ref, err := name.ParseReference(reference)
	if err != nil {
		// the initial request already succeeded for this reference, so this
		// should never happen; report it rather than failing open
		return kuikv1alpha1.ImageAvailabilityUnreachable, fmt.Errorf("could not parse reference: %w", err)
	}

	// Digest-addressed refs already targeted the exact manifest the runtime will
	// pull, so no second request is needed. desc == nil is a defensive fallback:
	// ReadDescriptor returned no error but no descriptor; skip digest verification
	// and treat the image as available rather than failing open on a nil dereference.
	if _, isDigest := ref.(name.Digest); isDigest || desc == nil {
		return kuikv1alpha1.ImageAvailabilityAvailable, nil
	}

	digestReference := ref.Context().Name() + "@" + desc.Digest.String()
	_, headers, err := client.ReadDescriptor(ctx, method, digestReference)

	if IsRateLimited(headers) {
		return kuikv1alpha1.ImageAvailabilityQuotaExceeded, fmt.Errorf("rate limit exceeded")
	}

	if err != nil {
		switch TransportStatusCode(err) {
		case http.StatusNotFound:
			return kuikv1alpha1.ImageAvailabilityNotFound, fmt.Errorf("tag/digest inconsistency: %q resolves to digest %s but the registry does not serve that manifest by digest, pulls from this registry will fail on any node that does not already have the image: %w", reference, desc.Digest.String(), err)
		case http.StatusMethodNotAllowed, http.StatusNotImplemented:
			// The registry supports tag lookup but not manifest-by-digest for this
			// HTTP method; the tag check already passed so the image is pullable.
			return kuikv1alpha1.ImageAvailabilityAvailable, nil
		}
		return availabilityFromError(err)
	}

	return kuikv1alpha1.ImageAvailabilityAvailable, nil
}

// availabilityFromError maps a registry transport error to an availability
// status, wrapping the underlying cause.
func availabilityFromError(err error) (kuikv1alpha1.ImageAvailabilityStatus, error) {
	switch TransportStatusCode(err) {
	case http.StatusNotFound:
		return kuikv1alpha1.ImageAvailabilityNotFound, fmt.Errorf("image not found: %w", err)
	case http.StatusUnauthorized, http.StatusForbidden:
		return kuikv1alpha1.ImageAvailabilityInvalidAuth, fmt.Errorf("authentication failed: %w", err)
	default:
		return kuikv1alpha1.ImageAvailabilityUnreachable, fmt.Errorf("registry unreachable: %w", err)
	}
}
