package kuik

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath/plan"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/keychain"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
)

// mirrorState is what the reconciler remembers about one ImageMirror between reconciles. None
// of it is persisted: a restart recomputes it from the destination and the pods.
type mirrorState struct {
	mu sync.Mutex
	// copied are the references copied by this process, keyed by their String form.
	copied map[string]bool
	// sources are where each owed reference is read from, keyed the same way.
	sources map[string]*copySource
	// failed is the last failure of each reference whose copy is failing.
	failed map[string]copyFailure
	// fresh are the failures observed since the last status write, which reports them.
	fresh []kuikv1alpha1.FailedImageCopy
	// queues are the hosts this mirror holds a copy queue on.
	queues map[string]bool

	// present are the references the last whole destination pass found, keyed by their String
	// form; lastPass is when that pass ran, zero before the first.
	present  map[string]bool
	lastPass time.Time
	// digests are the digests the destination is known to hold each reference at, from a
	// pass or a copy: what tells a deleted manifest from a deleted tag.
	digests map[string]string
	// recopy are the references the destination lost; writeBack those whose manifest is still
	// there but whose tag of this cluster is gone.
	recopy    map[string]bool
	writeBack map[string]bool

	// policy is the driftPolicy the last reconcile read; rings are the hosts this mirror
	// turns a drift ring on.
	policy kuikv1alpha1.DriftPolicy
	rings  map[string]bool
	// drifted are the copied tags whose upstream digest moved away from the copy; resync
	// those Sync copies again.
	drifted map[string]driftEntry
	resync  map[string]bool
	// declaring is, for every desired reference, the pod whose pull secrets read it.
	declaring map[string]*corev1.Pod

	// live are the references the selected pods ran at the last reconcile, nil before the
	// first one: what tells which reference lost its last pod.
	live []imagepath.Reference
	// deleteUnsupported is set once the destination refused a tag deletion as unsupported,
	// until one succeeds.
	deleteUnsupported bool
	// held counts the tries of the finalizer holding the deleted mirror.
	held uint
}

// refusesDeletion reports whether the destination refused the last tag deletion as
// unsupported.
func (st *mirrorState) refusesDeletion() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.deleteUnsupported
}

// release returns the references of the previous reconcile no selected pod runs any more,
// and remembers live for the next one. The first reconcile of a process releases nothing:
// the sweep finds what fell out of use while it was not running.
func (st *mirrorState) release(live []imagepath.Reference) []imagepath.Reference {
	st.mu.Lock()
	defer st.mu.Unlock()
	previous := st.live
	st.live = live
	running := map[string]bool{}
	for _, ref := range live {
		running[ref.String()] = true
	}
	var released []imagepath.Reference
	for _, ref := range previous {
		if !running[ref.String()] {
			released = append(released, ref)
		}
	}
	return released
}

// driftEntry is a copied tag whose upstream digest moved, as the last check saw it.
type driftEntry struct {
	upstream, copied string
	at               time.Time
}

// copySource is the candidates an owed reference may be read from, in the order the webhook
// would offer them, and the one its next copy window tries.
type copySource struct {
	origin     imagepath.Reference
	candidates []routing.Candidate
	// pod declares the reference: its pull secrets are part of each candidate's credentials.
	pod  *corev1.Pod
	next int
}

// host is the registry the next copy window of s reads.
func (s *copySource) host() string {
	ref, err := imagepath.Parse(s.candidates[s.next].Reference)
	if err != nil {
		return s.origin.Host
	}
	return ref.Host
}

// copyFailure is what the last attempt to copy a reference observed.
type copyFailure struct {
	reason kuikv1alpha1.CopyFailureReason
	// registry is the side that failed: the source host, or the destination host.
	registry string
	at       time.Time
}

func newMirrorState() *mirrorState {
	return &mirrorState{
		copied:    map[string]bool{},
		sources:   map[string]*copySource{},
		failed:    map[string]copyFailure{},
		queues:    map[string]bool{},
		present:   map[string]bool{},
		digests:   map[string]string{},
		recopy:    map[string]bool{},
		writeBack: map[string]bool{},
		rings:     map[string]bool{},
		drifted:   map[string]driftEntry{},
		resync:    map[string]bool{},
		declaring: map[string]*corev1.Pod{},
	}
}

