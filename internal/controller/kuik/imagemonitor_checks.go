package kuik

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/status/condition"
)

// eventAnonymousAccessDenied is emitted once when an anonymous check is refused.
const eventAnonymousAccessDenied = "AnonymousAccessDenied"

// verdict is the outcome of the last check of one image.
type verdict struct {
	// reason is why the check failed, empty when the image answered.
	reason kuikv1alpha1.CheckFailureReason
	// since is when the failure was first observed, kept while it lasts.
	since time.Time
	// anonymous is set when the check that failed sent no credential and no fallbackAuth
	// entry covers the image: this monitor's own request, never a response reused from the
	// verdict cache.
	anonymous bool
	// denied is set once AnonymousAccessDenied announced the failure, or once a restart found
	// it in the status with reason Unauthorized. It holds while the image keeps failing,
	// whatever the reason, so that a failure is announced once.
	denied bool
}

// seed reads the failing images of status back into the verdicts, once per process: a
// restart keeps the since of a failure that lasts, and writes the list back as it was until
// the ring comes round.
func (m *monitorMemory) seed(status *kuikv1alpha1.ImageMonitorStatus) {
	if m.seeded {
		return
	}
	m.seeded = true
	if out := status.Truncated[listUnavailableImages]; out > 0 && status.Images != nil && status.Images.Origin != nil {
		m.persisted = &persistedCounts{unavailable: status.Images.Origin.Unavailable, truncated: out}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range status.UnavailableImages {
		if _, ok := m.verdicts[e.Ref]; !ok {
			// An Unauthorized entry the status holds was announced already.
			m.verdicts[e.Ref] = verdict{reason: e.Reason, since: e.Since.Time, denied: e.Reason == kuikv1alpha1.CheckUnauthorized}
		}
	}
}

// listUnavailableImages is the status field of the failing origins, a capped list.
const listUnavailableImages = "unavailableImages"

// persistedCounts are the failing images a status counted beyond the entries of its capped
// list.
type persistedCounts struct {
	unavailable int32
	truncated   int32
}

// held returns what the status persisted beyond its capped list while some tracked image has
// no verdict yet, and forgets it once every one has.
func (m *monitorMemory) held(tracked []string) (persistedCounts, bool) {
	if m.persisted == nil {
		return persistedCounts{}, false
	}
	m.mu.Lock()
	complete := !slices.ContainsFunc(tracked, func(ref string) bool {
		_, ok := m.verdicts[ref]
		return !ok
	})
	m.mu.Unlock()
	if complete {
		m.persisted = nil
		return persistedCounts{}, false
	}
	return *m.persisted, true
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
	for ref := range m.secretErrs {
		if !keep[ref] {
			delete(m.secretErrs, ref)
		}
	}
	for ref := range m.anonymous {
		if !keep[ref] {
			delete(m.anonymous, ref)
		}
	}
	return out
}

// setDeclaring records the pods declaring each tracked image, for the checks to read their
// pull secrets.
func (m *monitorMemory) setDeclaring(declaring map[string][]*corev1.Pod) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.declaring = declaring
}

