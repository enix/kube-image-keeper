package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
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
	_, errs := c.tryThrough(ctx, c.transportFor(repo.Registry, repository.Insecure), repository.Auth, func(a attempt) error {
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
	_, errs := c.tryThrough(ctx, c.transportFor(ref.Context().Registry, tag.Insecure), tag.Auth, func(a attempt) error {
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

// ErrManifestDeleteUnsupported is returned when the registry refuses manifest deletion.
var ErrManifestDeleteUnsupported = errors.New("registry does not support manifest deletion")

// ManifestDigest returns the digest of the manifest image points at, and false when the
// registry does not hold it. It sends a HEAD accepting every manifest and index type, as a
// copy does, so a multi-platform tag gives the digest of its index. A registry answering the
// HEAD without the digest header gets a GET, which the digest is computed from. Destination
// calls are not counted.
func (c *Client) ManifestDigest(ctx context.Context, image Endpoint) (v1.Hash, bool, error) {
	ref, err := name.ParseReference(image.Reference, image.nameOptions()...)
	if err != nil {
		return v1.Hash{}, false, err
	}
	var digest v1.Hash
	found := true
	_, errs := c.tryThrough(ctx, c.transportFor(ref.Context().Registry, image.Insecure), image.Auth, func(a attempt) error {
		found = true
		descriptor, err := remote.Head(ref, a.options...)
		if err != nil && TransportStatusCode(err) == 0 {
			// An answer without a status error lacked a header the HEAD needs.
			var got *remote.Descriptor
			if got, err = remote.Get(ref, a.options...); err == nil {
				descriptor = &got.Descriptor
			}
		}
		if TransportStatusCode(err) == http.StatusNotFound {
			found = false
			return nil
		}
		if err != nil {
			return err
		}
		digest = descriptor.Digest
		return nil
	})
	if errs != nil {
		return v1.Hash{}, false, errors.Join(errs...)
	}
	return digest, found, nil
}

// DeleteManifest deletes one manifest by digest, and with it every tag pointing at it, other
// clusters' included: a tag reference is refused. A manifest already gone is a success. A
// registry answering 405, 400 or 501 does not support manifest deletion, which is
// ErrManifestDeleteUnsupported.
func (c *Client) DeleteManifest(ctx context.Context, manifest Endpoint) error {
	ref, err := name.NewDigest(manifest.Reference, append(manifest.nameOptions(), name.StrictValidation)...)
	if err != nil {
		return fmt.Errorf("refusing to delete %q: a manifest is deleted by digest only: %w", manifest.Reference, err)
	}
	_, errs := c.tryThrough(ctx, c.transportFor(ref.Context().Registry, manifest.Insecure), manifest.Auth, func(a attempt) error {
		err := remote.Delete(ref, a.options...)
		switch TransportStatusCode(err) {
		case http.StatusNotFound:
			return nil
		case http.StatusMethodNotAllowed, http.StatusBadRequest, http.StatusNotImplemented:
			return fmt.Errorf("%w: %w", ErrManifestDeleteUnsupported, err)
		}
		return err
	})
	if errs != nil {
		return errors.Join(errs...)
	}
	return nil
}
