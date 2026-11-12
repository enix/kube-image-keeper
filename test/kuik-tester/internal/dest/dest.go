// Package dest runs the admin actions (inventory, delete, retag, push) that
// the UI sends to a proxy host's backend registry. They go straight to the
// backend, not through the public proxy, so they never show up as kuik
// traffic in the request log.
//
// Error contract: every error of Inventory, DeleteTag, DeleteManifest, Tag,
// PushSynthetic and Purge matches exactly one of two sentinels with
// errors.Is. ErrInvalid is a repo, tag or digest refused before any request
// is sent (the admin API maps it to 400); ErrBackend is anything else, the
// backend or the connection to it failing (mapped to 502). The error text is
// the original one, and errors.As still reaches the *name.ErrBadName or the
// *transport.Error underneath. A deadline reached, the caller's or the
// method's, is an ErrBackend.
//
// Proxies is the one owner of the proxy topology: it resolves a host label
// to its configured proxy, backend URL and Client, one Client per backend.
package dest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// ErrInvalid marks a repo, tag or digest refused before any request.
var ErrInvalid = errors.New("invalid reference")

// ErrBackend marks a failure of the backend or of the connection to it.
var ErrBackend = errors.New("backend failure")

// classified carries an error with its sentinel; its text is the error's.
type classified struct{ kind, err error }

func (e *classified) Error() string   { return e.err.Error() }
func (e *classified) Unwrap() []error { return []error{e.kind, e.err} }

// classify tags *errp with ErrInvalid or ErrBackend, unless it already
// carries one, so methods calling each other do not wrap twice.
func classify(errp *error) {
	err := *errp
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrBackend) {
		return
	}
	kind := ErrBackend
	if _, bad := errors.AsType[*name.ErrBadName](err); bad {
		kind = ErrInvalid
	}
	*errp = &classified{kind, err}
}

const (
	// responseHeaderTimeout bounds the wait for a backend's response headers.
	responseHeaderTimeout = 30 * time.Second
	// defaultActionTimeout bounds DeleteTag, DeleteManifest, Tag and PushSynthetic.
	defaultActionTimeout = time.Minute
	// defaultListTimeout bounds Inventory and Purge, which walk every repository.
	defaultListTimeout = 5 * time.Minute
)

// Client talks to one backend registry.
type Client struct {
	registry      name.Registry
	nameOpts      []name.Option
	transport     *http.Transport
	actionTimeout time.Duration // bound of one single-repository action
	listTimeout   time.Duration // bound of Inventory and Purge
}

// New returns a client for backend, a scheme://host[:port] URL parsed by
// config, which New never modifies. It never goes through
// HTTP_PROXY/HTTPS_PROXY.
func New(u *url.URL) (*Client, error) {
	var opts []name.Option
	if u.Scheme == "http" {
		opts = append(opts, name.Insecure)
	}
	// go-containerregistry only reads the first path segment as a registry
	// when it has a '.', a ':' or is "localhost", so spell the port out.
	host := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	reg, err := name.NewRegistry(host, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{registry: reg, nameOpts: opts, transport: NewTransport(),
		actionTimeout: defaultActionTimeout, listTimeout: defaultListTimeout}, nil
}

// NewTransport returns the transport used to reach backend registries. It
// never goes through HTTP_PROXY/HTTPS_PROXY. A backend that sends no response
// headers within 30 s fails the request; the timer starts once the request
// body is written, so uploads and streamed response bodies are not bounded.
// The registry proxy uses this transport too, so it has the same bound.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.ResponseHeaderTimeout = responseHeaderTimeout
	return t
}

func (c *Client) remoteOpts(ctx context.Context) []remote.Option {
	return []remote.Option{remote.WithContext(ctx), remote.WithTransport(c.transport)}
}

func (c *Client) repo(repo string) (name.Repository, error) {
	return name.NewRepository(c.registry.Name()+"/"+repo, c.nameOpts...)
}

func (c *Client) tagRef(repo, tag string) (name.Tag, error) {
	return name.NewTag(c.registry.Name()+"/"+repo+":"+tag, c.nameOpts...)
}

func (c *Client) digestRef(repo, digest string) (name.Digest, error) {
	return name.NewDigest(c.registry.Name()+"/"+repo+"@"+digest, c.nameOpts...)
}

// Inventory lists every repository with its tags and their digests, sorted
// by repository name then tag.
func (c *Client) Inventory(ctx context.Context) (_ []api.Repository, err error) {
	defer classify(&err)
	ctx, cancel := context.WithTimeout(ctx, c.listTimeout)
	defer cancel()
	names, err := c.catalog(ctx)
	if err != nil {
		return nil, err
	}
	repos := make([]api.Repository, 0, len(names))
	for _, n := range names {
		tags, err := c.tagInfos(ctx, n)
		if isNotFound(err) {
			continue // deleted since the catalog call
		}
		if err != nil {
			return nil, err
		}
		repos = append(repos, api.Repository{Name: n, Tags: tags})
	}
	return repos, nil
}

