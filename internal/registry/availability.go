package registry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// CheckImageAvailability performs an HTTP HEAD or GET request against the registry
// and returns the availability status of the image along with any error encountered.
// The error is non-nil for all non-Available statuses and contains the underlying
// cause (e.g. HTTP status text, transport error).
func CheckImageAvailability(ctx context.Context, reference string, method string, timeout time.Duration, pullSecrets []corev1.Secret) (kuikv1alpha1.ImageAvailabilityStatus, error) {
	_, headers, err := NewClient(nil, nil).
		WithTimeout(timeout).
		WithPullSecrets(pullSecrets).
		ReadDescriptor(method, reference)

	if IsRateLimited(headers) {
		return kuikv1alpha1.ImageAvailabilityQuotaExceeded, fmt.Errorf("rate limit exceeded")
	}

	if err != nil {
		switch TransportStatusCode(err) {
		case http.StatusNotFound:
			return kuikv1alpha1.ImageAvailabilityNotFound, fmt.Errorf("image not found: %w", err)
		case http.StatusUnauthorized, http.StatusForbidden:
			return kuikv1alpha1.ImageAvailabilityInvalidAuth, fmt.Errorf("authentication failed: %w", err)
		default:
			return kuikv1alpha1.ImageAvailabilityUnreachable, fmt.Errorf("registry unreachable: %w", err)
		}
	}

	return kuikv1alpha1.ImageAvailabilityAvailable, nil
}
