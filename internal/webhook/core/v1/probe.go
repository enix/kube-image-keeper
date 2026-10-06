package v1

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/keychain"
	"github.com/enix/kube-image-keeper/internal/routing"
)

// checkResponse is what one check answered: the verdict, the digest or the reason it failed,
// so that a cached answer serves whoever asks the same question.
type checkResponse struct {
	available bool
	digest    string
	reason    kuikv1alpha1.CheckFailureReason
}

type cachedCheck struct {
	response checkResponse
	expires  time.Time
}

// checkCache holds the answers of this replica's checks for activeCheckCache.ttl, keyed by
// the candidate reference and the identity of the credentials it was probed with, and
// collapses concurrent checks of one key into a single request.
type checkCache struct {
	mu      sync.Mutex
	entries map[string]cachedCheck
	flight  singleflight.Group
}

func newCheckCache() *checkCache {
	return &checkCache{entries: map[string]cachedCheck{}}
}

// check returns the cached answer for key, or runs probe once however many callers ask.
func (c *checkCache) check(key string, ttl time.Duration, probe func() checkResponse) checkResponse {
	if r, ok := c.lookup(key); ok {
		return r
	}
	v, _, _ := c.flight.Do(key, func() (any, error) {
		if r, ok := c.lookup(key); ok {
			return r, nil
		}
		r := probe()
		c.store(key, r, ttl)
		return r, nil
	})
	return v.(checkResponse)
}

func (c *checkCache) lookup(key string) (checkResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		return checkResponse{}, false
	}
	return e.response, true
}

func (c *checkCache) store(key string, r checkResponse, ttl time.Duration) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = cachedCheck{response: r, expires: now.Add(ttl)}
}

// available probes a candidate with the credentials the node will have: the entry's auth,
// then the pod's pull secrets, never fallbackAuth. A declared Secret that cannot be read
// leaves the pod's pull secrets and anonymous, as the kubelet would. A candidate that failed
// its check comes with the reason it failed on; one skipped before any check, with none.
func (d *PodDefaulter) available(ctx context.Context, pod *corev1.Pod, c routing.Candidate, cfg *config.Config) checkResponse {
	log := logf.FromContext(ctx).WithValues("candidate", c.Reference)

	ref, err := imagepath.Parse(c.Reference)
	if err != nil {
		log.V(1).Info("Skipped candidate", "error", err.Error())
		return checkResponse{}
	}
	creds, err := d.resolver.Resolve(ctx, ref, c.Config.Auth, pod)
	if err != nil {
		log.V(1).Info("Skipped the declared credential of candidate", "error", err.Error())
		if creds, err = d.resolver.Resolve(ctx, ref, nil, pod); err != nil {
			return checkResponse{}
		}
	}
	auths, identity := authenticators(ctx, c.Reference, creds)

	key := c.Reference + "\x00" + strconv.FormatBool(c.Config.Insecure) + "\x00" + identity
	timeout := cfg.Webhook.AvailabilityCheck.Timeout.Duration
	response := d.checks.check(key, cfg.Webhook.AvailabilityCheck.ActiveCheckCache.TTL.Duration, func() checkResponse {
		// The check serves every caller of this key: no single admission may cancel it.
		probeCtx, cancel := registry.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		result, err := d.registry.Check(probeCtx, registry.Endpoint{
			Reference: c.Reference,
			Insecure:  c.Config.Insecure,
			Auth:      auths,
		})
		if err != nil {
			var reason kuikv1alpha1.CheckFailureReason
			if checkErr, ok := errors.AsType[*registry.CheckError](err); ok {
				reason = checkErr.Reason
			}
			log.V(1).Info("Candidate failed its check", "reason", reason, "error", err.Error())
			return checkResponse{reason: reason}
		}
		return checkResponse{available: true, digest: result.Descriptor.Digest.String()}
	})
	return response
}

// authenticators turns the resolved credentials into the authenticators to try, in order,
// and names the Secrets they come from. A pending provider yields nothing; with no
// authenticator left the check is anonymous.
func authenticators(ctx context.Context, image string, creds []auth.Credential) ([]authn.Authenticator, string) {
	var auths []authn.Authenticator
	var identity []string
	for _, cred := range creds {
		if cred.Pending || len(cred.Secrets) == 0 {
			continue
		}
		found, err := keychain.Authenticators(image, cred.Secrets)
		if err != nil {
			logf.FromContext(ctx).V(1).Info("Skipped malformed pull Secret", "error", err.Error())
			continue
		}
		if len(found) == 0 {
			continue
		}
		auths = append(auths, found...)
		for _, s := range cred.Secrets {
			identity = append(identity, s.Namespace+"/"+s.Name+"@"+s.ResourceVersion)
		}
	}
	return auths, strings.Join(identity, ",")
}
