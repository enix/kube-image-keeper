// Package pacing paces what kuik reads from the registries it pulls from: one series of
// check windows and one of copy windows per host, the check rings of every (resource, host)
// pair and the copy queues of every source host. See "Scheduling" in docs/v3/spec.md.
package pacing

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// Owner is the resource a ring or a copy queue belongs to.
type Owner struct {
	// Kind is ImageMonitor or ImageMirror.
	Kind string
	// Name is the name of the cluster-scoped resource.
	Name string
}

// Response is what one check of a reference obtained, shared through the verdict cache: the
// descriptor carries the digest drift detection compares, Err the failure.
type Response struct {
	Result *registry.CheckResult
	Err    error
}

// Checker reads the references of one ring on behalf of the resource that owns it.
type Checker interface {
	// Check reads ref from its registry. ctx carries the check timeout of the host.
	Check(ctx context.Context, ref string) Response
	// Checked receives the response of ref, obtained by Check or reused from the verdict
	// cache.
	Checked(ref string, r Response)
}

// Copier copies the references a mirror owes from one source host.
type Copier interface {
	// Copy copies ref. ctx carries the copy timeout of the host, if any.
	Copy(ctx context.Context, ref string) error
}

// Scheduler opens the windows of every host and hands each one to a ring or a queue. It runs
// in the leader-elected reconciler.
type Scheduler struct {
	clk clock.Clock
	// wake interrupts the wait for the next window when a ring or the config changes.
	wake chan struct{}

	mu  sync.Mutex
	cfg *config.Config
	// ctx is the context of Start, the parent of every check.
	ctx context.Context
	// start is when Start ran, the origin of every series; zero before.
	start time.Time
	hosts map[string]*host
	// idle is true while the loop waits for its next window, due at due.
	idle    bool
	due     time.Time
	running int
}

// series is the windows of one loop against a host, opened at origin + k × interval.
type series struct {
	origin   time.Time
	interval time.Duration
	// busy is true while the image of the last window is still being read.
	busy bool
}

// next returns the first window of s strictly after now.
func (s *series) next(now time.Time) time.Time {
	k := now.Sub(s.origin)/s.interval + 1
	return s.origin.Add(k * s.interval)
}

// host is the budget of one registry host and the rings that share it.
type host struct {
	name  string
	check series
	// due is the next check window.
	due   time.Time
	rings []*ring
	// turn is the index of the ring the next check window goes to.
	turn int
	// cache holds the last response of every reference of the host, whichever ring got it.
	cache map[string]cached
}

// cached is a response and the window that obtained it.
type cached struct {
	response Response
	at       time.Time
}

// ring is the references one resource tracks on one host, in lexicographic order.
type ring struct {
	owner   Owner
	refs    []string
	checker Checker
	// cursor is the reference last taken.
	cursor        string
	cycleStarted  *time.Time
	cycleDuration *time.Duration
	// created and visits date what the ring already knows: a cached response newer than
	// the last visit of its reference, or than created for a reference never visited, is
	// one this ring has not seen yet.
	created time.Time
	visits  map[string]time.Time
}

// New returns a scheduler paced by cfg, whose windows count from Start.
func New(clk clock.Clock, cfg *config.Config) *Scheduler {
	return &Scheduler{
		clk:   clk,
		cfg:   cfg,
		wake:  make(chan struct{}, 1),
		hosts: map[string]*host{},
	}
}

// waiting reports whether the scheduler waits for its next window, so that a test can move
// a fake clock without racing it.
func (s *Scheduler) waiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idle && (s.due.IsZero() || s.clk.Now().Before(s.due))
}

// inFlight is the number of checks and copies running.
func (s *Scheduler) inFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Start opens windows until ctx is done.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.start = s.clk.Now()
	for _, h := range s.hosts {
		s.phase(h)
	}
	s.mu.Unlock()

	for {
		// The timer is armed before the loop reports itself idle, so that the clock cannot
		// move between the two. A wake posted before this pass is dropped: the pass reads
		// the current hosts anyway, and the stale wake would end the wait at once.
		var timer clock.Timer
		var fire <-chan time.Time
		s.mu.Lock()
		select {
		case <-s.wake:
		default:
		}
		now := s.clk.Now()
		next := s.open(now)
		if !next.IsZero() {
			timer = s.clk.NewTimer(next.Sub(now))
			fire = timer.C()
		}
		s.idle, s.due = true, next
		s.mu.Unlock()

		select {
		case <-ctx.Done():
		case <-fire:
		case <-s.wake:
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return nil
		}
		s.mu.Lock()
		s.idle = false
		s.mu.Unlock()
	}
}

