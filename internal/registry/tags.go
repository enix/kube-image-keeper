package registry

import (
	"context"
	"errors"
)

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
