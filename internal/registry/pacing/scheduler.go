// Package pacing paces what kuik reads from the registries it pulls from: one series of
// check windows and one of copy windows per host, the check rings of every (resource, host)
// pair and the copy queues of every source host. See "Scheduling" in docs/v3/spec.md.
package pacing

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
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
type Scheduler struct{}

// New returns a scheduler paced by cfg, whose windows count from Start.
func New(clk clock.Clock, cfg *config.Config) *Scheduler {
	return &Scheduler{}
}

// waiting reports whether the scheduler waits for its next window, so that a test can move
// a fake clock without racing it.
func (s *Scheduler) waiting() bool {
	return true
}

// inFlight is the number of checks and copies running.
func (s *Scheduler) inFlight() int {
	return 0
}

// Start opens windows until ctx is done.
func (s *Scheduler) Start(ctx context.Context) error {
	return nil
}

// NeedLeaderElection is true: a single budget per host holds only in the leader.
func (s *Scheduler) NeedLeaderElection() bool {
	return true
}

// SetConfig applies a reloaded config: a series whose interval changed restarts from now,
// every other one keeps its phase, and no cursor moves.
func (s *Scheduler) SetConfig(cfg *config.Config) {}

// SetRing sets the references the ring of owner on host turns over, checked by c. resume is
// the ring's persisted status, read only when the ring does not exist yet in this process.
func (s *Scheduler) SetRing(owner Owner, host string, refs []string, resume kuikv1alpha1.RegistryCheck, c Checker) {
}

// RemoveRing drops the ring of owner on host.
func (s *Scheduler) RemoveRing(owner Owner, host string) {}

// RegistryChecks returns the health of every ring of owner, for its status.
func (s *Scheduler) RegistryChecks(owner Owner) []kuikv1alpha1.RegistryCheck {
	return nil
}

// SetCopyQueue sets the references owner still owes from host, copied by c in that order.
// An empty refs drops the queue.
func (s *Scheduler) SetCopyQueue(owner Owner, host string, refs []string, c Copier) {}

// Collector returns the scheduling health metrics, computed from the current rings and
// config on every scrape.
func (s *Scheduler) Collector() prometheus.Collector {
	return nil
}