// state returns the memory of the ImageMirror name, created on first use.
func (r *ImageMirrorReconciler) state(name string) *mirrorState {
	r.statesMu.Lock()
	defer r.statesMu.Unlock()
	s, ok := r.states[name]
	if !ok {
		s = newMirrorState()
		r.states[name] = s
	}
	return s
}

// owe hands the scheduler the references im still owes its destination, each in the queue of
// the host its next copy window reads, and drops the queues it no longer needs.
func (r *ImageMirrorReconciler) owe(im *kuikv1alpha1.ImageMirror, st *mirrorState, owed []imagepath.Reference, sources map[string]*copySource) {
	owner := pacing.Owner{Kind: routing.KindImageMirror, Name: im.Name}

	st.mu.Lock()
	queues := map[string][]string{}
	for _, ref := range owed {
		key := ref.String()
		source := sources[key]
		// A reference keeps the candidate it reached as long as its candidates do not change.
		if previous, ok := st.sources[key]; ok && sameCandidates(previous.candidates, source.candidates) {
			source.next = previous.next
		}
		st.sources[key] = source
		queues[source.host()] = append(queues[source.host()], key)
	}
	stillOwed := make(map[string]bool, len(owed))
	for _, ref := range owed {
		stillOwed[ref.String()] = true
	}
	for key := range st.sources {
		if !stillOwed[key] {
			delete(st.sources, key)
		}
	}
	gone := []string{}
	for host := range st.queues {
		if _, ok := queues[host]; !ok {
			gone = append(gone, host)
		}
	}
	st.queues = map[string]bool{}
	for host := range queues {
		st.queues[host] = true
	}
	recopy := maps.Clone(st.recopy)
	st.mu.Unlock()

	for host, refs := range queues {
		// A lost copy is an availability hole pods may be routed to right now; an initial
		// copy is background work.
		slices.SortFunc(refs, func(a, b string) int {
			if recopy[a] != recopy[b] {
				if recopy[a] {
					return -1
				}
				return 1
			}
			return strings.Compare(a, b)
		})
		r.scheduler.SetCopyQueue(owner, host, refs, mirrorCopier{r: r, name: im.Name, host: host})
	}
	for _, host := range gone {
		r.scheduler.SetCopyQueue(owner, host, nil, mirrorCopier{r: r, name: im.Name, host: host})
	}
}

func sameCandidates(a, b []routing.Candidate) bool {
	return slices.EqualFunc(a, b, func(x, y routing.Candidate) bool { return x.Reference == y.Reference })
}

// copySources lists, for every desired reference of im, the candidates it may be read from:
// the origin, then each ImageAlternative entry covering it, ordered as at admission. A
// reference a pod declares is read in the scope of that pod.
func (r *ImageMirrorReconciler) copySources(ctx context.Context, desired []imagepath.Reference, pods []*corev1.Pod) (map[string]*copySource, error) {
	var alternatives kuikv1alpha1.ImageAlternativeList
	if err := r.List(ctx, &alternatives); err != nil {
		return nil, err
	}
	index := routing.NewIndex(alternatives.Items, nil)
	unavailable := unavailableEntries(alternatives.Items)
	declaring := map[string]*corev1.Pod{}
	for _, pod := range pods {
		for _, c := range containersOf(pod) {
			if _, ok := declaring[c]; !ok {
				declaring[c] = pod
			}
		}
	}
	namespaceLabels, err := r.namespaceLabels(ctx)
	if err != nil {
		return nil, err
	}

	opts := routing.Options{ClusterID: r.config.Load().ClusterID}
	sources := make(map[string]*copySource, len(desired))
	for _, ref := range desired {
		req := routing.Request{Image: ref}
		pod := declaring[ref.String()]
		if pod != nil {
			req.PodLabels = pod.Labels
			req.NamespaceLabels = namespaceLabels[pod.Namespace]
		}
		candidates := slices.DeleteFunc(index.Candidates(req, opts).Candidates, func(c routing.Candidate) bool {
			return c.Resource != nil && marked(unavailable, c)
		})
		sources[ref.String()] = &copySource{origin: ref, candidates: candidates, pod: pod}
	}
	return sources, nil
}

