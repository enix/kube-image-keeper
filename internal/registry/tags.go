package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ListTags lists every tag of the repository, following the registry's pagination. A
// repository the registry does not know has no tag. Destination calls are not counted.
func (c *Client) ListTags(ctx context.Context, repository Endpoint) ([]string, error) {
	repo, err := name.NewRepository(repository.Reference, repository.nameOptions()...)
	if err != nil {
		return nil, err
	}
	var tags []string
	_, errs := c.try(ctx, repository.Auth, func(a attempt) error {
		listed, err := remote.List(repo, a.options...)
		if TransportStatusCode(err) == http.StatusNotFound {
			listed, err = nil, nil
		}
		tags = listed
		return err
	})
	if errs != nil {
		return nil, errors.Join(errs...)
	}
	return tags, nil
}

// ErrDeleteUnsupported is returned when the registry refuses tag deletion.
var ErrDeleteUnsupported = errors.New("registry does not support tag deletion")

// DeleteTag deletes one tag, never the manifest it points at: a digest reference is
// refused. A tag already gone is a success. A registry answering 405, 400 or 501 does not
// support tag deletion, which is ErrDeleteUnsupported.
func (c *Client) DeleteTag(ctx context.Context, tag Endpoint) error {
	if strings.Contains(tag.Reference, "@") {
		return fmt.Errorf("refusing to delete %q: kuik deletes by tag, never by digest", tag.Reference)
	}
	ref, err := name.NewTag(tag.Reference, append(tag.nameOptions(), name.StrictValidation)...)
	if err != nil {
		return err
	}
	_, errs := c.try(ctx, tag.Auth, func(a attempt) error {
		err := remote.Delete(ref, a.options...)
		switch TransportStatusCode(err) {
		case http.StatusNotFound:
			return nil
		case http.StatusMethodNotAllowed, http.StatusBadRequest, http.StatusNotImplemented:
			return fmt.Errorf("%w: %w", ErrDeleteUnsupported, err)
		}
		return err
	})
	if errs != nil {
		return errors.Join(errs...)
	}
	return nil
}
