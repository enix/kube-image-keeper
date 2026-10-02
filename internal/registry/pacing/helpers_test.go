package pacing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// origin is when every spec starts its scheduler.
var origin = time.Date(2026, 7, 10, 13, 32, 0, 0, time.UTC)

// at is origin plus d.
func at(d time.Duration) time.Time {
	return origin.Add(d)
}

// window is a `check` or `copy` block, a zero field left out.
func window(interval, timeout time.Duration) *config.Window {
	w := &config.Window{}
	if interval != 0 {
		w.Interval = &config.Duration{Duration: interval}
	}
	if timeout != 0 {
		w.Timeout = &config.Duration{Duration: timeout}
	}
	return w
}

// pacing is a config whose `registries.default` checks every check and copies every copy,
// with timeouts long enough to stay out of the way, and hosts as the host blocks.
func pacing(check, copy time.Duration, hosts map[string]config.RegistryPacing) *config.Config {
	if hosts == nil {
		hosts = map[string]config.RegistryPacing{}
	}
	return &config.Config{Registries: config.Registries{
		Default: config.RegistryPacing{
			Check: window(check, time.Hour),
			Copy:  window(copy, time.Hour),
		},
		Hosts: hosts,
	}}
}

// harness runs a scheduler on a fake clock started at origin.
type harness struct {
	clk *clocktesting.FakeClock
	s   *Scheduler
}

// run starts a scheduler paced by cfg and stops it when the spec ends.
func run(cfg *config.Config) *harness {
	clk := clocktesting.NewFakeClock(origin)
	s := New(clk, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		Expect(s.Start(ctx)).To(Succeed())
	}()
	DeferCleanup(func() {
		cancel()
		Eventually(done).Should(BeClosed())
	})
	return &harness{clk: clk, s: s}
}

// settle waits until the scheduler waits for its next window with nothing in flight. The
// loop is waited for first: until it has opened the windows due, nothing is in flight yet.
func (h *harness) settle() {
	GinkgoHelper()
	Eventually(h.s.waiting).Should(BeTrue())
	Eventually(h.s.inFlight).Should(BeZero())
}

// step moves the clock by d once the scheduler waits for its next window, whatever is in
// flight.
func (h *harness) step(d time.Duration) {
	GinkgoHelper()
	Eventually(h.s.waiting).Should(BeTrue())
	h.clk.Step(d)
}

// advance moves the clock by d once the scheduler is settled, and lets it settle again.
func (h *harness) advance(d time.Duration) {
	GinkgoHelper()
	h.settle()
	h.clk.Step(d)
	h.settle()
}

// gauges collects the series of the metric name from c, keyed by their labels written
// `label=value,...` in label order.
func gauges(c prometheus.Collector, name string) map[string]float64 {
	GinkgoHelper()
	reg := prometheus.NewPedanticRegistry()
	Expect(reg.Register(c)).To(Succeed())
	families, err := reg.Gather()
	Expect(err).NotTo(HaveOccurred())

	series := map[string]float64{}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := make([]string, 0, len(m.GetLabel()))
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName()+"="+l.GetValue())
			}
			series[strings.Join(labels, ",")] = m.GetGauge().GetValue()
		}
	}
	return series
}

// answer is the response a fake checker gets for ref: a digest of its own, so that a reused
// response can be told from a fresh one.
func answer(ref string) Response {
	digest := v1.Hash{Algorithm: "sha256", Hex: fmt.Sprintf("%x", sha256.Sum256([]byte(ref)))}
	return Response{Result: &registry.CheckResult{Descriptor: v1.Descriptor{Digest: digest}}}
}

// visit is one call a fake checker received.
type visit struct {
	Ref string
	At  time.Time
}

// fakeChecker records the checks a ring asks for and the responses it receives. Check
// answers at once, unless the ref is held: it then waits for release or for its context.
type fakeChecker struct {
	clk *clocktesting.FakeClock

	mu      sync.Mutex
	checks  []visit
	checked map[string][]Response
	held    map[string]chan struct{}
	ended   map[string]time.Time
}

