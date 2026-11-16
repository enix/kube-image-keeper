// Package quota holds the fixed-window request counters behind quota rules.
package quota

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// sweepEvery is the minimum delay between two sweeps of expired windows
// by Take.
const sweepEvery = time.Minute

type window struct {
	used  int
	start time.Time
	len   time.Duration
}

// Counters counts requests per quota name and key. It is safe for
// concurrent use.
type Counters struct {
	mu        sync.Mutex
	m         map[string]map[string]*window
	nextSweep time.Time
}

// New returns empty counters.
func New() *Counters {
	return &Counters{m: map[string]map[string]*window{}}
}

func (w *window) expired(now time.Time) bool {
	return !now.Before(w.start.Add(w.len))
}

// Take counts one request against def for key. The window starts at the
// first Take; a window older than WindowSec restarts at now. It returns
// the requests left in the window and false when the limit is reached, in
// which case nothing is counted. At most once per sweepEvery, Take first
// drops the expired windows of every quota.
func (c *Counters) Take(def api.QuotaDef, key string, now time.Time) (remaining int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !now.Before(c.nextSweep) {
		c.sweep(now)
		c.nextSweep = now.Add(sweepEvery)
	}
	keys := c.m[def.Name]
	if keys == nil {
		keys = map[string]*window{}
		c.m[def.Name] = keys
	}
	w := keys[key]
	if w == nil || w.expired(now) {
		w = &window{start: now}
		keys[key] = w
	}
	w.len = time.Duration(def.WindowSec) * time.Second
	if w.used >= def.Limit {
		return 0, false
	}
	w.used++
	return def.Limit - w.used, true
}

// Peek reports the requests used in the current window. An expired or
// unused window reads as 0 used.
func (c *Counters) Peek(def api.QuotaDef, key string, now time.Time) (used int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.m[def.Name][key]
	if w == nil || w.expired(now) {
		return 0
	}
	return w.used
}

// sweep deletes the expired windows, then the quotas left without any.
// The caller holds c.mu.
func (c *Counters) sweep(now time.Time) {
	for name, keys := range c.m {
		for key, w := range keys {
			if w.expired(now) {
				delete(keys, key)
			}
		}
		if len(keys) == 0 {
			delete(c.m, name)
		}
	}
}

// Snapshot returns the used count per quota name and key, current windows
// only; expired windows are deleted.
func (c *Counters) Snapshot(now time.Time) map[string]map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]map[string]int{}
	for name, keys := range c.m {
		for key, w := range keys {
			if w.expired(now) {
				delete(keys, key)
				continue
			}
			if out[name] == nil {
				out[name] = map[string]int{}
			}
			out[name][key] = w.used
		}
	}
	return out
}

// Reset forgets every counter of the named quota.
func (c *Counters) Reset(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, name)
}

// ResetAll forgets every counter of every quota.
func (c *Counters) ResetAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.m)
}

// Key returns the counter key for a request under def: "" for a global
// quota (api.QuotaKeyGlobal), the client IP (api.QuotaKeyIP) or the user
// (api.QuotaKeyUser, api.Anonymous when empty) otherwise.
func Key(def api.QuotaDef, clientIP, user string) string {
	switch def.Key {
	case api.QuotaKeyGlobal:
		return ""
	case api.QuotaKeyIP:
		return clientIP
	case api.QuotaKeyUser:
		if user == "" {
			return api.Anonymous
		}
		return user
	}
	return ""
}

// Counts reports whether a request of the given kind and method is counted
// by def. By default only GET manifest requests are.
func Counts(def api.QuotaDef, kind api.Kind, method string) bool {
	kinds := def.Kinds
	if len(kinds) == 0 {
		kinds = []api.Kind{api.KindManifestByTag, api.KindManifestByDigest}
	}
	if !slices.Contains(kinds, kind) {
		return false
	}
	if len(def.Methods) == 0 {
		return strings.EqualFold(method, "GET")
	}
	return slices.ContainsFunc(def.Methods, func(m string) bool { return strings.EqualFold(m, method) })
}
