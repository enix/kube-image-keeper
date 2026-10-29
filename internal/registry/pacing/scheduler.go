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
	// Check reads ref from its registry. ctx carries the check timeout of the host. An empty
	// Response, neither a result nor an error, is no verdict: the cache never keeps it.
	Check(ctx context.Context, ref string) Response
	// Checked receives the response of ref, obtained by Check or reused from the verdict
	// cache.
	Checked(ref string, r Response)
}

// Copier copies the references a mirror owes from one source host.
type Copier interface {
	// Copy copies ref. ctx carries the copy timeout of the host, if any. An error is a failed
	// attempt: ref then waits before its next one (see SetCopyQueue). nil is no failure,
	// whether ref was copied or there was nothing to do.
	Copy(ctx context.Context, ref string) error
}

// maxCopyBackoff caps the wait of a reference whose copy keeps failing. The kubelet caps an
// image pull backoff at 5 minutes, below the default copy interval: 30 minutes is the cap
// the ImageMirror finalizer already uses.
const maxCopyBackoff = 30 * time.Minute

// copyWait is the backoff of a reference whose last copy failed: it takes no window opened
// before until, and its next failure waits twice delay.
type copyWait struct {
	until time.Time
	delay time.Duration
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
	// destinations are the destination hosts of the mirrors, by mirror, scanned whole rather
	// than paced: they hold no series and only export their scan interval.
	destinations map[Owner]string
	// waits are the backoffs of the references whose copy failed, by owner and reference:
	// a reference moving to the queue of another host keeps its own.
	waits map[Owner]map[string]*copyWait
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

// host is the budget of one registry host and the rings and queues that share it.
type host struct {
	name  string
	check series
	copy  series
	// due and copyDue are the next check and copy windows.
	due     time.Time
	copyDue time.Time
	queues  []*queue
	// copyTurn is the index of the queue the next copy window goes to.
	copyTurn int
	rings    []*ring
	// turn is the index of the ring the next check window goes to.
	turn int
	// cache holds the last response of every reference of the host, whichever ring got it.
	cache map[string]cached
}

// queue is the references one mirror still owes from one source host, in its order. It is
// drained, not cycled: what it holds is persisted nowhere.
type queue struct {
	owner  Owner
	refs   []string
	copier Copier
	// next is the reference the next attempt takes, the one after the last attempted: a
	// failing image does not hold back the others.
	next string
	// first are the references to copy ahead of next, in order, each one once; taken are
	// those of them already taken, which then wait for their turn like the others.
	first []string
	taken map[string]bool
}

// prioritize sets the references of q to copy first. One already taken since it was first
// is not taken ahead again: a copy that keeps failing would hold back the others.
func (q *queue) prioritize(first []string) {
	taken := map[string]bool{}
	q.first = nil
	for _, ref := range first {
		switch {
		case q.taken[ref]:
			taken[ref] = true
		case slices.Contains(q.refs, ref):
			q.first = append(q.first, ref)
		}
	}
	q.taken = taken
}

// takeFirst returns the first reference to copy first that is ready, leaving next as it is.
// One that waits keeps its place among them.
func (q *queue) takeFirst(ready func(string) bool) (string, bool) {
	for i, ref := range q.first {
		if ready(ref) {
			q.first = slices.Delete(q.first, i, i+1)
			q.taken[ref] = true
			return ref, true
		}
	}
	return "", false
}

// takeNext returns the next reference that is ready, from next on, or from the first one
// when there is none, and moves next to the one after it. It returns false, leaving next as
// it is, when every reference waits.
func (q *queue) takeNext(ready func(string) bool) (string, bool) {
	start := max(slices.Index(q.refs, q.next), 0)
	for k := range len(q.refs) {
		i := (start + k) % len(q.refs)
		if ready(q.refs[i]) {
			q.next = q.refs[(i+1)%len(q.refs)]
			return q.refs[i], true
		}
	}
	return "", false
}

// replace sets the references q owes. When next is gone, typically copied and dropped by
// the mirror, the position moves on to the first reference after it, in the previous order,
// that q still owes.
func (q *queue) replace(refs []string) {
	if i := slices.Index(q.refs, q.next); i >= 0 {
		q.next = ""
		for k := range len(q.refs) {
			if ref := q.refs[(i+k)%len(q.refs)]; slices.Contains(refs, ref) {
				q.next = ref
				break
			}
		}
	}
	q.refs = slices.Clone(refs)
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
		clk:          clk,
		cfg:          cfg,
		wake:         make(chan struct{}, 1),
		hosts:        map[string]*host{},
		destinations: map[Owner]string{},
		waits:        map[Owner]map[string]*copyWait{},
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
	h.copy = series{origin: s.start, interval: p.Copy.Interval}
	h.due = h.check.next(s.start)
	h.copyDue = h.copy.next(s.start)
}

// hostFor returns the host named name, created on first use. s.mu is held.
func (s *Scheduler) hostFor(name string) *host {
	h, ok := s.hosts[name]
	if !ok {
		h = &host{name: name, cache: map[string]cached{}}
		s.hosts[name] = h
		if !s.start.IsZero() {
			s.phase(h)
			// A host first used after the start keeps the phase of the process: its first
			// windows are the next ones of the series, never one already past.
			now := s.clk.Now()
			h.due = h.check.next(now)
			h.copyDue = h.copy.next(now)
		}
	}
	return h
}

// open opens every window due at now and returns when the next one is. s.mu is held.
func (s *Scheduler) open(now time.Time) time.Time {
	var next time.Time
	earliest := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	for _, h := range s.hosts {
		if !now.Before(h.due) {
			s.openCheck(h, now)
			h.due = h.check.next(now)
		}
		if !now.Before(h.copyDue) {
			// The window of the series being opened, whatever the timer's delay: the backoffs
			// count from it.
			s.openCopy(h, h.copy.next(now).Add(-h.copy.interval))
			h.copyDue = h.copy.next(now)
		}
		earliest(h.due)
		earliest(h.copyDue)
	}
	return next
}

// openCopy hands the copy window of h opened at opened to the next queue with a ready
// reference, in round-robin, unless a queue holds a ready reference to copy first: that one
// goes ahead, without taking the turn of the next queue. A reference that waits for its
// backoff is passed over. The window is lost when no queued reference is ready or the
// previous copy still transfers. s.mu is held.
func (s *Scheduler) openCopy(h *host, opened time.Time) {
	if h.copy.busy || len(h.queues) == 0 {
		return
	}
	n := len(h.queues)
	for k := range n {
		q := h.queues[(h.copyTurn+k)%n]
		if ref, ok := q.takeFirst(s.ready(q.owner, opened)); ok {
			s.startCopy(h, q, ref, opened)
			return
		}
	}
	for k := range n {
		i := (h.copyTurn + k) % n
		q := h.queues[i]
		if ref, ok := q.takeNext(s.ready(q.owner, opened)); ok {
			h.copyTurn = (i + 1) % n
			s.startCopy(h, q, ref, opened)
			return
		}
	}
}

// ready reports which references of owner may take the window opened at opened: those whose
// backoff, if any, is over. s.mu is held.
func (s *Scheduler) ready(owner Owner, opened time.Time) func(string) bool {
	waits := s.waits[owner]
	return func(ref string) bool {
		w, ok := waits[ref]
		return !ok || !opened.Before(w.until)
	}
}

// startCopy spends the window of h opened at opened on ref of q. s.mu is held.
func (s *Scheduler) startCopy(h *host, q *queue, ref string, opened time.Time) {
	h.copy.busy = true
	s.running++
	go s.copyImage(h, q.owner, q.copier, ref, opened, h.copy.interval, s.cfg.Registries.For(h.name).Copy.Timeout)
}

// copyImage copies ref with c, abandoning it after timeout unless timeout is 0. A failure
// makes ref wait from when it failed, or from opened, the window it took, for a copy that
// failed at once; its host copies every interval.
func (s *Scheduler) copyImage(h *host, owner Owner, c Copier, ref string, opened time.Time, interval, timeout time.Duration) {
	ctx, cancel := s.bounded(timeout)
	err := c.Copy(ctx, ref)
	cancel()

	s.mu.Lock()
	h.copy.busy = false
	s.running--
	if err != nil {
		// As the kubelet counts its backoff from the failure: a copy that outlasted its
		// window must not spend its wait while it was still transferring.
		s.backOff(owner, ref, latest(opened, s.clk.Now()), interval)
	}
	s.mu.Unlock()
}

// latest returns the later of a and b.
func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// backOff makes ref of owner, whose copy failed at failed, wait one interval, then twice as
// long as its last wait, up to maxCopyBackoff but never less than one interval. A reference
// no queue of owner holds any more has nothing to wait for. s.mu is held.
func (s *Scheduler) backOff(owner Owner, ref string, failed time.Time, interval time.Duration) {
	if !s.queued(owner, ref) {
		return
	}
	waits, ok := s.waits[owner]
	if !ok {
		waits = map[string]*copyWait{}
		s.waits[owner] = waits
	}
	w, ok := waits[ref]
	if !ok {
		w = &copyWait{}
		waits[ref] = w
	}
	w.delay = max(min(2*w.delay, maxCopyBackoff), interval)
	w.until = failed.Add(w.delay)
}

// queued reports whether a queue of owner holds ref. s.mu is held.
func (s *Scheduler) queued(owner Owner, ref string) bool {
	for _, h := range s.hosts {
		for _, q := range h.queues {
			if q.owner == owner && slices.Contains(q.refs, ref) {
				return true
			}
		}
	}
	return false
}

// forget drops the backoffs of the references of owner that left every queue of owner: one
// that comes back is copied at its next window. s.mu is held.
func (s *Scheduler) forget(owner Owner) {
	waits, ok := s.waits[owner]
	if !ok {
		return
	}
	maps.DeleteFunc(waits, func(ref string, _ *copyWait) bool { return !s.queued(owner, ref) })
	if len(waits) == 0 {
		delete(s.waits, owner)
	}
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
	// A response with neither a result nor an error is no verdict: another ring must not
	// reuse it.
	if r.Result != nil || r.Err != nil {
		h.cache[ref] = cached{response: r, at: at}
	}
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
// its first window a full interval later, every other one keeps its phase, and no cursor
// moves. Timeouts apply from the next window, without re-phasing anything: the restart
// exists to keep a shortened interval from bursting a host, and a timeout does not change the
// rate.
func (s *Scheduler) SetConfig(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old := s.cfg
	s.cfg = cfg
	if s.start.IsZero() {
		return
	}
	now := s.clk.Now()
	for _, h := range s.hosts {
		before, after := old.Registries.For(h.name), cfg.Registries.For(h.name)
		if before.Check.Interval != after.Check.Interval {
			h.check.origin, h.check.interval = now, after.Check.Interval
			h.due = h.check.next(now)
		}
		if before.Copy.Interval != after.Copy.Interval {
			h.copy.origin, h.copy.interval = now, after.Copy.Interval
			h.copyDue = h.copy.next(now)
		}
	}
	s.notify()
}

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
	// The last lap measured holds until the next one completes.
	if resume.CycleDuration != nil {
		d := resume.CycleDuration.Duration
		r.cycleDuration = &d
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

// unused reports whether no ring and no copy queue use h. Its windows go on opening empty and
// it keeps its phase and the image it may still be reading, so that using it again can neither
// burst the host nor read it twice at once: only its series are hidden.
func (h *host) unused() bool {
	return len(h.rings) == 0 && len(h.queues) == 0
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
				// To the second, as the API server stores it: a finer time would differ from
				// its own read-back and rewrite the status on every reconcile.
				started := metav1.NewTime(*r.cycleStarted).Rfc3339Copy()
				check.CycleStarted = &started
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
// first are references of refs to copy first: each is taken once at the next window of host,
// ahead of the position of the queue and of the other owners' turns, then waits for its turn
// like the others. An empty refs drops the queue.
//
// A reference whose copy failed waits before its next attempt: it takes no window opened
// before one copy interval after the failure, counted from its window when the copy failed
// within it, then twice as long after each
// new failure, up to 30 minutes. The wait is held by owner and reference, whatever the queue,
// and forgotten once the reference leaves every queue of owner. A reference moving to the
// queue of another host keeps it only when both queues change in one SetCopyQueues.
func (s *Scheduler) SetCopyQueue(owner Owner, host string, refs []string, c Copier, first ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notify()

	s.setCopyQueue(owner, s.hostFor(host), refs, c, first)
	s.forget(owner)
}

// CopyQueue is the references an owner still owes from one host, as SetCopyQueue takes them.
type CopyQueue struct {
	Refs   []string
	First  []string
	Copier Copier
}

// SetCopyQueues sets every copy queue of owner at once, by host, as SetCopyQueue does for
// each, and drops its queues on the other hosts.
func (s *Scheduler) SetCopyQueues(owner Owner, queues map[string]CopyQueue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notify()

	for name, h := range s.hosts {
		if _, ok := queues[name]; !ok {
			s.setCopyQueue(owner, h, nil, nil, nil)
		}
	}
	for name, q := range queues {
		s.setCopyQueue(owner, s.hostFor(name), q.Refs, q.Copier, q.First)
	}
	s.forget(owner)
}

// setCopyQueue sets the queue of owner on h. s.mu is held.
func (s *Scheduler) setCopyQueue(owner Owner, h *host, refs []string, c Copier, first []string) {
	i := slices.IndexFunc(h.queues, func(q *queue) bool { return q.owner == owner })
	switch {
	case len(refs) == 0 && i >= 0:
		h.queues = slices.Delete(h.queues, i, i+1)
		if len(h.queues) > 0 {
			h.copyTurn %= len(h.queues)
		} else {
			h.copyTurn = 0
		}
	case len(refs) == 0:
	case i >= 0:
		h.queues[i].replace(refs)
		h.queues[i].prioritize(first)
		h.queues[i].copier = c
	default:
		q := &queue{owner: owner, refs: slices.Clone(refs), copier: c}
		q.prioritize(first)
		h.queues = append(h.queues, q)
	}
}

// SetDestinationScan declares host the destination owner scans, in place of any previous one:
// its scan interval is exported with operation Scan, and no window ever opens on it.
func (s *Scheduler) SetDestinationScan(owner Owner, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destinations[owner] = host
}

// RemoveDestinationScan releases the destination owner scans: its Scan series goes once no
// other owner scans the same host.
func (s *Scheduler) RemoveDestinationScan(owner Owner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.destinations, owner)
}

// Collector returns the scheduling health metrics, computed from the current rings and
// config on every scrape.
func (s *Scheduler) Collector() prometheus.Collector {
	return collector{s: s}
}

var (
	ringLabels = []string{"kind", "name", "registry"}

	// The HELP texts are those of docs/v3/observability.md, "Scheduling health".
	cycleDurationDesc = prometheus.NewDesc("kuik_check_cycle_duration_seconds",
		"Wall-clock seconds taken by the last completed check lap over a registry's images. "+
			"Produced by an ImageMonitor for the images it tracks, and by an ImageMirror under "+
			"`driftPolicy: Warn` / `Sync` for the source tags it re-reads. Absent until a first lap completes",
		ringLabels, nil)
	cycleStartedDesc = prometheus.NewDesc("kuik_check_cycle_started_timestamp_seconds",
		"Unix timestamp at which the lap currently in progress over a registry's images started",
		ringLabels, nil)
	ringImagesDesc = prometheus.NewDesc("kuik_check_ring_images",
		"Size of the ring a resource turns over a registry: the images an ImageMonitor tracks there, "+
			"and under `driftPolicy: Warn` / `Sync` the copied tags an ImageMirror re-reads there. "+
			"The denominator behind the lap above",
		ringLabels, nil)
	intervalDesc = prometheus.NewDesc("kuik_registry_interval_seconds",
		"Configured pace at which kuik reads a registry for this operation, as currently loaded: "+
			"the window between two requests for `Check` and `Copy`, the period between two whole "+
			"passes for `Scan` (a mirror destination)",
		[]string{"registry", "operation"}, nil)
)

// collector reads the scheduling health from a scheduler on every scrape, so that a removed
// ring leaves no series behind and a reload shows at once.
type collector struct {
	s *Scheduler
}

func (c collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- cycleDurationDesc
	ch <- cycleStartedDesc
	ch <- ringImagesDesc
	ch <- intervalDesc
}

func (c collector) Collect(ch chan<- prometheus.Metric) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()

	for _, h := range c.s.hosts {
		if h.unused() {
			continue
		}
		p := c.s.cfg.Registries.For(h.name)
		ch <- prometheus.MustNewConstMetric(intervalDesc, prometheus.GaugeValue, p.Check.Interval.Seconds(), h.name, "Check")
		ch <- prometheus.MustNewConstMetric(intervalDesc, prometheus.GaugeValue, p.Copy.Interval.Seconds(), h.name, "Copy")

		for _, r := range h.rings {
			labels := []string{r.owner.Kind, r.owner.Name, h.name}
			ch <- prometheus.MustNewConstMetric(ringImagesDesc, prometheus.GaugeValue, float64(len(r.refs)), labels...)
			if r.cycleStarted != nil {
				ch <- prometheus.MustNewConstMetric(cycleStartedDesc, prometheus.GaugeValue, float64(r.cycleStarted.Unix()), labels...)
			}
			if r.cycleDuration != nil {
				ch <- prometheus.MustNewConstMetric(cycleDurationDesc, prometheus.GaugeValue, r.cycleDuration.Seconds(), labels...)
			}
		}
	}

	scan := c.s.cfg.Mirror.DestinationScan.Interval.Seconds()
	scanned := map[string]bool{}
	for _, host := range c.s.destinations {
		if !scanned[host] {
			scanned[host] = true
			ch <- prometheus.MustNewConstMetric(intervalDesc, prometheus.GaugeValue, scan, host, "Scan")
		}
	}
}
