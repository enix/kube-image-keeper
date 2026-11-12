// Package reqlog keeps a ring buffer of registry requests, cumulative
// counters and live subscribers.
package reqlog

import (
	"cmp"
	"net/http"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Filter selects entries; zero fields match anything. Status is a pointer so
// that 0 (no response) can be selected; nil matches any. Since matches Time >= Since.
type Filter struct {
	Host, Repo, Caller string
	Kind               api.Kind
	Status             *int
	Since              time.Time
}

// Match reports whether e passes the filter.
func (f Filter) Match(e api.LogEntry) bool {
	return (f.Host == "" || e.Host == f.Host) &&
		(f.Repo == "" || e.Repo == f.Repo) &&
		(f.Caller == "" || e.Caller == f.Caller) &&
		(f.Kind == "" || e.Kind == f.Kind) &&
		(f.Status == nil || e.Status == *f.Status) &&
		(f.Since.IsZero() || !e.Time.Before(f.Since))
}

type statKey struct {
	Host   string
	Kind   api.Kind
	Method string
	Status int
}

const subBuffer = 256

const (
	// maxField bounds, in bytes, every string a logged entry stores.
	maxField = 256
	// maxStatKeys bounds the distinct stats rows; later new keys count under
	// overflowHostLabel.
	maxStatKeys = 10_000
	// otherMethod is the stats method of any method outside the HTTP verbs
	// the registry serves.
	otherMethod = "OTHER"
	// overflowHostLabel is the Stat host of keys that arrive once the stats
	// are full.
	overflowHostLabel = "(overflow)"
)

// unknownHostLabel is the Stat host of requests for an unknown host.
const unknownHostLabel = "(unknown)"

// Log is a concurrency-safe request log.
type Log struct {
	mu    sync.Mutex
	buf   []api.LogEntry
	head  int // index of the oldest entry once full
	n     int
	stats map[statKey]int64
	subs  map[chan api.LogEntry]struct{}
}

// New returns a Log keeping the last size entries.
func New(size int) *Log {
	if size < 1 {
		size = 1
	}
	return &Log{
		buf:   make([]api.LogEntry, size),
		stats: map[statKey]int64{},
		subs:  map[chan api.LogEntry]struct{}{},
	}
}

// truncate cuts s to at most maxField bytes, at a rune boundary, ending with
// an ellipsis when it was cut.
func truncate(s string) string {
	if len(s) <= maxField {
		return s
	}
	const ellipsis = "…"
	cut := maxField - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

// statMethod maps a request method to its stats label.
func statMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return m
	}
	return otherMethod
}

// Add records an entry (strings cut to maxField bytes), updates the counters
// and notifies subscribers.
func (l *Log) Add(e api.LogEntry) {
	for _, f := range []*string{&e.Host, &e.ClientIP, &e.User, &e.UserAgent, &e.Caller,
		&e.Method, &e.Repo, &e.Reference, &e.Digest, &e.RuleID} {
		*f = truncate(*f)
	}
	l.mu.Lock()
	if l.n < len(l.buf) {
		l.buf[(l.head+l.n)%len(l.buf)] = e
		l.n++
	} else {
		l.buf[l.head] = e
		l.head = (l.head + 1) % len(l.buf)
	}
	host := e.Host
	if e.UnknownHost {
		host = unknownHostLabel
	}
	k := statKey{host, e.Kind, statMethod(e.Method), e.Status}
	if _, ok := l.stats[k]; !ok && len(l.stats) >= maxStatKeys {
		k.Host = overflowHostLabel
	}
	l.stats[k]++
	for ch := range l.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop
		}
	}
	l.mu.Unlock()
}

// Query returns the matching entries, oldest first.
func (l *Log) Query(f Filter) []api.LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []api.LogEntry
	for i := range l.n {
		e := l.buf[(l.head+i)%len(l.buf)]
		if !f.Match(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Expire drops the entries older than cutoff, oldest first, and returns how
// many it dropped. The cumulative counters are not touched.
func (l *Log) Expire(cutoff time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	dropped := 0
	for l.n > 0 && l.buf[l.head].Time.Before(cutoff) {
		l.buf[l.head] = api.LogEntry{}
		l.head = (l.head + 1) % len(l.buf)
		l.n--
		dropped++
	}
	return dropped
}

// Subscribe returns a channel of new entries (buffer 256; entries are dropped
// for slow subscribers) and a func that unsubscribes and closes the channel.
func (l *Log) Subscribe() (<-chan api.LogEntry, func()) {
	ch := make(chan api.LogEntry, subBuffer)
	l.mu.Lock()
	l.subs[ch] = struct{}{}
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, ok := l.subs[ch]; ok {
			delete(l.subs, ch)
			close(ch)
		}
	}
}

// Stats returns the cumulative counters sorted by Host, Kind, Method, Status.
func (l *Log) Stats() []api.Stat {
	l.mu.Lock()
	out := make([]api.Stat, 0, len(l.stats))
	for k, c := range l.stats {
		out = append(out, api.Stat{Host: k.Host, Kind: k.Kind, Method: k.Method, Status: k.Status, Count: c})
	}
	l.mu.Unlock()
	slices.SortFunc(out, func(a, b api.Stat) int {
		return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Method, b.Method), cmp.Compare(a.Status, b.Status))
	})
	return out
}

// ResetStats clears the cumulative counters.
func (l *Log) ResetStats() {
	l.mu.Lock()
	l.stats = map[statKey]int64{}
	l.mu.Unlock()
}

// Subscribers returns the number of live subscribers. It lets a test wait
// until a stream has attached.
func (l *Log) Subscribers() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.subs)
}

// Clear empties the ring and returns how many entries it held. The stats
// and the subscribers are kept.
func (l *Log) Clear() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.n
	clear(l.buf)
	l.head, l.n = 0, 0
	return n
}