func newChecker(h *harness) *fakeChecker {
	return &fakeChecker{
		clk:     h.clk,
		checked: map[string][]Response{},
		held:    map[string]chan struct{}{},
		ended:   map[string]time.Time{},
	}
}

// hold makes the next checks of ref wait until release.
func (f *fakeChecker) hold(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held[ref] = make(chan struct{})
}

// release lets the held checks of ref answer.
func (f *fakeChecker) release(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.held[ref])
	delete(f.held, ref)
}

func (f *fakeChecker) Check(ctx context.Context, ref string) Response {
	f.mu.Lock()
	f.checks = append(f.checks, visit{Ref: ref, At: f.clk.Now()})
	held := f.held[ref]
	f.mu.Unlock()

	if held == nil {
		return answer(ref)
	}
	select {
	case <-held:
		return answer(ref)
	case <-ctx.Done():
		f.mu.Lock()
		f.ended[ref] = f.clk.Now()
		f.mu.Unlock()
		return Response{Err: ctx.Err()}
	}
}

func (f *fakeChecker) Checked(ref string, r Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked[ref] = append(f.checked[ref], r)
}

// visits returns the checks received so far.
func (f *fakeChecker) visits() []visit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]visit(nil), f.checks...)
}

// refs returns the references checked so far, in order.
func (f *fakeChecker) refs() []string {
	visits := f.visits()
	refs := make([]string, 0, len(visits))
	for _, v := range visits {
		refs = append(refs, v.Ref)
	}
	return refs
}

// abandonedAt returns when the context of a held check of ref was cancelled.
func (f *fakeChecker) abandonedAt(ref string) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended[ref]
}

// responses returns the responses ref received.
func (f *fakeChecker) responses(ref string) []Response {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Response(nil), f.checked[ref]...)
}

// fakeCopier records the copies a queue asks for. Copy succeeds at once, unless the ref is
// held, then it waits for release or for its context, or failing, then it returns an error.
type fakeCopier struct {
	clk *clocktesting.FakeClock

	mu      sync.Mutex
	copies  []visit
	held    map[string]chan struct{}
	ended   map[string]time.Time
	failing map[string]bool
}

func newCopier(h *harness) *fakeCopier {
	return &fakeCopier{
		clk:     h.clk,
		held:    map[string]chan struct{}{},
		ended:   map[string]time.Time{},
		failing: map[string]bool{},
	}
}

// hold makes the next copies of ref wait until release.
func (f *fakeCopier) hold(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held[ref] = make(chan struct{})
}

// release lets the held copies of ref finish.
func (f *fakeCopier) release(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.held[ref])
	delete(f.held, ref)
}

// fail makes every copy of ref fail.
func (f *fakeCopier) fail(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[ref] = true
}

func (f *fakeCopier) Copy(ctx context.Context, ref string) error {
	f.mu.Lock()
	f.copies = append(f.copies, visit{Ref: ref, At: f.clk.Now()})
	held, failing := f.held[ref], f.failing[ref]
	f.mu.Unlock()

	if held != nil {
		select {
		case <-held:
		case <-ctx.Done():
			f.mu.Lock()
			f.ended[ref] = f.clk.Now()
			f.mu.Unlock()
			return ctx.Err()
		}
	}
	if failing {
		return errors.New("copy failed")
	}
	return nil
}

// visits returns the copies received so far.
func (f *fakeCopier) visits() []visit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]visit(nil), f.copies...)
}

// refs returns the references copied so far, in order.
func (f *fakeCopier) refs() []string {
	visits := f.visits()
	refs := make([]string, 0, len(visits))
	for _, v := range visits {
		refs = append(refs, v.Ref)
	}
	return refs
}

// abandonedAt returns when the context of a held copy of ref was cancelled.
func (f *fakeCopier) abandonedAt(ref string) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended[ref]
}
