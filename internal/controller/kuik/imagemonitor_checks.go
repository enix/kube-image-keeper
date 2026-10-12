package kuik

import (
	"context"
	"errors"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
)

// verdict is the outcome of the last check of one image.
type verdict struct {
	// reason is why the check failed, empty when the image answered.
	reason kuikv1alpha1.CheckFailureReason
	// since is when the failure was first observed, kept while it lasts.
	since time.Time
}

// seed reads the failing images of status back into the verdicts, once per process: a
// restart keeps the since of a failure that lasts, and writes the list back as it was until
// the ring comes round.
func (m *monitorMemory) seed(status *kuikv1alpha1.ImageMonitorStatus) {
	if m.seeded {
		return
	}
	m.seeded = true
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range status.UnavailableImages {
		if _, ok := m.verdicts[e.Ref]; !ok {
			m.verdicts[e.Ref] = verdict{reason: e.Reason, since: e.Since.Time}
		}
	}
}

// verdictsOf returns the verdicts of the tracked images, and forgets those of the images no
// longer tracked.
func (m *monitorMemory) verdictsOf(tracked []string) map[string]verdict {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]bool, len(tracked))
	out := make(map[string]verdict, len(tracked))
	for _, ref := range tracked {
		keep[ref] = true
		if v, ok := m.verdicts[ref]; ok {
			out[ref] = v
		}
	}
	for ref := range m.verdicts {
		if !keep[ref] {
			delete(m.verdicts, ref)
		}
	}
	return out
}

// turnRings hands the scheduler one ring per registry host of the images im tracks,
// resuming each from the cursor its status persisted, and drops the rings of the hosts it
// tracks nothing on any more.
func (r *ImageMonitorReconciler) turnRings(ctx context.Context, im *kuikv1alpha1.ImageMonitor, memory *monitorMemory, tracked []string) {
	if r.scheduler == nil {
		return
	}
	rings := map[string][]string{}
	for _, ref := range tracked {
		parsed, err := imagepath.Parse(ref)
		if err != nil {
			// A reference no registry can be asked about.
			continue
		}
		rings[parsed.Host] = append(rings[parsed.Host], ref)
	}
	resume := map[string]kuikv1alpha1.RegistryCheck{}
	if im.Status.Checks != nil {
		for _, ring := range im.Status.Checks.Registries {
			resume[ring.Registry] = ring
		}
	}
	owner := pacing.Owner{Kind: kindImageMonitor, Name: im.Name}
	checker := monitorChecker{r: r, name: im.Name}
	var gone []string
	for host := range memory.rings {
		if _, ok := rings[host]; !ok {
			gone = append(gone, host)
		}
	}
	r.dropRings(ctx, im.Name, memory, gone)
	for host, refs := range rings {
		r.scheduler.SetRing(owner, host, refs, resume[host], checker)
		memory.rings[host] = true
	}
}

// dropRings removes the rings of the monitor name on hosts.
func (r *ImageMonitorReconciler) dropRings(ctx context.Context, name string, memory *monitorMemory, hosts []string) {
	if r.scheduler == nil {
		return
	}
	owner := pacing.Owner{Kind: kindImageMonitor, Name: name}
	for _, host := range hosts {
		r.scheduler.RemoveRing(owner, host)
		delete(memory.rings, host)
		logf.FromContext(ctx).Info("Removed the check ring of an ImageMonitor", "registry", host)
	}
}

// checks is the status of the rings of the monitor name, nil when it holds none.
func (r *ImageMonitorReconciler) checks(name string) *kuikv1alpha1.ChecksStatus {
	registries := r.scheduler.RegistryChecks(pacing.Owner{Kind: kindImageMonitor, Name: name})
	if len(registries) == 0 {
		return nil
	}
	return &kuikv1alpha1.ChecksStatus{Registries: registries}
}

// monitorChecker checks the images one ImageMonitor tracks.
type monitorChecker struct {
	r    *ImageMonitorReconciler
	name string
}

// Check reads ref from its registry.
func (c monitorChecker) Check(ctx context.Context, ref string) pacing.Response {
	result, err := c.r.registry.Check(ctx, registry.Endpoint{Reference: ref})
	return pacing.Response{Result: result, Err: err}
}

// Checked records the verdict of ref: available when it answered, unavailable with the
// reason of the failure otherwise, since the first failure while it lasts.
func (c monitorChecker) Checked(ref string, response pacing.Response) {
	c.r.mu.Lock()
	memory := c.r.monitors[c.name]
	c.r.mu.Unlock()
	if memory == nil {
		// The monitor was deleted while the check ran.
		return
	}
	log := logf.Log.WithValues("resource", kindImageMonitor+"/"+c.name, "image", ref)
	// Status times are written to the second: a finer since would differ from its own
	// read-back and rewrite the status on every report.
	now := metav1.NewTime(c.r.clock.Now()).Rfc3339Copy().Time

	memory.mu.Lock()
	previous, known := memory.verdicts[ref]
	next := verdict{}
	if response.Err != nil {
		next = verdict{reason: checkFailure(response.Err), since: now}
		if known && previous.reason != "" {
			next.since = previous.since
		}
	}
	memory.verdicts[ref] = next
	memory.mu.Unlock()

	switch {
	case next.reason != "" && (!known || previous.reason == ""):
		log.Info("Found image unavailable", "reason", next.reason, "error", response.Err.Error())
	case next.reason != "":
		log.V(1).Info("Failed to check image", "reason", next.reason, "error", response.Err.Error())
	case known && previous.reason != "":
		log.Info("Found image available again")
	default:
		log.V(1).Info("Checked image")
	}
	c.r.wake(c.name)
}

// checkFailure is the reason a failed check reports: the registry's own, or Unreachable for a
// check that never reached it.
func checkFailure(err error) kuikv1alpha1.CheckFailureReason {
	if checkErr, ok := errors.AsType[*registry.CheckError](err); ok {
		return checkErr.Reason
	}
	return kuikv1alpha1.CheckUnreachable
}
