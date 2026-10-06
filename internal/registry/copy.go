package registry

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// CopyError is a failed copy and the reason it reports: the side that failed, and on that
// side the reason of the first credential tried.
type CopyError struct {
	Reason kuikv1alpha1.CopyFailureReason
	// Destination reports that the failure was observed on the destination, which
	// Unauthorized and QuotaExceeded do not tell.
	Destination bool
	Err         error
}

func (e *CopyError) Error() string { return string(e.Reason) + ": " + e.Err.Error() }
func (e *CopyError) Unwrap() error { return e.Err }

// sourceError marks a failure of a request to the source, raised while writing the
// destination: blobs are read from the source as they are pushed.
type sourceError struct{ err error }

func (e *sourceError) Error() string { return e.err.Error() }
func (e *sourceError) Unwrap() error { return e.err }

// Copy copies src verbatim into the repository dst under every tag of tags and returns the
// digest it copied: the upstream index or manifest, its digest unchanged. When dst already
// holds that digest, only the tags are written and no blob moves. Reading the source is
// counted in RequestsTotal, writing the destination is not. A failure on the source side
// ends the attempts: another destination credential cannot fix it. A failure is a
// *CopyError.
func (c *Client) Copy(ctx context.Context, src, dst Endpoint, tags []string) (v1.Hash, error) {
	if len(tags) == 0 {
		return v1.Hash{}, errors.New("a copy needs at least one tag")
	}
	srcRef, err := name.ParseReference(src.Reference, src.nameOptions()...)
	if err != nil {
		return v1.Hash{}, err
	}
	dstRepo, err := name.NewRepository(dst.Reference, dst.nameOptions()...)
	if err != nil {
		return v1.Hash{}, err
	}

	watch := &failureWatch{next: c.transport}
	source, err := c.readSource(ctx, srcRef, src, watch)
	if err != nil {
		return v1.Hash{}, err
	}

	_, errs, stopped := c.tryUntilStopped(ctx, c.transport, dst.Auth, func(a attempt) error {
		// Each attempt starts clean: a source failure of an earlier one says nothing of it.
		watch.reset()
		err := writeDestination(srcRef, source, dstRepo, tags, a.options, watch)
		if _, ok := errors.AsType[*sourceError](err); ok {
			return &stopAttempts{err}
		}
		return err
	})
	if errs != nil {
		// A source failure ends the attempts and is the outcome; otherwise the first
		// destination credential's error is.
		decisive := errs[0]
		if stopped != nil {
			decisive = stopped
		}
		if fromSource, ok := errors.AsType[*sourceError](decisive); ok {
			// The blobs read during the transfer are source requests too.
			RequestsTotal.WithLabelValues(registryLabel(srcRef), "Copy", checkResultLabel(fromSource.err)).Inc()
		}
		_, fromSource := errors.AsType[*sourceError](decisive)
		return v1.Hash{}, &CopyError{Reason: copyFailureReason(decisive), Destination: !fromSource, Err: errors.Join(errs...)}
	}
	return source.Digest, nil
}

// readSource fetches the source manifest, with each credential of src in turn.
func (c *Client) readSource(ctx context.Context, ref name.Reference, src Endpoint, watch *failureWatch) (*remote.Descriptor, error) {
	host := registryLabel(ref)
	var source *remote.Descriptor
	_, errs := c.tryThrough(ctx, watch, src.Auth, func(a attempt) error {
		descriptor, err := remote.Get(ref, a.options...)
		RequestsTotal.WithLabelValues(host, "Copy", checkResultLabel(err)).Inc()
		source = descriptor
		return err
	})
	if errs != nil {
		return nil, &CopyError{Reason: copyFailureReason(&sourceError{errs[0]}), Err: errors.Join(errs...)}
	}
	return source, nil
}

// writeDestination writes source under every tag. It asks the destination for the digest
// first: present, the copy is one manifest PUT per tag; absent, the first tag carries the
// full copy and the others are manifest PUTs.
func writeDestination(srcRef name.Reference, source *remote.Descriptor, repo name.Repository,
	tags []string, options []remote.Option, watch *failureWatch) error {
	existing, err := remote.Head(repo.Digest(source.Digest.String()), options...)
	if err != nil && TransportStatusCode(err) != http.StatusNotFound {
		return err
	}
	present := err == nil && existing.Digest == source.Digest

	for i, tag := range tags {
		target := repo.Tag(tag)
		if i == 0 && !present {
			err = writeVerbatim(target, source, options)
		} else {
			err = remote.Tag(target, source, options...)
		}
		if err != nil {
			if watch.hasFailed() || fromSource(err, srcRef) {
				return &sourceError{err}
			}
			return err
		}
	}
	return nil
}

// writeVerbatim pushes the upstream index or image as it is, children and blobs included.
func writeVerbatim(tag name.Tag, source *remote.Descriptor, options []remote.Option) error {
	if source.MediaType.IsIndex() {
		index, err := source.ImageIndex()
		if err != nil {
			return err
		}
		return remote.WriteIndex(tag, index, options...)
	}
	image, err := source.Image()
	if err != nil {
		return err
	}
	return remote.Write(tag, image, options...)
}

// fromSource reports whether err is a failed request to the source repository, for the
// failures the source transport cannot see.
func fromSource(err error, srcRef name.Reference) bool {
	var failed *url.URL
	var transportErr *transport.Error
	var urlErr *url.Error
	switch {
	case errors.As(err, &transportErr) && transportErr.Request != nil:
		failed = transportErr.Request.URL
	case errors.As(err, &urlErr):
		failed, _ = url.Parse(urlErr.URL)
	}
	if failed == nil {
		return false
	}
	repo := srcRef.Context()
	return failed.Host == repo.RegistryStr() && strings.HasPrefix(failed.Path, "/v2/"+repo.RepositoryStr()+"/")
}

// copyFailureReason maps what one request observed to a copy reason, naming the side that
// failed where both sides can fail the same way.
func copyFailureReason(err error) kuikv1alpha1.CopyFailureReason {
	var fromSource *sourceError
	source := errors.As(err, &fromSource)
	code := TransportStatusCode(err)
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return kuikv1alpha1.CopyUnauthorized
	case code == http.StatusTooManyRequests:
		return kuikv1alpha1.CopyQuotaExceeded
	case source && code == http.StatusNotFound:
		return kuikv1alpha1.CopySourceNotFound
	case source:
		return kuikv1alpha1.CopySourceUnreachable
	case code >= 400 && code < 500:
		return kuikv1alpha1.CopyPushRejected
	default:
		return kuikv1alpha1.CopyDestinationUnreachable
	}
}