// checkable returns the references of tracked a ring can check: the ones that parse.
func checkable(tracked []string) []string {
	out := make([]string, 0, len(tracked))
	for _, ref := range tracked {
		if _, err := imagepath.Parse(ref); err == nil {
			out = append(out, ref)
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

// Check reads ref from its registry with the credentials of docs/v3/spec.md, "No auth at
// all": an origin has no entry auth, so the pull secrets of every pod declaring it, the most
// specific fallbackAuth entry, then anonymous when none applies. A fallbackAuth Secret that
// cannot be read is skipped, as the kubelet skips a missing pull secret, and reported.
func (c monitorChecker) Check(ctx context.Context, ref string) pacing.Response {
	memory := c.r.monitor(c.name)
	if memory == nil {
		// The monitor was deleted while its window opened: no verdict, which the cache
		// never hands to another ring.
		return pacing.Response{}
	}
	parsed, err := imagepath.Parse(ref)
	if err != nil {
		return pacing.Response{Err: err}
	}
	memory.mu.Lock()
	pods := memory.declaring[ref]
	memory.mu.Unlock()

	var auths []authn.Authenticator
	// The pods declaring an image often share their pull secrets: each is tried once.
	seen := map[types.NamespacedName]bool{}
	for _, pod := range pods {
		unseen := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace}}
		for _, s := range pod.Spec.ImagePullSecrets {
			key := types.NamespacedName{Namespace: pod.Namespace, Name: s.Name}
			if !seen[key] {
				seen[key] = true
				unseen.Spec.ImagePullSecrets = append(unseen.Spec.ImagePullSecrets, s)
			}
		}
		if len(unseen.Spec.ImagePullSecrets) == 0 {
			continue
		}
		creds, err := c.r.podSecrets.Resolve(ctx, parsed, nil, unseen)
		if err == nil {
			auths = append(auths, authenticators(ref, creds)...)
		}
	}
	creds, err := c.r.resolver.Resolve(ctx, parsed, nil, nil)
	memory.mu.Lock()
	switch {
	case isSecretError(err):
		memory.secretErrs[ref] = err
	case err == nil:
		delete(memory.secretErrs, ref)
		// A transient read error leaves the previous verdict on the Secret as it was.
	}
	memory.mu.Unlock()
	// A matching fallbackAuth entry covers the image even when it yields no credential: its
	// broken Secret is reported by Ready, which names the remedy, never by AnonymousAccessDenied.
	covered := err != nil || slices.ContainsFunc(creds, func(c auth.Credential) bool { return c.Source != auth.SourceAnonymous })
	if err == nil {
		auths = append(auths, authenticators(ref, creds)...)
	} else {
		logf.FromContext(ctx).V(1).Info("Skipped the fallbackAuth credential of an image", "image", ref, "error", err.Error())
	}

	result, err := c.r.registry.Check(ctx, registry.Endpoint{Reference: ref, Auth: distinct(auths)})
	memory.mu.Lock()
	memory.anonymous[ref] = len(auths) == 0 && !covered
	memory.mu.Unlock()
	return pacing.Response{Result: result, Err: err}
}

// Checked records the verdict of ref: available when it answered, unavailable with the
// reason of the failure otherwise, since the first failure while it lasts.
func (c monitorChecker) Checked(ref string, response pacing.Response) {
	memory := c.r.monitor(c.name)
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
	// Only this monitor's own request says whether it was anonymous: a response reused from
	// another ring finds no flag here.
	anonymous := memory.anonymous[ref]
	delete(memory.anonymous, ref)
	next := verdict{}
	if response.Err != nil {
		next = verdict{reason: checkFailure(response.Err), since: now, anonymous: anonymous}
		if known && previous.reason != "" {
			next.since = previous.since
			next.denied = previous.denied
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

// monitor returns the memory of the monitor name, nil once it was deleted.
func (r *ImageMonitorReconciler) monitor(name string) *monitorMemory {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.monitors[name]
}

// announceDenied emits AnonymousAccessDenied once for each image whose anonymous check the
// registry refused with Unauthorized: a private image nobody declared a credential for.
func (r *ImageMonitorReconciler) announceDenied(im *kuikv1alpha1.ImageMonitor, memory *monitorMemory, verdicts map[string]verdict) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	for ref, v := range verdicts {
		if v.reason != kuikv1alpha1.CheckUnauthorized || !v.anonymous || v.denied {
			continue
		}
		r.recorder.Eventf(im, nil, corev1.EventTypeWarning, eventAnonymousAccessDenied, "Check",
			"Pull access denied for %s: the registry refused an anonymous check, the repository may require authorization. "+
				"Declare a fallbackAuth entry covering it", ref)
		v.denied = true
		verdicts[ref] = v
		if current, ok := memory.verdicts[ref]; ok && current.reason != "" {
			current.denied = true
			memory.verdicts[ref] = current
		}
	}
}

// fallbackSecrets reports SecretNotFound or SecretMalformed while the fallbackAuth Secret of
// a tracked image cannot be read. Each image a check found with a broken Secret is resolved
// again against the current fallbackAuth, every Secret read once, so that Ready returns as
// soon as the Secret is fixed or the image matches another entry, rather than when the ring
// comes round. A transient read error keeps the previous verdict.
func (r *ImageMonitorReconciler) fallbackSecrets(ctx context.Context, memory *monitorMemory) *condition.NotReady {
	memory.mu.Lock()
	broken := maps.Clone(memory.secretErrs)
	memory.mu.Unlock()
	refs := slices.Sorted(maps.Keys(broken))
	if len(refs) > 0 {
		reader := &onceReader{Reader: r.secretReader, read: map[client.ObjectKey]onceRead{}}
		resolver := auth.NewResolver(reader, r.namespace, auth.ModeReconciler)
		// The entries were validated when they were loaded.
		_ = resolver.SetFallbackAuth(*r.fallbackAuth.Load())
		for _, ref := range refs {
			parsed, err := imagepath.Parse(ref)
			if err != nil {
				continue
			}
			_, err = resolver.Resolve(ctx, parsed, nil, nil)
			switch {
			case isSecretError(err):
				broken[ref] = err
				memory.mu.Lock()
				memory.secretErrs[ref] = err
				memory.mu.Unlock()
			case err == nil:
				memory.mu.Lock()
				delete(memory.secretErrs, ref)
				memory.mu.Unlock()
				delete(broken, ref)
			}
		}
	}
	for _, ref := range refs {
		err, ok := broken[ref]
		if !ok {
			continue
		}
		message := fmt.Sprintf("the fallbackAuth credential of %s: %s", ref, err)
		if _, notFound := errors.AsType[*auth.ErrSecretNotFound](err); notFound {
			return &condition.NotReady{Reason: kuikv1alpha1.ReasonSecretNotFound, Message: message}
		}
		return &condition.NotReady{Reason: kuikv1alpha1.ReasonSecretMalformed, Message: message}
	}
	return nil
}

// isSecretError reports whether err is a declared Secret missing or of the wrong type.
func isSecretError(err error) bool {
	if _, ok := errors.AsType[*auth.ErrSecretNotFound](err); ok {
		return true
	}
	_, ok := errors.AsType[*auth.ErrSecretMalformed](err)
	return ok
}

// onceReader reads each Secret once, for the lifetime of one report.
type onceReader struct {
	client.Reader
	read map[client.ObjectKey]onceRead
}

// onceRead is the outcome of one read.
type onceRead struct {
	secret corev1.Secret
	err    error
}

func (o *onceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return o.Reader.Get(ctx, key, obj, opts...)
	}
	read, ok := o.read[key]
	if !ok {
		read.err = o.Reader.Get(ctx, key, &read.secret, opts...)
		o.read[key] = read
	}
	if read.err == nil {
		read.secret.DeepCopyInto(secret)
	}
	return read.err
}

// distinct keeps the first of the authenticators that carry the same credential: a pull
// Secret copied in every namespace costs one request, not one per copy.
func distinct(auths []authn.Authenticator) []authn.Authenticator {
	seen := map[authn.AuthConfig]bool{}
	out := make([]authn.Authenticator, 0, len(auths))
	for _, a := range auths {
		cfg, err := a.Authorization()
		if err != nil || cfg == nil {
			out = append(out, a)
			continue
		}
		if seen[*cfg] {
			continue
		}
		seen[*cfg] = true
		out = append(out, a)
	}
	return out
}