// unavailableEntries indexes the entries marked unavailable by the name of their resource. The
// webhook still probes them, last; a copy never reads them. An origin matching one stays a
// candidate, which the candidate list already puts last.
func unavailableEntries(alternatives []kuikv1alpha1.ImageAlternative) *imagepath.Trie[string] {
	trie := imagepath.NewTrie[string]()
	for _, ia := range alternatives {
		for _, entry := range ia.Spec.Alternatives {
			if !entry.Unavailable {
				continue
			}
			if path, kind, err := imagepath.ValidateEntry(entry.Repository, entry.RepositoryGroup); err == nil {
				trie.Insert(path, kind, ia.Name)
			}
		}
	}
	return trie
}

// marked reports whether c is an alternative its own resource marks unavailable.
func marked(unavailable *imagepath.Trie[string], c routing.Candidate) bool {
	ref, err := imagepath.Parse(c.Reference)
	if err != nil {
		return false
	}
	for _, m := range unavailable.Match(ref) {
		if m.Value == c.Resource.Name {
			return true
		}
	}
	return false
}

// namespaceLabels maps every namespace to its labels.
func (r *ImageMirrorReconciler) namespaceLabels(ctx context.Context) (map[string]map[string]string, error) {
	var namespaces corev1.NamespaceList
	if err := r.List(ctx, &namespaces); err != nil {
		return nil, err
	}
	labels := make(map[string]map[string]string, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		labels[ns.Name] = ns.Labels
	}
	return labels, nil
}

// mirrorCopier copies the references one ImageMirror owes, one per window of host.
type mirrorCopier struct {
	r    *ImageMirrorReconciler
	name string
	host string
}

// Copy reads ref from the candidate its window is for and writes it to the destination. A
// candidate that does not answer hands ref to the next one, whose own host's window reads it.
func (c mirrorCopier) Copy(ctx context.Context, ref string) error {
	r := c.r
	log := logf.FromContext(ctx).WithValues("imagemirror", klog.KRef("", c.name), "image", ref)
	st := r.state(c.name)
	st.mu.Lock()
	source, ok := st.sources[ref]
	// A window of a host reads that host only: a reference handed to a candidate elsewhere
	// waits for a window there, once the next reconcile moved it to that queue.
	if !ok || source.host() != c.host || (st.copied[ref] && !st.resync[ref]) {
		st.mu.Unlock()
		return nil
	}
	candidate := source.candidates[source.next]
	st.mu.Unlock()

	var im kuikv1alpha1.ImageMirror
	if err := r.Get(ctx, types.NamespacedName{Name: c.name}, &im); err != nil {
		return client.IgnoreNotFound(err)
	}
	defer r.wake(c.name)

	sourceEndpoint, err := r.sourceEndpoint(ctx, candidate, source.pod)
	if err == nil {
		_, err = r.registry.Check(ctx, sourceEndpoint)
	}
	if err != nil {
		log.V(1).Info("Skipped copy source", "candidate", candidate.Reference, "error", err.Error())
		st.mu.Lock()
		defer st.mu.Unlock()
		source.next++
		if source.next == len(source.candidates) {
			source.next = 0
			st.fail(ref, copyFailure{reason: kuikv1alpha1.CopySourceNotFound, registry: source.origin.Host, at: r.clock.Now()})
		}
		return nil
	}

	destination := destinationRepository(&im, source.origin, r.config.Load().ClusterID)
	// The registry offers no way to list the repositories a client populated: the one that
	// is not recorded before its first push is never swept.
	if err := r.recordRepository(ctx, &im, destination); err != nil {
		return err
	}
	manage, err := r.manageAuth(ctx, &im)
	if err != nil {
		return err
	}
	destinationEndpoint := registry.Endpoint{Reference: destination, Insecure: im.Spec.Destination.Insecure, Auth: manage(destination)}
	digest, err := r.registry.Copy(ctx, sourceEndpoint, destinationEndpoint, mirrorpath.Tags(source.origin, r.config.Load().ClusterID))

	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		reason, failedRef := kuikv1alpha1.CopyDestinationUnreachable, destination
		if copyErr, ok := errors.AsType[*registry.CopyError](err); ok {
			reason = copyErr.Reason
			if !copyErr.Destination {
				failedRef = sourceEndpoint.Reference
			}
		}
		host, _, _ := strings.Cut(failedRef, "/")
		st.fail(ref, copyFailure{reason: reason, registry: host, at: r.clock.Now()})
		log.V(1).Info("Failed to copy image", "reason", reason, "error", err.Error())
		return nil
	}
	delete(st.failed, ref)
	st.copied[ref] = true
	st.digests[ref] = digest.String()
	source.next = 0
	switch {
	case st.writeBack[ref]:
		// The manifest was there: only this cluster's tag was written back, nothing was lost.
	case st.recopy[ref]:
		r.recorder.Eventf(&im, nil, corev1.EventTypeWarning, "ImageRecopied", "Copy",
			"Copied %s to %s again: something outside kuik deleted it from the destination", ref, destination)
	case st.resync[ref]:
		r.recorder.Eventf(&im, nil, corev1.EventTypeNormal, "ImageResynced", "Copy",
			"Moved the tag of %s at %s onto the upstream's new digest %s", ref, destination, digest)
	default:
		r.recorder.Eventf(&im, nil, corev1.EventTypeNormal, "ImageCopied", "Copy", "Copied %s to %s", ref, destination)
	}
	delete(st.recopy, ref)
	delete(st.writeBack, ref)
	delete(st.resync, ref)
	delete(st.drifted, ref)
	return nil
}

