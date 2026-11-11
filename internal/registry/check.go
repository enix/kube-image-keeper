package registry

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// errRateLimited is an answer whose rate-limit headers say the quota is exhausted.
var errRateLimited = errors.New("rate limit exhausted")

// CheckResult is what an available image answered.
type CheckResult struct {
	// Descriptor is the manifest descriptor the registry returned.
	Descriptor v1.Descriptor
	// Auth is the credential that answered.
	Auth authn.Authenticator
}

// CheckError is a failed check and the reason it reports: the reason of the first
// credential tried, the most specific one.
type CheckError struct {
	Reason kuikv1alpha1.CheckFailureReason
	Err    error
}

func (e *CheckError) Error() string { return string(e.Reason) + ": " + e.Err.Error() }
func (e *CheckError) Unwrap() error { return e.Err }

// Check sends one manifest HEAD to a source registry, by digest when the reference carries
// one, and counts it in RequestsTotal. A failure is a *CheckError.
func (c *Client) Check(ctx context.Context, image Endpoint) (*CheckResult, error) {
	return c.check(ctx, image, operationCheck)
}

// CheckCopySource is Check of the source a copy is about to read, counted with the copies
// under operation Copy: it is the first request of the copy.
func (c *Client) CheckCopySource(ctx context.Context, image Endpoint) (*CheckResult, error) {
	return c.check(ctx, image, operationCopy)
}

// CheckDestination is Check against a mirror destination, which kuik does not count.
func (c *Client) CheckDestination(ctx context.Context, image Endpoint) (*CheckResult, error) {
	return c.check(ctx, image, "")
}

// check sends the HEAD and counts it under operation, nothing when operation is empty.
func (c *Client) check(ctx context.Context, image Endpoint, operation string) (*CheckResult, error) {
	ref, err := name.ParseReference(image.Reference, image.nameOptions()...)
	if err != nil {
		return nil, err
	}
	host := registryLabel(ref)
	base := c.sourceTransport(ref.Context().Registry, image.Insecure)
	if operation == "" {
		// A mirror destination is never paced.
		base = c.transportFor(ref.Context().Registry, image.Insecure)
	}

	var descriptor *v1.Descriptor
	auth, errs := c.tryThrough(ctx, base, image.Auth, func(a attempt) error {
		d, err := remote.Head(ref, a.options...)
		if isRateLimited(a.headers.headers()) {
			err = errors.Join(errRateLimited, err)
		}
		// A request refused before it was sent is not one kuik sent.
		if operation != "" && a.headers.sent() {
			RequestsTotal.WithLabelValues(host, operation, checkResultLabel(err)).Inc()
		}
		descriptor = d
		return err
	})
	if errs != nil {
		return nil, &CheckError{Reason: checkFailureReason(errs[0]), Err: errors.Join(errs...)}
	}
	return &CheckResult{Descriptor: *descriptor, Auth: auth}, nil
}

// checkFailureReason maps what one request observed to a check reason. Rate-limit headers
// come before the status: an exhausted quota fails the check even on a 200.
func checkFailureReason(err error) kuikv1alpha1.CheckFailureReason {
	if errors.Is(err, errRateLimited) {
		return kuikv1alpha1.CheckQuotaExceeded
	}
	switch TransportStatusCode(err) {
	case http.StatusTooManyRequests:
		return kuikv1alpha1.CheckQuotaExceeded
	case http.StatusNotFound:
		return kuikv1alpha1.CheckManifestNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return kuikv1alpha1.CheckUnauthorized
	default:
		return kuikv1alpha1.CheckUnreachable
	}
}

func checkResultLabel(err error) string {
	if err == nil {
		return resultOk
	}
	return string(checkFailureReason(err))
}
