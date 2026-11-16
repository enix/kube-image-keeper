package admin

import (
	"context"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Destinations

// Destinations lists the proxy names, sorted; never nil.
func (s *Service) Destinations() []string { return s.proxies.Names() }

func (s *Service) dest(host string) (*dest.Client, error) {
	p, ok := s.proxies.Lookup(host)
	if !ok {
		return nil, ErrNotFound
	}
	return p.Client, nil
}

// withDest runs fn on the client of host; an unknown host is ErrNotFound, a
// failure of fn a ValidationError (invalid name) or a backendError.
func (s *Service) withDest(host string, fn func(*dest.Client) error) error {
	c, err := s.dest(host)
	if err != nil {
		return err
	}
	return destErr(fn(c))
}

// Inventory lists the repositories of a proxy backend.
func (s *Service) Inventory(ctx context.Context, host string) ([]api.Repository, error) {
	var repos []api.Repository
	err := s.withDest(host, func(c *dest.Client) (err error) {
		repos, err = c.Inventory(ctx)
		return err
	})
	return repos, err
}

// DestDeleteTag deletes a tag of a proxy backend.
func (s *Service) DestDeleteTag(ctx context.Context, host, repo, tag string) error {
	return s.withDest(host, func(c *dest.Client) error { return c.DeleteTag(ctx, repo, tag) })
}

// DestDeleteManifest deletes a manifest of a proxy backend by digest.
func (s *Service) DestDeleteManifest(ctx context.Context, host, repo, digest string) error {
	return s.withDest(host, func(c *dest.Client) error { return c.DeleteManifest(ctx, repo, digest) })
}

// DestTag gives a new tag to an existing tag or digest of a proxy backend.
func (s *Service) DestTag(ctx context.Context, host, repo, src, newTag string) error {
	return s.withDest(host, func(c *dest.Client) error { return c.Tag(ctx, repo, src, newTag) })
}

// DestPurge deletes every tag under a repository prefix of a proxy host.
func (s *Service) DestPurge(ctx context.Context, host, prefix string) (api.PurgeResult, error) {
	res := api.PurgeResult{Repositories: []string{}}
	err := s.withDest(host, func(c *dest.Client) (err error) {
		if !repoPath(strings.TrimSuffix(prefix, "/")) {
			return invalid("prefix %q must be a repository path without glob", prefix)
		}
		res, err = c.Purge(ctx, prefix)
		return err
	})
	return res, err
}

// DestPushSynthetic pushes a synthetic image; an empty contentKey means the
// repo and a generation of 0 means 1.
func (s *Service) DestPushSynthetic(ctx context.Context, host, repo, tag, contentKey string, generation int) error {
	if contentKey == "" {
		contentKey = repo
	}
	if generation == 0 {
		generation = 1
	}
	return s.withDest(host, func(c *dest.Client) error {
		return c.PushSynthetic(ctx, repo, tag, contentKey, generation)
	})
}
