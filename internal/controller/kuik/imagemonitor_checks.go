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

// seed reads the failing images and the drifted tags of status back into the memory, once per
// process: a restart keeps the since of a failure or a drift that lasts, announces neither
// again, and writes the lists back as they were until the ring comes round.
func (m *monitorMemory) seed(status *kuikv1alpha1.ImageMonitorStatus) {
	if m.seeded {
		return
	}
	m.seeded = true
	if status.Images != nil && status.Images.Origin != nil {
		held := persistedCounts{}
		if out := status.Truncated[listUnavailableImages]; out > 0 {
			held.unavailable, held.truncated = status.Images.Origin.Unavailable, out
		}
		if out := status.Truncated[listDriftedImages]; out > 0 {
			held.drifted, held.driftTruncated = status.Images.Origin.Drifted, out
		}
		if out := status.Truncated[listUnavailableAlternatives]; out > 0 && status.Images.Alternatives != nil {
			held.altUnavailable, held.altTruncated = status.Images.Alternatives.Unavailable, out
		}
		if held != (persistedCounts{}) {
			m.persisted = &held
		}
	}
	// An Unauthorized alternative the status holds was announced already.
	for _, e := range status.UnavailableAlternatives {
		if e.Reason == kuikv1alpha1.CheckUnauthorized {
			m.unusable[e.Ref] = true
		}
	}
	// A drift the status holds was announced already, its skew too when it lists several digests.
	for _, e := range status.DriftedImages {
		m.drifts[e.Ref] = driftState{since: e.Since.Time, skewed: len(e.RunningDigests) > 1}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range status.DriftedImages {
		if _, ok := m.upstream[e.Ref]; !ok {
			m.upstream[e.Ref] = e.UpstreamDigest
		}
	}
	for _, e := range status.UnavailableImages {
		if _, ok := m.verdicts[e.Ref]; !ok {
			// An Unauthorized entry the status holds was announced already.
			m.verdicts[e.Ref] = verdict{reason: e.Reason, since: e.Since.Time, denied: e.Reason == kuikv1alpha1.CheckUnauthorized}
		}
	}
	// An alternative several origins stand for has one entry per origin, all from one verdict.
	for _, e := range status.UnavailableAlternatives {
		if _, ok := m.verdicts[e.Ref]; !ok {
			m.verdicts[e.Ref] = verdict{reason: e.Reason, since: e.Since.Time}
		}
	}
}

// listUnavailableImages is the status field of the failing origins, a capped list.
const listUnavailableImages = "unavailableImages"

// listDriftedImages is the status field of the drifted tags, a capped list.
const listDriftedImages = "driftedImages"

// persistedCounts are the failing images and the drifted tags a status counted beyond the
// entries of its capped lists.
type persistedCounts struct {
	unavailable int32
	truncated   int32
	// drifted and driftTruncated are the same for driftedImages.
	drifted        int32
	driftTruncated int32
	// altUnavailable and altTruncated are the same for unavailableAlternatives.
	altUnavailable int32
	altTruncated   int32
}

// listUnavailableAlternatives is the status field of the failing alternatives, a capped list.
const listUnavailableAlternatives = "unavailableAlternatives"

// heldAlternatives returns what the status persisted beyond unavailableAlternatives while some
// alternative of alternatives has no verdict yet, and forgets it once every one has.
func (m *monitorMemory) heldAlternatives(alternatives []string) (persistedCounts, bool) {
	if m.persisted == nil {
		return persistedCounts{}, false
	}
	m.mu.Lock()
	complete := !slices.ContainsFunc(alternatives, func(ref string) bool {
		_, ok := m.verdicts[ref]
		return !ok
	})
	m.mu.Unlock()
	if complete {
		m.persisted.altUnavailable, m.persisted.altTruncated = 0, 0
	}
	if *m.persisted == (persistedCounts{}) {
		m.persisted = nil
		return persistedCounts{}, false
	}
	return *m.persisted, true
}

// held returns what the status persisted beyond its capped lists: the failing images while
// some tracked image has no verdict yet, the drifted tags while pendingDrift tags have no
// upstream digest known yet. It forgets each part once that no longer holds.
func (m *monitorMemory) held(tracked []string, pendingDrift int32) (persistedCounts, bool) {
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
		m.persisted.unavailable, m.persisted.truncated = 0, 0
	}
	// The drift the status persisted is the last thing known of the tags no check read an
	// upstream digest of yet: a failed check tells nothing of their drift.
	if pendingDrift == 0 {
		m.persisted.drifted, m.persisted.driftTruncated = 0, 0
	}
	if *m.persisted == (persistedCounts{}) {
		m.persisted = nil
		return persistedCounts{}, false
	}
	return *m.persisted, true
}