// NeedLeaderElection is true: a single budget per host holds only in the leader.
func (s *Scheduler) NeedLeaderElection() bool {
	return true
}

// notify wakes the loop so that it re-reads the hosts. s.mu is held.
func (s *Scheduler) notify() {
	s.idle = false
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// phase sets the series of h from the start of the scheduler. s.mu is held.
func (s *Scheduler) phase(h *host) {
	p := s.cfg.Registries.For(h.name)
	h.check = series{origin: s.start, interval: p.Check.Interval}
	h.due = h.check.next(s.start)
}

// hostFor returns the host named name, created on first use. s.mu is held.
func (s *Scheduler) hostFor(name string) *host {
	h, ok := s.hosts[name]
	if !ok {
		h = &host{name: name, cache: map[string]cached{}}
		s.hosts[name] = h
		if !s.start.IsZero() {
			s.phase(h)
		}
	}
	return h
}

// open opens every window due at now and returns when the next one is. s.mu is held.
func (s *Scheduler) open(now time.Time) time.Time {
	var next time.Time
	for _, h := range s.hosts {
		if !now.Before(h.due) {
			s.openCheck(h, now)
			h.due = h.check.next(now)
		}
		if next.IsZero() || h.due.Before(next) {
			next = h.due
		}
	}
	return next
}

// openCheck hands a check window of h to the next ring with an image, in round-robin. A ring
// whose next image has a response it has not seen yet reuses it and the window moves on, so
// that it is spent on the first image that needs a request. The window is lost when no image
// needs one, or when the previous check still runs. s.mu is held.
func (s *Scheduler) openCheck(h *host, now time.Time) {
	if h.check.busy {
		return
	}
	// Each step takes one image, so a window that only finds reusable responses ends after
	// one lap of every ring.
	steps := len(h.rings)
	for _, r := range h.rings {
		steps += len(r.refs)
	}
	for range steps {
		r := h.rings[h.turn%len(h.rings)]
		h.turn = (h.turn + 1) % len(h.rings)
		if len(r.refs) == 0 {
			continue
		}
		known, visited := r.visits[r.peek()]
		if !visited {
			known = r.created
		}
		ref := r.take(now)
		if c, ok := h.cache[ref]; ok && c.at.After(known) {
			s.deliver(r.checker, ref, c.response)
			continue
		}
		h.check.busy = true
		s.running++
		go s.check(h, r.checker, ref, now, s.cfg.Registries.For(h.name).Check.Timeout)
		return
	}
}

// deliver hands a reused response to c outside the lock. s.mu is held.
func (s *Scheduler) deliver(c Checker, ref string, r Response) {
	s.running++
	go func() {
		c.Checked(ref, r)
		s.mu.Lock()
		s.running--
		s.mu.Unlock()
	}()
}

// check reads ref with c, abandoning it after timeout, caches the response as obtained by the
// window opened at, and hands it to c.
func (s *Scheduler) check(h *host, c Checker, ref string, at time.Time, timeout time.Duration) {
	ctx, cancel := s.bounded(timeout)
	r := c.Check(ctx, ref)
	cancel()

	s.mu.Lock()
	h.cache[ref] = cached{response: r, at: at}
	h.check.busy = false
	s.mu.Unlock()
	c.Checked(ref, r)

	s.mu.Lock()
	s.running--
	s.mu.Unlock()
}

// bounded returns a context of Start cancelled after timeout on the scheduler's clock, or
// never when timeout is 0.
func (s *Scheduler) bounded(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(s.ctx)
	if timeout == 0 {
		return ctx, cancel
	}
	timer := s.clk.NewTimer(timeout)
	go func() {
		select {
		case <-timer.C():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		timer.Stop()
		cancel()
	}
}

// next returns the index of the reference after the cursor, wrapping to the first one.
func (r *ring) next() int {
	i, found := slices.BinarySearch(r.refs, r.cursor)
	if found {
		i++
	}
	if i == len(r.refs) {
		i = 0
	}
	return i
}

// peek returns the reference take would return.
func (r *ring) peek() string {
	return r.refs[r.next()]
}

// take returns the reference after the cursor, moves the cursor there and records the
// visit. Taking the first reference ends the lap in progress and starts the next.
func (r *ring) take(now time.Time) string {
	i := r.next()
	if i == 0 {
		r.lap(now)
	}
	r.cursor = r.refs[i]
	r.visits[r.cursor] = now
	return r.cursor
}

// lap starts a lap at now, measuring the one it ends. A ring resumed without a lap start
// measures nothing until its next lap.
func (r *ring) lap(now time.Time) {
	if r.cycleStarted != nil && r.cursor != "" {
		d := now.Sub(*r.cycleStarted)
		r.cycleDuration = &d
	}
	r.cycleStarted = &now
}

// SetConfig applies a reloaded config: a series whose interval changed restarts from now,
// every other one keeps its phase, and no cursor moves.
func (s *Scheduler) SetConfig(cfg *config.Config) {}

// SetRing sets the references the ring of owner on host turns over, checked by c. resume is
// the ring's persisted status, read only when the ring does not exist yet in this process.
func (s *Scheduler) SetRing(owner Owner, host string, refs []string, resume kuikv1alpha1.RegistryCheck, c Checker) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h := s.hostFor(host)
	sorted := slices.Clone(refs)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	defer s.notify()
	defer h.prune()

	i := slices.IndexFunc(h.rings, func(r *ring) bool { return r.owner == owner })
	if i >= 0 {
		r := h.rings[i]
		r.refs = sorted
		r.checker = c
		maps.DeleteFunc(r.visits, func(ref string, _ time.Time) bool {
			_, found := slices.BinarySearch(sorted, ref)
			return !found
		})
		return
	}
	r := &ring{
		owner:   owner,
		refs:    sorted,
		checker: c,
		cursor:  resume.Cursor,
		created: s.clk.Now(),
		visits:  map[string]time.Time{},
	}
	if resume.Cursor != "" && resume.CycleStarted != nil {
		started := resume.CycleStarted.Time
		r.cycleStarted = &started
	}
	h.rings = append(h.rings, r)
}

// prune drops the cached responses of the references no ring of h tracks any more.
func (h *host) prune() {
	maps.DeleteFunc(h.cache, func(ref string, _ cached) bool {
		return !slices.ContainsFunc(h.rings, func(r *ring) bool {
			_, found := slices.BinarySearch(r.refs, ref)
			return found
		})
	})
}

// RemoveRing drops the ring of owner on host.
func (s *Scheduler) RemoveRing(owner Owner, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.hosts[host]
	if !ok {
		return
	}
	h.rings = slices.DeleteFunc(h.rings, func(r *ring) bool { return r.owner == owner })
	if len(h.rings) > 0 {
		h.turn %= len(h.rings)
	} else {
		h.turn = 0
	}
	h.prune()
	s.notify()
}

// RegistryChecks returns the health of every ring of owner, for its status.
func (s *Scheduler) RegistryChecks(owner Owner) []kuikv1alpha1.RegistryCheck {
	s.mu.Lock()
	defer s.mu.Unlock()

	var checks []kuikv1alpha1.RegistryCheck
	for _, h := range s.hosts {
		for _, r := range h.rings {
			if r.owner != owner {
				continue
			}
			check := kuikv1alpha1.RegistryCheck{
				Registry: h.name,
				Images:   int32(len(r.refs)),
				Cursor:   r.cursor,
			}
			if r.cycleStarted != nil {
				check.CycleStarted = &metav1.Time{Time: *r.cycleStarted}
			}
			if r.cycleDuration != nil {
				check.CycleDuration = &metav1.Duration{Duration: *r.cycleDuration}
			}
			checks = append(checks, check)
		}
	}
	slices.SortFunc(checks, func(a, b kuikv1alpha1.RegistryCheck) int {
		return cmp.Compare(a.Registry, b.Registry)
	})
	return checks
}

// SetCopyQueue sets the references owner still owes from host, copied by c in that order.
// An empty refs drops the queue.
func (s *Scheduler) SetCopyQueue(owner Owner, host string, refs []string, c Copier) {}

// Collector returns the scheduling health metrics, computed from the current rings and
// config on every scrape.
func (s *Scheduler) Collector() prometheus.Collector {
	return nil
}