// catalog lists the repository names of the backend, sorted.
func (c *Client) catalog(ctx context.Context) ([]string, error) {
	names, err := remote.Catalog(ctx, c.registry, c.remoteOpts(ctx)...)
	slices.Sort(names)
	return names, err
}

// hasStatus reports whether err is a backend error with one of codes.
func hasStatus(err error, codes ...int) bool {
	terr, ok := errors.AsType[*transport.Error](err)
	return ok && slices.Contains(codes, terr.StatusCode)
}

// isNotFound reports a backend 404: the object is gone.
func isNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// tagInfos returns the tags of repo with their digests. A repo that is gone
// yields the 404; a tag deleted since the listing is skipped.
func (c *Client) tagInfos(ctx context.Context, repo string) ([]api.TagInfo, error) {
	r, err := c.repo(repo)
	if err != nil {
		return nil, err
	}
	tags, err := remote.List(r, c.remoteOpts(ctx)...)
	if err != nil {
		return nil, err
	}
	slices.Sort(tags)
	infos := make([]api.TagInfo, 0, len(tags))
	for _, tag := range tags {
		ref, err := c.tagRef(repo, tag)
		if err != nil {
			return nil, err
		}
		desc, err := remote.Head(ref, c.remoteOpts(ctx)...)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		infos = append(infos, api.TagInfo{Tag: tag, Digest: desc.Digest.String()})
	}
	return infos, nil
}

// DeleteTag deletes the tag; a missing tag is the backend's error.
func (c *Client) DeleteTag(ctx context.Context, repo, tag string) (err error) {
	defer classify(&err)
	ctx, cancel := context.WithTimeout(ctx, c.actionTimeout)
	defer cancel()
	ref, err := c.tagRef(repo, tag)
	if err != nil {
		return err
	}
	return remote.Delete(ref, c.remoteOpts(ctx)...)
}

// DeleteManifest removes every tag pointing to digest, then the manifest
// itself. Only the manifest named goes: the platform manifests of an index
// and the blobs stay until the backend's GC.
func (c *Client) DeleteManifest(ctx context.Context, repo, digest string) (err error) {
	defer classify(&err)
	ctx, cancel := context.WithTimeout(ctx, c.actionTimeout)
	defer cancel()
	dref, err := c.digestRef(repo, digest)
	if err != nil {
		return err
	}
	tags, err := c.tagInfos(ctx, repo)
	if err != nil {
		return err
	}
	for _, ti := range tags {
		if ti.Digest != digest {
			continue
		}
		if err := c.DeleteTag(ctx, repo, ti.Tag); err != nil && !isNotFound(err) {
			return err
		}
	}
	// A backend may drop the manifest along with its last tag.
	err = remote.Delete(dref, c.remoteOpts(ctx)...)
	if isNotFound(err) {
		return nil
	}
	return err
}

// Tag points newTag at the manifest src (a tag, or "sha256:…"), keeping the
// manifest bytes, hence its digest.
func (c *Client) Tag(ctx context.Context, repo, src, newTag string) (err error) {
	defer classify(&err)
	ctx, cancel := context.WithTimeout(ctx, c.actionTimeout)
	defer cancel()
	var srcRef name.Reference
	if strings.HasPrefix(src, "sha256:") {
		srcRef, err = c.digestRef(repo, src)
	} else {
		srcRef, err = c.tagRef(repo, src)
	}
	if err != nil {
		return err
	}
	dst, err := c.tagRef(repo, newTag)
	if err != nil {
		return err
	}
	desc, err := remote.Get(srcRef, c.remoteOpts(ctx)...)
	if err != nil {
		return err
	}
	return remote.Tag(dst, desc, c.remoteOpts(ctx)...)
}

// PushSynthetic writes the synthetic index of (contentKey, generation).
func (c *Client) PushSynthetic(ctx context.Context, repo, tag, contentKey string, generation int) (err error) {
	defer classify(&err)
	ctx, cancel := context.WithTimeout(ctx, c.actionTimeout)
	defer cancel()
	ref, err := c.tagRef(repo, tag)
	if err != nil {
		return err
	}
	idx, err := synth.Build(contentKey, generation, nil)
	if err != nil {
		return fmt.Errorf("build synthetic image: %w", err)
	}
	return remote.WriteIndex(ref, idx, c.remoteOpts(ctx)...)
}

// Purge deletes every tag of every repository named prefix or under
// prefix/ (a trailing slash on prefix is ignored), then tries to delete the
// manifests those tags pointed to and, for an index, the manifests it lists,
// then the blobs (configs and layers) of those manifests in that repository;
// a backend that refuses or has already dropped a manifest or a blob (404,
// 405) is not an error. The result counts the deleted tags and names the
// repositories visited.
func (c *Client) Purge(ctx context.Context, prefix string) (api.PurgeResult, error) {
	prefix = strings.TrimSuffix(prefix, "/")
	return c.purge(ctx, func(n string) bool { return n == prefix || strings.HasPrefix(n, prefix+"/") })
}

