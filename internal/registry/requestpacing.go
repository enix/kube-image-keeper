package registry

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/utils/clock"
)

// RequestPacer spaces the HTTP requests sent to each source host by the requestInterval of
// that host: each request leaves in the order it asked, once the one ahead of it has left or
// given up, and as soon as the current interval has passed since the last one that left,
// with no burst.
//
// The kubelet paces pulls with a per-node token bucket (registryPullQPS, registryBurst).
// kuik counts HTTP requests per host instead, and allows no burst on purpose: a burst would
// spend the burst of the host the nodes' own pulls need.
type RequestPacer struct {
	clk clock.Clock
	// interval returns the requestInterval of a host, read from the current config.
	interval func(host string) time.Duration

	mu    sync.Mutex
	hosts map[string]*requestSeries
	// reloaded is closed, and a new one made, on each Reload: what a waiting request watches
	// to read the interval again.
	reloaded chan struct{}
}

// requestSeries is the queue of the requests waiting on one host.
type requestSeries struct {
	// waiting holds the requests that have not left yet, in the order they asked. A request
	// that gives up leaves the queue at once; the others keep their places.
	waiting []*pacedRequest
	// lastSent is when the last request left, valid when sent is set.
	lastSent time.Time
	sent     bool
}

// pacedRequest is one request waiting on a host.
type pacedRequest struct {
	// turn is closed once every request ahead of it has left or given up.
	turn chan struct{}
}

// NewRequestPacer returns a RequestPacer reading the requestInterval of each host from
// interval, keyed as the global config names hosts: docker.io for Docker Hub.
func NewRequestPacer(clk clock.Clock, interval func(host string) time.Duration) *RequestPacer {
	return &RequestPacer{clk: clk, interval: interval, hosts: map[string]*requestSeries{}, reloaded: make(chan struct{})}
}

// Reload makes the requests waiting read the requestInterval of their host again: a lower
// one lets them go sooner, 0 at once. Call it on every reload of the config interval reads.
func (p *RequestPacer) Reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	close(p.reloaded)
	p.reloaded = make(chan struct{})
}

// queued counts the requests waiting on host, named as the image writes it, the one whose
// turn has come included.
func (p *RequestPacer) queued(host string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.series(seriesKey(host)).waiting)
}

// SetRequestPacer paces the source requests of c with p: those of Check, CheckCopySource and
// the source side of Copy. Call it before c is used; a Client without one paces nothing.
func (c *Client) SetRequestPacer(p *RequestPacer) {
	c.pacer = p
}

// paces reports whether p spaces the requests to host now. A nil pacer paces nothing.
func (p *RequestPacer) paces(host string) bool {
	return p != nil && p.interval(host) > 0
}

func (p *RequestPacer) series(host string) *requestSeries {
	s, ok := p.hosts[host]
	if !ok {
		s = &requestSeries{}
		p.hosts[host] = s
	}
	return s
}

// enqueue puts a request at the end of the queue of the series key and returns it, nil when
// the interval of host, named as the image writes it, is 0: that request goes at once.
func (p *RequestPacer) enqueue(key, host string) *pacedRequest {
	interval := p.interval(host)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.series(key)
	if interval <= 0 {
		s.lastSent, s.sent = p.clk.Now(), true
		return nil
	}
	r := &pacedRequest{turn: make(chan struct{})}
	if len(s.waiting) == 0 {
		close(r.turn)
	}
	s.waiting = append(s.waiting, r)
	return r
}

// leave takes r out of the queue of the series key, sent or not, and hands the turn to the
// next one.
func (p *RequestPacer) leave(key string, r *pacedRequest, sent bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.series(key)
	if sent {
		s.lastSent, s.sent = p.clk.Now(), true
	}
	i := slices.Index(s.waiting, r)
	if i < 0 {
		return
	}
	s.waiting = slices.Delete(s.waiting, i, i+1)
	if i == 0 && len(s.waiting) > 0 {
		close(s.waiting[0].turn)
	}
}