// fail records failure for ref, reported at the next status write. st.mu is held.
func (st *mirrorState) fail(ref string, failure copyFailure) {
	st.failed[ref] = failure
	st.fresh = append(st.fresh, kuikv1alpha1.FailedImageCopy{Ref: ref, Reason: failure.reason})
}

// sourceEndpoint is candidate with the credentials a node would pull it with, then
// fallbackAuth: its entry's auth, the pull secrets of pod, the most specific fallbackAuth
// entry, then anonymous.
func (r *ImageMirrorReconciler) sourceEndpoint(ctx context.Context, candidate routing.Candidate, pod *corev1.Pod) (registry.Endpoint, error) {
	ref, err := imagepath.Parse(candidate.Reference)
	if err != nil {
		return registry.Endpoint{}, err
	}
	creds, err := r.sourceResolver.Resolve(ctx, ref, candidate.Config.Auth, pod)
	if err != nil {
		return registry.Endpoint{}, err
	}
	return registry.Endpoint{Reference: candidate.Reference, Insecure: candidate.Config.Insecure, Auth: authenticators(candidate.Reference, creds)}, nil
}

// manageAuth reads the credential im writes its destination with, and returns the
// authenticators it offers for a destination reference: none when im has no credential.
func (r *ImageMirrorReconciler) manageAuth(ctx context.Context, im *kuikv1alpha1.ImageMirror) (func(ref string) []authn.Authenticator, error) {
	if im.Spec.Destination.Manage == nil {
		return func(string) []authn.Authenticator { return nil }, nil
	}
	creds, err := r.resolver.Resolve(ctx, imagepath.Reference{}, &im.Spec.Destination.Manage.Auth, nil)
	if err != nil {
		return nil, err
	}
	// Looked up per reference, as the kubelet does for the image it pulls: destination.path
	// alone, with its trailing slash, is no reference the keyring can match.
	return func(ref string) []authn.Authenticator { return authenticators(ref, creds) }, nil
}

// authenticators turns resolved credentials into the authenticators to try for image, in
// order. A pending provider or a malformed Secret yields nothing.
func authenticators(image string, creds []auth.Credential) []authn.Authenticator {
	var auths []authn.Authenticator
	for _, cred := range creds {
		if cred.Pending || len(cred.Secrets) == 0 {
			continue
		}
		found, err := keychain.Authenticators(image, cred.Secrets)
		if err != nil {
			continue
		}
		auths = append(auths, found...)
	}
	return auths
}

// recordRepository adds repository to the status.repositories of im, unless listed already.
// The reconcile writes the same status, so a conflict reads the mirror again and retries.
func (r *ImageMirrorReconciler) recordRepository(ctx context.Context, im *kuikv1alpha1.ImageMirror, repository string) error {
	// Listed already in the mirror the copy read: the common case costs no read.
	if slices.Contains(im.Status.Repositories, repository) {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(im), im); err != nil {
			return err
		}
		if slices.Contains(im.Status.Repositories, repository) {
			return nil
		}
		patch := client.MergeFromWithOptions(im.DeepCopy(), client.MergeFromWithOptimisticLock{})
		im.Status.Repositories = append(im.Status.Repositories, repository)
		slices.Sort(im.Status.Repositories)
		return r.Status().Patch(ctx, im, patch)
	})
}