// PurgeAll is Purge over every repository of the backend.
func (c *Client) PurgeAll(ctx context.Context) (api.PurgeResult, error) {
	return c.purge(ctx, func(string) bool { return true })
}

// purge deletes the tags and manifests of every repository keep accepts.
func (c *Client) purge(ctx context.Context, keep func(repo string) bool) (res api.PurgeResult, err error) {
	defer classify(&err)
	ctx, cancel := context.WithTimeout(ctx, c.listTimeout)
	defer cancel()
	res = api.PurgeResult{Repositories: []string{}}
	names, err := c.catalog(ctx)
	if err != nil {
		return res, err
	}
	for _, n := range names {
		if !keep(n) {
			continue
		}
		tags, err := c.tagInfos(ctx, n)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return res, err
		}
		digests := map[string]bool{}
		for _, ti := range tags {
			err := c.DeleteTag(ctx, n, ti.Tag)
			if isNotFound(err) {
				continue
			}
			if err != nil {
				return res, err
			}
			res.DeletedTags++
			digests[ti.Digest] = true
		}
		for d := range digests {
			// The platform manifests of an index go too, after the index,
			// and the blobs of every manifest after them: left behind, they
			// would spare the next copy of the same image its manifest PUTs,
			// its blob reads and its uploads. The backend's garbage
			// collection may never reach them while the repository exists.
			children, err := c.indexChildren(ctx, n, d)
			if err != nil {
				return res, err
			}
			manifests := append([]string{d}, children...)
			var blobs []string
			for _, digest := range manifests {
				b, err := c.manifestBlobs(ctx, n, digest)
				if err != nil {
					return res, err
				}
				blobs = append(blobs, b...)
			}
			for _, digest := range manifests {
				if err := c.deleteDigest(ctx, n, digest); err != nil {
					return res, err
				}
			}
			for _, digest := range blobs {
				if err := c.deleteBlob(ctx, n, digest); err != nil {
					return res, err
				}
			}
		}
		res.Repositories = append(res.Repositories, n)
	}
	return res, nil
}

// indexChildren returns the digests of the manifests an index lists, or none
// when digest is not an index (or is already gone).
func (c *Client) indexChildren(ctx context.Context, repo, digest string) ([]string, error) {
	dref, err := c.digestRef(repo, digest)
	if err != nil {
		return nil, err
	}
	desc, err := remote.Get(dref, c.remoteOpts(ctx)...)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !desc.MediaType.IsIndex() {
		return nil, nil
	}
	idx, err := desc.ImageIndex()
	if err != nil {
		return nil, err
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(im.Manifests))
	for _, m := range im.Manifests {
		out = append(out, m.Digest.String())
	}
	return out, nil
}

// manifestBlobs returns the config and layer digests of an image manifest,
// or none when digest is an index (or is already gone).
func (c *Client) manifestBlobs(ctx context.Context, repo, digest string) ([]string, error) {
	dref, err := c.digestRef(repo, digest)
	if err != nil {
		return nil, err
	}
	desc, err := remote.Get(dref, c.remoteOpts(ctx)...)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if desc.MediaType.IsIndex() {
		return nil, nil
	}
	m, err := v1.ParseManifest(bytes.NewReader(desc.Manifest))
	if err != nil {
		return nil, err
	}
	out := []string{m.Config.Digest.String()}
	for _, l := range m.Layers {
		out = append(out, l.Digest.String())
	}
	return out, nil
}

// deleteBlob deletes a blob from one repository; one already gone (404), or
// a backend that refuses blob deletions (405), is not an error. On a
// deduplicating backend, the other repositories keep their copy.
func (c *Client) deleteBlob(ctx context.Context, repo, digest string) error {
	r, err := c.repo(repo)
	if err != nil {
		return err
	}
	rt, err := transport.NewWithContext(ctx, c.registry, authn.Anonymous, c.transport, []string{r.Scope(transport.DeleteScope)})
	if err != nil {
		return err
	}
	u := url.URL{Scheme: c.registry.Scheme(), Host: c.registry.RegistryStr(), Path: "/v2/" + repo + "/blobs/" + digest}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return transport.CheckError(resp, http.StatusAccepted, http.StatusOK, http.StatusNotFound, http.StatusMethodNotAllowed)
}

// deleteDigest deletes a manifest by digest; one already gone (404), or a
// backend that refuses deletions (405), is not an error.
func (c *Client) deleteDigest(ctx context.Context, repo, digest string) error {
	dref, err := c.digestRef(repo, digest)
	if err != nil {
		return err
	}
	if err := remote.Delete(dref, c.remoteOpts(ctx)...); err != nil && !hasStatus(err, http.StatusNotFound, http.StatusMethodNotAllowed) {
		return err
	}
	return nil
}
