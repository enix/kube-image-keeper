// Package client is a Go client of the kuik-tester admin JSON API
// (/api/v1). Every object a client creates carries the set it was built with
// (WithSet), so the objects of a test are deleted in one call (DeleteSet).
// The wire types it sends and receives are those of package api.
//
// A call lasts as long as its context allows: the client sets no overall
// timeout, because a purge or an inventory of a large backend answers only
// once it is done; only connecting and the TLS handshake are bounded.
// Callers bound every call with their ctx.
//
// It serves Go programs that drive the tester: tests and tools. Package
// clienttest serves an in-process kuik-tester to test code built on this
// client.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	dialTimeout         = 10 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
)

// Client talks to one kuik-tester admin.
type Client struct {
	base string
	auth string // "user:pass" or ""
	set  string
	http *http.Client
}

// New returns a client for the admin at baseURL (without /api/v1), sending
// Basic credentials when basicAuth is "user:pass" and never an Origin header.
func New(baseURL, basicAuth string) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/") + "/api/v1",
		auth: basicAuth,
		http: &http.Client{Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: tlsHandshakeTimeout,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			// No response header timeout: a purge or an inventory answers only once done.
		}},
	}
}

// WithSet returns a client whose created objects carry the set name.
func (c *Client) WithSet(name string) *Client {
	n := *c
	n.set = name
	return &n
}

// Set is the set the client tags its objects with.
func (c *Client) Set() string { return c.set }

// APIError is a non-2xx answer of the admin.
type APIError struct {
	Status int
	Msg    string
}

// Error renders the status and the message of the admin.
func (e *APIError) Error() string { return fmt.Sprintf("kuik-tester admin: %d %s", e.Status, e.Msg) }

