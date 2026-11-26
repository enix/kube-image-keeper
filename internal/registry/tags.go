package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
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

// ErrDeleteUnauthorized is returned when the registry answers 401 or 403 to the deletion of a
// tag or of a manifest: it refuses the credential for it.
var ErrDeleteUnauthorized = errors.New("registry refused the credential for the deletion")

// Refusal is what a registry answered a deletion it refused.
type Refusal struct {
	// StatusCode is the HTTP status of the answer.
	StatusCode int
	// Message is the registry's own words: the code and the message of each error of the
	// answer's body, empty when it sent none.
	Message string
}

// deleteRefusal is a deletion the registry refused in a way trying again does not fix. kind is
// the sentinel that says which way (ErrDeleteUnsupported, ErrManifestDeleteUnsupported or
// ErrDeleteUnauthorized), err the answer of the registry.
type deleteRefusal struct {
	kind error
	err  error
}

func (e *deleteRefusal) Error() string { return e.kind.Error() + ": " + e.err.Error() }

func (e *deleteRefusal) Unwrap() []error { return []error{e.kind, e.err} }

// answeredDelete turns the answer of a DELETE into the outcome of the deletion. A 404, or a
// repository the registry does not know, is a success: there is nothing left to delete. A 405,
// a 501 or any other 400 is unsupported, the sentinel of the deletion made; a 401 or a 403 is
// ErrDeleteUnauthorized. Any other error is returned as it is.
func answeredDelete(err, unsupported error) error {
	if repositoryUnknown(err) {
		return nil
	}
	switch TransportStatusCode(err) {
	case http.StatusNotFound:
		return nil
	case http.StatusMethodNotAllowed, http.StatusBadRequest, http.StatusNotImplemented:
		return &deleteRefusal{kind: unsupported, err: err}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &deleteRefusal{kind: ErrDeleteUnauthorized, err: err}
	}
	return err
}

// RefusalDetail returns what the registry answered the first attempt of err refused the way
// kind says, and false when err holds no such attempt. A deletion tried with several
// credentials returns the error of each, which the registry may have refused in different
// ways: the caller names the one it reports. The message is built from the registry's error
// body, never from the error text, which holds the URL of the request.
func RefusalDetail(err, kind error) (Refusal, bool) {
	refusal, ok := findRefusal(err, kind)
	if !ok {
		return Refusal{}, false
	}
	detail := Refusal{StatusCode: TransportStatusCode(refusal.err)}
	if transportErr, ok := errors.AsType[*transport.Error](refusal.err); ok {
		var words []string
		for _, diagnostic := range transportErr.Errors {
			// "DENIED: requested access to the resource is denied", or whichever of the two
			// the registry sent.
			parts := slices.DeleteFunc([]string{string(diagnostic.Code), diagnostic.Message}, func(part string) bool { return part == "" })
			if len(parts) > 0 {
				words = append(words, strings.Join(parts, ": "))
			}
		}
		detail.Message = strings.Join(words, "; ")
	}
	return detail, true
}

// findRefusal walks the errors err wraps or joins, in order, for the first deletion refused
// the way kind says.
func findRefusal(err, kind error) (*deleteRefusal, bool) {
	switch wrapped := err.(type) {
	case *deleteRefusal:
		return wrapped, wrapped.kind == kind
	case interface{ Unwrap() error }:
		return findRefusal(wrapped.Unwrap(), kind)
	case interface{ Unwrap() []error }:
		for _, inner := range wrapped.Unwrap() {
			if refusal, ok := findRefusal(inner, kind); ok {
				return refusal, true
			}
		}
	}
	return nil, false
}

// repositoryUnknown reports whether err is a 400 carrying the error code NAME_UNKNOWN: the
// registry does not know the repository, so it holds nothing there to delete. The OCI
// distribution spec ("Deleting tags", "Deleting Manifests") lets a registry refuse a deletion
// it does not support with a 400 or a 405 and answers a missing manifest with a 404; its
// error codes table gives NAME_UNKNOWN for a repository the registry does not know, which zot
// sends with a 400 on a DELETE. Harbor refuses tag deletion with a 400 under another code.
func repositoryUnknown(err error) bool {
	transportErr, ok := errors.AsType[*transport.Error](err)
	if !ok || transportErr.StatusCode != http.StatusBadRequest {
		return false
	}
	return slices.ContainsFunc(transportErr.Errors, func(d transport.Diagnostic) bool {
		return d.Code == transport.NameUnknownErrorCode
	})
}

// DeleteTag deletes one tag, never the manifest it points at: a digest reference is
// refused. A tag already gone is a success, in a repository the registry does not know
// included. A registry answering 405, 501 or any other 400 does not support tag deletion,
// which is ErrDeleteUnsupported; one answering 401 or 403 refuses the credential for it, which
// is ErrDeleteUnauthorized. Every credential is tried, and the error joins the answer to each:
// it may hold both.
func (c *Client) DeleteTag(ctx context.Context, tag Endpoint) error {
	if strings.Contains(tag.Reference, "@") {
		return fmt.Errorf("refusing to delete %q: kuik deletes by tag, never by digest", tag.Reference)
	}
	ref, err := name.NewTag(tag.Reference, append(tag.nameOptions(), name.StrictValidation)...)
	if err != nil {
		return err
	}
	_, errs := c.tryThrough(ctx, c.transportFor(ref.Context().Registry, tag.Insecure), tag.Auth, func(a attempt) error {
		return answeredDelete(remote.Delete(ref, a.options...), ErrDeleteUnsupported)
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
// clusters' included: a tag reference is refused. A manifest already gone is a success, in a
// repository the registry does not know included. A registry answering 405, 501 or any other
// 400 does not support manifest deletion, which is ErrManifestDeleteUnsupported; one answering
// 401 or 403 refuses the credential for it, which is ErrDeleteUnauthorized.
func (c *Client) DeleteManifest(ctx context.Context, manifest Endpoint) error {
	ref, err := name.NewDigest(manifest.Reference, append(manifest.nameOptions(), name.StrictValidation)...)
	if err != nil {
		return fmt.Errorf("refusing to delete %q: a manifest is deleted by digest only: %w", manifest.Reference, err)
	}
	_, errs := c.tryThrough(ctx, c.transportFor(ref.Context().Registry, manifest.Insecure), manifest.Auth, func(a attempt) error {
		return answeredDelete(remote.Delete(ref, a.options...), ErrManifestDeleteUnsupported)
	})
	if errs != nil {
		return errors.Join(errs...)
	}
	return nil
}