// gap returns how long the request whose turn has come in the series key still waits for the
// current interval of host after the last request that left, and the channel a reload closes.
func (p *RequestPacer) gap(key, host string) (time.Duration, <-chan struct{}) {
	// The channel is taken before the interval is read: a reload that lands in between closes
	// it, so the wait reads the interval again rather than keeping the stale one.
	p.mu.Lock()
	reloaded := p.reloaded
	p.mu.Unlock()
	interval := p.interval(host)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.series(key)
	if !s.sent || interval <= 0 {
		return 0, reloaded
	}
	return s.lastSent.Add(interval).Sub(p.clk.Now()), reloaded
}

// sleep waits d on the clock, or until a reload or ctx is done. It reports false only when
// ctx is done.
func (p *RequestPacer) sleep(ctx context.Context, d time.Duration, reloaded <-chan struct{}) bool {
	timer := p.clk.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C():
	case <-reloaded:
	case <-ctx.Done():
		return false
	}
	return true
}

// wait blocks until the request may leave in the series key, paced by the interval of host,
// or until ctx is done. Its turn comes once every request ahead of it has left or given up;
// it then leaves as soon as the current interval has passed since the last request that
// left, read again on every reload.
func (p *RequestPacer) wait(ctx context.Context, key, host string) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	r := p.enqueue(key, host)
	if r == nil {
		return nil
	}
	ready := true
	select {
	case <-r.turn:
	case <-ctx.Done():
		ready = false
	}
	for ready {
		d, reloaded := p.gap(key, host)
		if d <= 0 {
			break
		}
		ready = p.sleep(ctx, d, reloaded)
	}
	p.leave(key, r, ready)
	if !ready {
		return fmt.Errorf("waiting for the requestInterval of %s: %w", host, context.Cause(ctx))
	}
	return nil
}

// pacedTransport makes each request to the source host wait for its turn. A request to
// another host, a blob redirected to a CDN or a token service elsewhere, goes at once.
type pacedTransport struct {
	pacer *RequestPacer
	// host is the source host as the image writes it, as tlsKey names it: the key its
	// requestInterval is read under, as its windows are.
	host string
	// series names the queue of the host, default port dropped, so that the requests of
	// every scheme and spelling of one host queue together.
	series string
	next   http.RoundTripper
}

func (t *pacedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if pacingKey(req.URL.Host, req.URL.Scheme) == pacingKey(t.host, req.URL.Scheme) {
		if err := t.pacer.wait(req.Context(), t.series, t.host); err != nil {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			// Never sent: not a request kuik counts, and the operation fails as its timeout does.
			return nil, &notSentError{err}
		}
	}
	return t.next.RoundTrip(req)
}

const schemeHTTPS = "https"

// defaultPorts is the port a URL of each scheme reaches when it names none.
var defaultPorts = map[string]string{schemeHTTPS: ":443", "http": ":80"}

// pacingKey names a host as tlsKey does, without the default port of scheme: a request to
// registry.example:443 over HTTPS goes to registry.example.
func pacingKey(host, scheme string) string {
	return tlsKey(strings.TrimSuffix(strings.ToLower(host), defaultPorts[scheme]))
}

// seriesKey names the queue of a host whatever the scheme of each request: its default port
// over HTTPS or plain HTTP dropped.
func seriesKey(host string) string {
	return pacingKey(pacingKey(host, schemeHTTPS), "http")
}

// sourceTransport is transportFor for the requests read from a source registry, paced when
// c has a pacer.
func (c *Client) sourceTransport(registry name.Registry, insecure bool) http.RoundTripper {
	next := c.transportFor(registry, insecure)
	if c.pacer == nil {
		return next
	}
	host := tlsKey(registry.RegistryStr())
	return &pacedTransport{pacer: c.pacer, host: host, series: seriesKey(host), next: next}
}
