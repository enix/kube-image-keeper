// Package registrytest serves in-memory OCI registries for tests, with the faults and the
// request accounting the registry specs need.
package registrytest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Request is one request the registry received.
type Request struct {
	Method string
	Path   string
	// Authorized is true when the request carried an Authorization header.
	Authorized bool
}

// Interceptor answers a request in place of the registry and returns true, or returns
// false to let the registry answer. It may set response headers before passing through.
type Interceptor func(w http.ResponseWriter, r *http.Request) bool

// Registry is an in-memory registry served over plain HTTP.
type Registry struct {
	server       *httptest.Server
	handler      http.Handler
	user         string
	password     string
	tagsPageSize int

	mu        sync.Mutex
	requests  []Request
	intercept Interceptor
}

// Option configures a Registry.
type Option func(*Registry)

// WithBasicAuth makes the registry refuse, with a 401, every request that does not carry
// these basic credentials.
func WithBasicAuth(user, password string) Option {
	return func(r *Registry) { r.user, r.password = user, password }
}

// WithTagsPageSize makes tags/list answer at most n tags per page, with a Link header to
// the next one.
func WithTagsPageSize(n int) Option {
	return func(r *Registry) { r.tagsPageSize = n }
}

// New starts a registry. It is closed when the caller calls Close.
func New(opts ...Option) *Registry {
	r := &Registry{handler: ggcrregistry.New()}
	for _, opt := range opts {
		opt(r)
	}
	r.server = httptest.NewServer(http.HandlerFunc(r.serveHTTP))
	return r
}

// Close stops the registry.
func (r *Registry) Close() { r.server.Close() }

// Host is the host:port of the registry.
func (r *Registry) Host() string { return strings.TrimPrefix(r.server.URL, "http://") }

// Intercept installs f in front of the registry, replacing any previous interceptor.
func (r *Registry) Intercept(f Interceptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intercept = f
}

// Requests returns the requests received so far whose method is method (any when empty)
// and whose path contains pathPart.
func (r *Registry) Requests(method, pathPart string) []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Request
	for _, req := range r.requests {
		if (method == "" || req.Method == method) && strings.Contains(req.Path, pathPart) {
			out = append(out, req)
		}
	}
	return out
}

// Reset forgets the requests received so far.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = nil
}

// Push writes img to ref, a reference relative to the registry ("repo:tag"), and returns
// its digest.
func (r *Registry) Push(ref string, img v1.Image) v1.Hash {
	tag, err := name.NewTag(r.Host()+"/"+ref, name.Insecure)
	must(err)
	must(remote.Write(tag, img, r.remoteOptions()...))
	digest, err := img.Digest()
	must(err)
	return digest
}

// PushIndex writes idx to ref, a reference relative to the registry, and returns its digest.
func (r *Registry) PushIndex(ref string, idx v1.ImageIndex) v1.Hash {
	tag, err := name.NewTag(r.Host()+"/"+ref, name.Insecure)
	must(err)
	must(remote.WriteIndex(tag, idx, r.remoteOptions()...))
	digest, err := idx.Digest()
	must(err)
	return digest
}

// Head returns the descriptor of ref, a reference relative to the registry.
func (r *Registry) Head(ref string) (*v1.Descriptor, error) {
	parsed, err := name.ParseReference(r.Host()+"/"+ref, name.Insecure)
	if err != nil {
		return nil, err
	}
	return remote.Head(parsed, r.remoteOptions()...)
}

func (r *Registry) remoteOptions() []remote.Option {
	if r.user == "" {
		return nil
	}
	return []remote.Option{remote.WithAuth(&authn.Basic{Username: r.user, Password: r.password})}
}

func (r *Registry) serveHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.requests = append(r.requests, Request{
		Method:     req.Method,
		Path:       req.URL.Path,
		Authorized: req.Header.Get("Authorization") != "",
	})
	intercept := r.intercept
	r.mu.Unlock()

	if r.user != "" {
		user, password, ok := req.BasicAuth()
		if !ok || user != r.user || password != r.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="registrytest"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	if intercept != nil && intercept(w, req) {
		return
	}
	if r.tagsPageSize > 0 && req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/tags/list") {
		r.serveTagsPage(w, req)
		return
	}
	r.handler.ServeHTTP(w, req)
}

// serveTagsPage answers one page of tags/list, with a Link header when a page may follow.
func (r *Registry) serveTagsPage(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	query.Set("n", strconv.Itoa(r.tagsPageSize))
	req.URL.RawQuery = query.Encode()

	recorder := httptest.NewRecorder()
	r.handler.ServeHTTP(recorder, req)
	body := recorder.Body.Bytes()

	var page struct {
		Tags []string `json:"tags"`
	}
	if recorder.Code == http.StatusOK {
		must(json.Unmarshal(body, &page))
		if len(page.Tags) == r.tagsPageSize {
			next := url.Values{"n": {strconv.Itoa(r.tagsPageSize)}, "last": {page.Tags[len(page.Tags)-1]}}
			w.Header().Set("Link", fmt.Sprintf(`<%s?%s>; rel="next"`, req.URL.Path, next.Encode()))
		}
	}
	for key, values := range recorder.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(recorder.Code)
	_, _ = w.Write(body)
}

// ClosedHost returns a host:port on which nothing listens.
func ClosedHost() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	host := listener.Addr().String()
	must(listener.Close())
	return host
}

// Image returns a random single-platform image.
func Image() v1.Image {
	img, err := random.Image(256, 1)
	must(err)
	return img
}

// Index returns a random multi-platform image index.
func Index() v1.ImageIndex {
	idx, err := random.Index(256, 1, 2)
	must(err)
	return idx
}

// Status answers every request whose method is method (any when empty) and whose path
// contains pathPart with status and headers, and lets the registry answer the others.
func Status(method, pathPart string, status int, headers map[string]string) Interceptor {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if (method != "" && r.Method != method) || !strings.Contains(r.URL.Path, pathPart) {
			return false
		}
		for key, value := range headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(status)
		return true
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
