package dest

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
)

// Proxy is a configured proxy host with its backend and client.
type Proxy struct {
	// Name is the proxy's name or pattern, as configured.
	Name string
	// Backend is the configuration's ProxyHost.URL; callers never modify it.
	Backend *url.URL
	// Client is shared by every proxy on the same backend.
	Client *Client
}

// Proxies holds the proxy topology: which backend and which Client serve a
// host label.
type Proxies struct {
	cfg     *testerconfig.Config
	clients map[string]*Client // by proxy name
	// backends is every distinct backend, by scheme://host.
	backends map[string]*Client
}

// Backend is a distinct proxy backend: several proxy names can share it.
type Backend struct {
	// Name is the backend's scheme://host.
	Name   string
	Client *Client
}

// NewProxies builds one Client per distinct scheme://host of the proxies of
// cfg, which must have gone through testerconfig.Load or Config.Resolve.
func NewProxies(cfg *testerconfig.Config) (*Proxies, error) {
	byBackend := map[string]*Client{}
	clients := make(map[string]*Client, len(cfg.Proxies))
	for i, p := range cfg.Proxies {
		if p.URL == nil {
			return nil, fmt.Errorf("proxies[%d] (%s): backend not parsed, the configuration did not go through Config.Resolve", i, p.Name)
		}
		key := p.URL.Scheme + "://" + p.URL.Host
		c, ok := byBackend[key]
		if !ok {
			var err error
			if c, err = New(p.URL); err != nil {
				return nil, fmt.Errorf("proxies[%d] (%s): %w", i, p.Name, err)
			}
			byBackend[key] = c
		}
		clients[p.Name] = c
	}
	return &Proxies{cfg: cfg, clients: clients, backends: byBackend}, nil
}

// Lookup returns the proxy serving label: an exact name first, then a
// pattern, as testerconfig.Config.Proxy does.
func (p *Proxies) Lookup(label string) (Proxy, bool) {
	h, ok := p.cfg.Proxy(label)
	if !ok {
		return Proxy{}, false
	}
	c, ok := p.clients[h.Name]
	if !ok {
		return Proxy{}, false
	}
	return Proxy{Name: h.Name, Backend: h.URL, Client: c}, true
}

// Names lists the configured proxy names, sorted; never nil.
func (p *Proxies) Names() []string {
	out := make([]string, 0, len(p.cfg.Proxies))
	for _, h := range p.cfg.Proxies {
		out = append(out, h.Name)
	}
	slices.Sort(out)
	return out
}

// Backends lists every distinct backend once, sorted by name (its
// scheme://host); never nil. Proxies on the same backend share one entry.
func (p *Proxies) Backends() []Backend {
	out := make([]Backend, 0, len(p.backends))
	for name, c := range p.backends {
		out = append(out, Backend{Name: name, Client: c})
	}
	slices.SortFunc(out, func(a, b Backend) int { return strings.Compare(a.Name, b.Name) })
	return out
}
