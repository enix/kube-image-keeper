package registry

import (
	"context"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

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