// containersOf returns the origins of the routed containers of pod, normalised.
func containersOf(pod *corev1.Pod) []string {
	var origins []string
	for _, c := range attribution.Containers(pod) {
		if ref, err := imagepath.Parse(c.Origin); err == nil {
			origins = append(origins, ref.String())
		}
	}
	return origins
}

// liveImages returns the live images of the routed containers of pods, normalised.
func liveImages(pods []*corev1.Pod) []string {
	var images []string
	for _, pod := range pods {
		for _, c := range attribution.Containers(pod) {
			images = append(images, c.Image)
		}
	}
	return images
}

// owed are the references of desired the destination is not known to hold yet. A reference
// the last pass found counts as held only in a repository status.repositories lists: an
// unlisted one would never be swept, so it is copied again, which records it. A tag Sync
// resyncs is owed again whatever the destination holds.
func (st *mirrorState) owed(desired []imagepath.Reference, inventoried func(imagepath.Reference) bool) []imagepath.Reference {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(desired), func(ref imagepath.Reference) bool {
		key := ref.String()
		return !st.resync[key] && (st.copied[key] || (st.present[key] && inventoried(ref)))
	})
}

// verdicts are snapshots of the references the last pass found and of those copied since.
func (st *mirrorState) verdicts() (present, copied map[string]bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return maps.Clone(st.present), maps.Clone(st.copied)
}

// reportFailures returns the failedImageCopies of im: one entry per desired reference whose
// copy is failing, since kept from the previous status. It emits the events of the failures
// observed since the last report: ImageCopyFailed coalesced per reason, and ImageUnrecoverable
// for a reference no source can supply any more.
func (r *ImageMirrorReconciler) reportFailures(im *kuikv1alpha1.ImageMirror, st *mirrorState, desired []imagepath.Reference) []kuikv1alpha1.FailedImageCopy {
	st.mu.Lock()
	defer st.mu.Unlock()

	previous := map[string]kuikv1alpha1.FailedImageCopy{}
	for _, e := range im.Status.FailedImageCopies {
		previous[e.Ref] = e
	}
	wanted := map[string]bool{}
	for _, ref := range desired {
		wanted[ref.String()] = true
	}

	var entries []kuikv1alpha1.FailedImageCopy
	for ref, failure := range st.failed {
		if !wanted[ref] {
			delete(st.failed, ref)
			continue
		}
		at := metav1.NewTime(failure.at)
		entry := kuikv1alpha1.FailedImageCopy{Ref: ref, Reason: failure.reason, Since: at, LastAttempt: &at}
		if p, ok := previous[ref]; ok {
			entry.Since = p.Since
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b kuikv1alpha1.FailedImageCopy) int {
		return cmp.Or(a.Since.Compare(b.Since.Time), cmp.Compare(a.Ref, b.Ref))
	})

	// A reference failing on several windows since the last report counts once, with its
	// last reason.
	fresh := map[string]kuikv1alpha1.FailedImageCopy{}
	for _, f := range st.fresh {
		fresh[f.Ref] = f
	}
	st.fresh = nil
	coalesced := slices.SortedFunc(maps.Values(fresh), func(a, b kuikv1alpha1.FailedImageCopy) int { return cmp.Compare(a.Ref, b.Ref) })
	for _, event := range plan.CopyFailureEvents(coalesced) {
		r.recorder.Eventf(im, nil, corev1.EventTypeWarning, "ImageCopyFailed", "Copy", "%s", event.Message)
	}
	for _, f := range coalesced {
		if f.Reason == kuikv1alpha1.CopySourceNotFound && previous[f.Ref].Reason != kuikv1alpha1.CopySourceNotFound {
			r.recorder.Eventf(im, nil, corev1.EventTypeWarning, "ImageUnrecoverable", "Copy",
				"No source can supply %s any more; pods still running it do so from a node cache", f.Ref)
		}
	}
	return entries
}