// do sends a request; body is JSON-encoded unless it is already []byte (a
// YAML fixture). A non-nil out receives the JSON answer.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	ct := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
		ct = "application/yaml"
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if rd != nil {
		req.Header.Set("Content-Type", ct)
	}
	if c.auth != "" {
		u, p, _ := strings.Cut(c.auth, ":")
		req.SetBasicAuth(u, p)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Go masks the password of the URL it prints, not the user name
		if ue, ok := errors.AsType[*url.Error](err); ok {
			ue.URL = maskURL(ue.URL)
		}
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e api.ErrorResponse
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return &APIError{Status: resp.StatusCode, Msg: e.Error}
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

// MatchRepo reports whether repo matches the glob pattern segment by
// segment: "**" matches zero or more segments, any other segment is matched
// with path.Match. An empty pattern matches everything. It is the matching
// of rule targets and of LogFilter.RepoGlob.
func MatchRepo(pattern, repo string) bool { return match.Repo(pattern, repo) }

var userinfoRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*://)[^/?#]*@`)

// maskURL is u with the userinfo (scheme://user:pass@host) replaced by ***.
func maskURL(u string) string { return userinfoRe.ReplaceAllString(u, "${1}***@") }

func isNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// --- rules -------------------------------------------------------------------

// CreateRule inserts r on top and returns the stored rule. r is sent whole,
// with Set replaced by the client's and Enabled forced to true; the admin
// assigns the ID. A disabled rule is made by passing the created rule with
// Enabled false to UpdateRule.
func (c *Client) CreateRule(ctx context.Context, r api.Rule) (api.Rule, error) {
	r.Set, r.Enabled = c.set, true
	var out api.RuleResponse
	err := c.do(ctx, http.MethodPost, "/rules", r, &out)
	return out.Rule, err
}

// UpdateRule replaces the rule r.ID with r, whole: every field is sent; an
// empty Set becomes the client's.
func (c *Client) UpdateRule(ctx context.Context, r api.Rule) (api.Rule, error) {
	if r.Set == "" {
		r.Set = c.set
	}
	var out api.RuleResponse
	err := c.do(ctx, http.MethodPut, "/rules/"+url.PathEscape(r.ID), r, &out)
	return out.Rule, err
}

// DeleteRule deletes the rule id.
func (c *Client) DeleteRule(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/rules/"+url.PathEscape(id), nil, nil)
}

// ListRules lists the rules, in evaluation order.
func (c *Client) ListRules(ctx context.Context) ([]api.Rule, error) {
	var out []api.Rule
	err := c.do(ctx, "GET", "/rules", nil, &out)
	return out, err
}

// --- users, grants, aliases, divergents, quotas, platforms ------------------------

// PutUser creates or replaces the registry user name.
func (c *Client) PutUser(ctx context.Context, name, password string) error {
	return c.do(ctx, "PUT", "/users/"+url.PathEscape(name), api.User{Name: name, Password: password, Set: c.set}, nil)
}

// DeleteUser deletes the registry user name.
func (c *Client) DeleteUser(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/users/"+url.PathEscape(name), nil, nil)
}

// CreateGrant creates a grant; Set is the client's.
func (c *Client) CreateGrant(ctx context.Context, g api.Grant) (api.Grant, error) {
	g.Set = c.set
	var out api.Grant
	err := c.do(ctx, "POST", "/grants", g, &out)
	return out, err
}

// DeleteGrant deletes the grant id.
func (c *Client) DeleteGrant(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/grants/"+url.PathEscape(id), nil, nil)
}

// CreateAlias creates an alias; Set is the client's.
func (c *Client) CreateAlias(ctx context.Context, a api.Alias) (api.Alias, error) {
	a.Set = c.set
	var out api.Alias
	err := c.do(ctx, "POST", "/aliases", a, &out)
	return out, err
}

// CreateDivergent creates a divergent; Set is the client's.
func (c *Client) CreateDivergent(ctx context.Context, d api.Divergent) (api.Divergent, error) {
	d.Set = c.set
	var out api.Divergent
	err := c.do(ctx, "POST", "/divergents", d, &out)
	return out, err
}

// PutQuota creates or replaces the quota q.Name and returns it as stored
// (methods upper-cased); Set is the client's.
func (c *Client) PutQuota(ctx context.Context, q api.QuotaDef) (api.QuotaDef, error) {
	q.Set = c.set
	var out api.QuotaDef
	err := c.do(ctx, http.MethodPut, "/quotas/"+url.PathEscape(q.Name), q, &out)
	return out, err
}

// QuotaUsage returns the current window count of every quota, by quota
// name then by key.
func (c *Client) QuotaUsage(ctx context.Context) (map[string]map[string]int, error) {
	out := map[string]map[string]int{}
	err := c.do(ctx, "GET", "/quotas/usage", nil, &out)
	return out, err
}

// ResetQuota clears the counters of the quota name.
func (c *Client) ResetQuota(ctx context.Context, name string) error {
	return c.do(ctx, "POST", "/quotas/"+url.PathEscape(name)+"/reset", nil, nil)
}

// CreatePlatform creates a platform rule; Set is the client's.
func (c *Client) CreatePlatform(ctx context.Context, p api.PlatformRule) (api.PlatformRule, error) {
	p.Set = c.set
	var out api.PlatformRule
	err := c.do(ctx, "POST", "/platforms", p, &out)
	return out, err
}

// DeletePlatform deletes a platform rule: its repositories serve every platform again.
func (c *Client) DeletePlatform(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/platforms/"+url.PathEscape(id), nil, nil)
}

// --- images ------------------------------------------------------------------

// Resolve builds and records the synthetic image host/repo:tag.
func (c *Client) Resolve(ctx context.Context, host, repo, tag string) (api.Resolved, error) {
	var out api.Resolved
	err := c.do(ctx, "POST", "/resolve", api.ResolveRequest{Ref: host + "/" + repo + ":" + tag}, &out)
	return out, err
}

// Drift bumps the generation of the images matching t and returns the
// changed references.
func (c *Client) Drift(ctx context.Context, t api.Target) ([]string, error) {
	var out api.DriftResult
	err := c.do(ctx, "POST", "/drift", t, &out)
	return out.Changed, err
}

// --- destinations -------------------------------------------------------------

// ListDestinations lists the proxy hosts.
func (c *Client) ListDestinations(ctx context.Context) ([]string, error) {
	var out []string
	err := c.do(ctx, "GET", "/destinations", nil, &out)
	return out, err
}

// InventoryPrefix lists the repositories and tags of the proxy host's backend
// that are named prefix or under prefix/.
func (c *Client) InventoryPrefix(ctx context.Context, host, prefix string) ([]api.Repository, error) {
	var all []api.Repository
	if err := c.do(ctx, "GET", "/destinations/"+url.PathEscape(host), nil, &all); err != nil {
		return nil, err
	}
	prefix = strings.TrimSuffix(prefix, "/")
	out := []api.Repository{}
	for _, r := range all {
		if r.Name == prefix || strings.HasPrefix(r.Name, prefix+"/") {
			out = append(out, r)
		}
	}
	return out, nil
}

// destPost sends the action of the proxy host's backend.
func (c *Client) destPost(ctx context.Context, host, action string, body, out any) error {
	return c.do(ctx, "POST", "/destinations/"+url.PathEscape(host)+"/"+action, body, out)
}

// DeleteTag deletes repo:tag on the proxy host's backend.
func (c *Client) DeleteTag(ctx context.Context, host, repo, tag string) error {
	return c.destPost(ctx, host, "delete-tag", api.DestDeleteTagRequest{Repo: repo, Tag: tag}, nil)
}

// DeleteManifest deletes repo@digest on the proxy host's backend.
func (c *Client) DeleteManifest(ctx context.Context, host, repo, digest string) error {
	return c.destPost(ctx, host, "delete-manifest", api.DestDeleteManifestRequest{Repo: repo, Digest: digest}, nil)
}

// Tag points newTag at the manifest of src (a tag or a digest) in repo on
// the proxy host's backend.
func (c *Client) Tag(ctx context.Context, host, repo, src, newTag string) error {
	return c.destPost(ctx, host, "tag", api.DestTagRequest{Repo: repo, Src: src, NewTag: newTag}, nil)
}

// Push writes a synthetic image as repo:tag to the proxy host's backend.
func (c *Client) Push(ctx context.Context, host, repo, tag string) error {
	return c.destPost(ctx, host, "push", api.DestPushRequest{Repo: repo, Tag: tag}, nil)
}

// Purge deletes every repository named prefix or under prefix/ on the
// proxy host's backend.
func (c *Client) Purge(ctx context.Context, host, prefix string) (api.PurgeResult, error) {
	var out api.PurgeResult
	err := c.destPost(ctx, host, "purge", api.DestPurgeRequest{Prefix: prefix}, &out)
	return out, err
}

// PurgeAll deletes every tag of every repository of the proxy host's
// backend.
func (c *Client) PurgeAll(ctx context.Context, host string) (api.PurgeResult, error) {
	var out api.PurgeResult
	err := c.destPost(ctx, host, "purge-all", nil, &out)
	return out, err
}

// --- request log and stats ---------------------------------------------------------

// LogFilter selects request log entries. Host, Repo, Kind, Caller, Status
// and Since are applied by the admin; RepoGlob (segment glob), User,
// ClientIP, Method and Digest on the client side.
type LogFilter struct {
	Host, Repo, RepoGlob, Caller, User, ClientIP, Method, Digest string
	Kind                                                         api.Kind
	Status                                                       *int
	Since                                                        time.Time
}

// Log returns the request log entries matching f, oldest first.
func (c *Client) Log(ctx context.Context, f LogFilter) ([]api.LogEntry, error) {
	q := url.Values{}
	for k, v := range map[string]string{"host": f.Host, "repo": f.Repo, "kind": string(f.Kind), "caller": f.Caller} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Status != nil {
		q.Set("status", strconv.Itoa(*f.Status))
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339Nano))
	}
	path := "/requests"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var all []api.LogEntry
	if err := c.do(ctx, "GET", path, nil, &all); err != nil {
		return nil, err
	}
	out := []api.LogEntry{}
	for _, e := range all {
		if f.RepoGlob != "" && !MatchRepo(f.RepoGlob, e.Repo) {
			continue
		}
		if f.User != "" && e.User != f.User {
			continue
		}
		if f.ClientIP != "" && e.ClientIP != f.ClientIP {
			continue
		}
		if f.Method != "" && !strings.EqualFold(e.Method, f.Method) {
			continue
		}
		if f.Digest != "" && e.Digest != f.Digest {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ListStats returns the cumulative request counters.
func (c *Client) ListStats(ctx context.Context) ([]api.Stat, error) {
	var out []api.Stat
	err := c.do(ctx, "GET", "/stats", nil, &out)
	return out, err
}

// ResetStats clears the cumulative request counters.
func (c *Client) ResetStats(ctx context.Context) error {
	return c.do(ctx, "POST", "/stats/reset", nil, nil)
}

// PurgeColl removes every object of a list, the ones of any set included,
// and returns how many. kind is rules, aliases, divergents, grants,
// platforms, users, hostauth or quotas. Emptying users or quotas that a
// grant or a rule still names is refused.
func (c *Client) PurgeColl(ctx context.Context, kind string) (int, error) {
	return c.purge(ctx, "/"+url.PathEscape(kind))
}

// PurgeImages forgets every seen image, digest and generation and returns
// how many images that was. An image a rule names literally stays listed.
func (c *Client) PurgeImages(ctx context.Context) (int, error) { return c.purge(ctx, "/images") }

// PurgeLog empties the request log, keeps the stats, and returns how many
// entries it held.
func (c *Client) PurgeLog(ctx context.Context) (int, error) { return c.purge(ctx, "/log") }

// PurgeEverything brings the tester back to a blank state: every list, image,
// logged request and stat goes, and the quota counters are reset. With
// backends, every repository of every proxy backend is deleted too, once per
// distinct backend. A failing backend is reported in the result, not as an
// error: the rest was purged.
func (c *Client) PurgeEverything(ctx context.Context, backends bool) (api.PurgeAllResult, error) {
	var out api.PurgeAllResult
	err := c.do(ctx, "POST", "/purge", api.PurgeAllRequest{Backends: backends}, &out)
	return out, err
}

func (c *Client) purge(ctx context.Context, path string) (int, error) {
	var out api.PurgeCount
	err := c.do(ctx, "DELETE", path, nil, &out)
	return out.Deleted, err
}

// --- sets and state ----------------------------------------------------------------

// ListSets lists the sets in use, sorted by name.
func (c *Client) ListSets(ctx context.Context) ([]api.SetInfo, error) {
	var out []api.SetInfo
	err := c.do(ctx, "GET", "/sets", nil, &out)
	return out, err
}

// DeleteSet removes every object of the set; an unknown set is fine.
func (c *Client) DeleteSet(ctx context.Context, name string) error {
	err := c.do(ctx, "DELETE", "/sets/"+url.PathEscape(name), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// ImportSet merges a YAML fixture under the set name.
func (c *Client) ImportSet(ctx context.Context, name string, yaml []byte) error {
	return c.do(ctx, "POST", "/import?set="+url.QueryEscape(name), yaml, nil)
}
