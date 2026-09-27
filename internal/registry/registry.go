// Package registry reads and writes OCI registries: availability checks, verbatim copies,
// tag listings and tag deletions.
package registry

import (
	"context"
	"errors"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// errNotImplemented marks the stubs of the milestone 5 outline.
var errNotImplemented = errors.New("not implemented")

// Endpoint is one reference kuik reads or writes, with what it takes to reach it.
type Endpoint struct {
	// Reference is an image reference for Check and a Copy source, a repository for a Copy
	// destination and ListTags, a tag for DeleteTag.
	Reference string
	// Insecure reaches the registry over plain HTTP.
	Insecure bool
	// Auth are the credentials to try, in order, until one answers. None means anonymous.
	Auth []authn.Authenticator
}

// Client talks to registries. One Client is safe for concurrent use.
type Client struct{}

// NewClient returns a Client.
func NewClient() *Client {
	return &Client{}
}

// WithTimeout bounds ctx by timeout, or leaves it unbounded when timeout is 0.
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

// CheckResult is what an available image answered.
type CheckResult struct {
	// Descriptor is the manifest descriptor the registry returned.
	Descriptor v1.Descriptor
	// Auth is the credential that answered.
	Auth authn.Authenticator
}

// CheckError is a failed check and the reason it reports.
type CheckError struct {
	Reason kuikv1alpha1.CheckFailureReason
	Err    error
}

func (e *CheckError) Error() string { return string(e.Reason) + ": " + e.Err.Error() }
func (e *CheckError) Unwrap() error { return e.Err }

// Check sends one manifest HEAD to a source registry. A failure is a *CheckError.
func (c *Client) Check(ctx context.Context, image Endpoint) (*CheckResult, error) {
	return nil, errNotImplemented
}

// CheckDestination is Check against a mirror destination, which kuik does not count.
func (c *Client) CheckDestination(ctx context.Context, image Endpoint) (*CheckResult, error) {
	return nil, errNotImplemented
}

// CopyError is a failed copy and the reason it reports.
type CopyError struct {
	Reason kuikv1alpha1.CopyFailureReason
	Err    error
}

func (e *CopyError) Error() string { return string(e.Reason) + ": " + e.Err.Error() }
func (e *CopyError) Unwrap() error { return e.Err }

// Copy copies src verbatim into the repository dst under every tag of tags and returns the
// digest it copied. A failure is a *CopyError.
func (c *Client) Copy(ctx context.Context, src, dst Endpoint, tags []string) (v1.Hash, error) {
	return v1.Hash{}, errNotImplemented
}

// ListTags lists every tag of the repository.
func (c *Client) ListTags(ctx context.Context, repository Endpoint) ([]string, error) {
	return nil, errNotImplemented
}

// ErrDeleteUnsupported is returned when the registry refuses tag deletion.
var ErrDeleteUnsupported = errors.New("registry does not support tag deletion")

// DeleteTag deletes one tag, never the manifest it points at.
func (c *Client) DeleteTag(ctx context.Context, tag Endpoint) error {
	return errNotImplemented
}