// forgetHeldDrift drops what the status persisted beyond driftedImages, and the persisted
// counts once nothing else is left in them.
func (m *monitorMemory) forgetHeldDrift() {
	if m.persisted == nil {
		return
	}
	m.persisted.drifted, m.persisted.driftTruncated = 0, 0
	if *m.persisted == (persistedCounts{}) {
		m.persisted = nil
	}
}

// undecided counts the tags of declared whose drift cannot be told yet: a tag a container
// runs, neither digest-pinned nor unparseable, with no upstream digest known, whether its
// check failed or did not run yet.
func undecided(declared map[string]declaredImage, upstream map[string]string) int32 {
	var n int32
	for ref, d := range declared {
		if _, err := imagepath.Parse(ref); err != nil || !d.running || pinned(ref) {
			continue
		}
		if _, known := upstream[ref]; !known {
			n++
		}
	}
	return n
}

// verdictsOf returns the verdicts and the upstream digests of the tracked images, read
// together so that a check landing meanwhile is in both or in neither, and forgets those of
// the images no longer tracked.
func (m *monitorMemory) verdictsOf(tracked []string) (map[string]verdict, map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]bool, len(tracked))
	out := make(map[string]verdict, len(tracked))
	upstream := make(map[string]string, len(tracked))
	for _, ref := range tracked {
		keep[ref] = true
		if v, ok := m.verdicts[ref]; ok {
			out[ref] = v
		}
		if digest, ok := m.upstream[ref]; ok {
			upstream[ref] = digest
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
	for ref := range m.upstream {
		if !keep[ref] {
			delete(m.upstream, ref)
		}
	}
	return out, upstream
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
	// An alternative is read with the credentials of what offers it: the auth of its entries,
	// then the pull secrets of the pods of its origins, next to those of the pods declaring it
	// when it is an origin too.
	pods := slices.Clone(memory.declaring[ref])
	isOrigin := memory.origins[ref]
	var sources []alternativeSource
	if a, ok := memory.alternatives[ref]; ok {
		sources = a.sources()
		for _, origin := range slices.Sorted(maps.Keys(a.origins)) {
			pods = append(pods, memory.declaring[origin]...)
		}
	}
	memory.mu.Unlock()

	// Each entry's auth goes over its own entry's transport only.
	entries := make([]entryCredentials[authn.Authenticator], 0, len(sources))
	for _, source := range sources {
		entry := entryCredentials[authn.Authenticator]{insecure: source.config.Insecure}
		if source.config.Auth != nil {
			// Webhook mode resolves the entry's auth alone. A broken entry Secret is skipped for
			// the next credential, and never moves Ready: the ImageAlternative reports it.
			creds, err := c.r.podSecrets.Resolve(ctx, parsed, source.config.Auth, nil)
			if err == nil {
				entry.auths = authenticators(ref, creds)
			} else {
				logf.FromContext(ctx).V(1).Info("Skipped the auth of an alternative's entry", "image", ref,
					"resource", viaOf(source), "error", err.Error())
			}
		}
		entries = append(entries, entry)
	}
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

	// auths are the shared credentials: the pull secrets of the pods, then fallbackAuth.
	var (
		result *registry.CheckResult
		sent   int
	)
	for _, attempt := range checkPlan(entries, auths, isOrigin) {
		creds := distinct(attempt.auths)
		sent += len(creds)
		result, err = c.r.registry.Check(ctx, registry.Endpoint{Reference: ref, Insecure: attempt.insecure, Auth: creds})
		// The next transport is tried only when this one refused every credential it was sent.
		if err == nil || checkFailure(err) != kuikv1alpha1.CheckUnauthorized {
			break
		}
	}
	memory.mu.Lock()
	memory.anonymous[ref] = sent == 0 && !covered
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
	} else if response.Result != nil {
		// Drift detection reads the digest of this same answer: it sends no request of its own.
		memory.upstream[ref] = response.Result.Descriptor.Digest.String()
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
