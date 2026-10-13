package kuik

import (
	"cmp"
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
	"github.com/enix/kube-image-keeper/internal/status/imagemetrics"
)

// alternativeOffers answers the alternatives the ImageAlternatives offer for an origin, as
// the webhook would propose them. See docs/v3/spec.md, "ImageMonitor".
type alternativeOffers struct {
	// scoped applies the selectors of each ImageAlternative, for an origin a pod declares.
	scoped *routing.Index
	// unscoped ignores them, for a retained origin, which no pod declares any more.
	unscoped *routing.Index
	// namespaces are the labels of every namespace.
	namespaces map[string]map[string]string
}

// alternativeOffers reads the ImageAlternatives and the namespaces the alternatives of a
// report are matched against. An ImageMirror is never read: its destination is verified by the
// mirror's own self-check.
func (r *ImageMonitorReconciler) alternativeOffers(ctx context.Context) (*alternativeOffers, error) {
	var alternatives kuikv1alpha1.ImageAlternativeList
	if err := r.List(ctx, &alternatives); err != nil {
		return nil, err
	}
	var namespaces corev1.NamespaceList
	if err := r.List(ctx, &namespaces); err != nil {
		return nil, err
	}
	namespaceLabels := make(map[string]map[string]string, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		namespaceLabels[ns.Name] = ns.Labels
	}
	unscoped := make([]kuikv1alpha1.ImageAlternative, 0, len(alternatives.Items))
	for _, ia := range alternatives.Items {
		ia := *ia.DeepCopy()
		// A selector that does not parse keeps the resource out everywhere, as it does at
		// admission.
		_, errPods := selector(ia.Spec.PodSelector)
		_, errNamespaces := selector(ia.Spec.NamespaceSelector)
		if errPods == nil && errNamespaces == nil {
			ia.Spec.PodSelector, ia.Spec.NamespaceSelector = nil, nil
		}
		unscoped = append(unscoped, ia)
	}
	return &alternativeOffers{
		scoped:     routing.NewIndex(alternatives.Items, nil),
		unscoped:   routing.NewIndex(unscoped, nil),
		namespaces: namespaceLabels,
	}, nil
}

// monitoredAlternative is one alternative a monitor tracks.
type monitoredAlternative struct {
	// origins are the origins it stands for, each with the candidate that offers it first.
	origins map[string]alternativeSource
	// live is set when an origin a pod declares offers it, unset when only retained ones do.
	live bool
	// running is set when a container carries it in place of one of its origins.
	running bool
}

// alternativeSource is the candidate that offers an alternative for one origin.
type alternativeSource struct {
	// via is the name of the ImageAlternative of the candidate.
	via string
	// always is set when the candidate sits in the Always band.
	always bool
	// config is what the candidate is checked with.
	config routing.Config
}

// before reports whether s comes before other in candidate order: the Always band first, then
// by name within a band. See docs/v3/spec.md, "Candidate ordering".
func (s alternativeSource) before(other alternativeSource) bool {
	if s.always != other.always {
		return s.always
	}
	return cmp.Less(s.via, other.via)
}

// offered are the alternatives each origin was offered, by origin then by reference.
type offered map[string]map[string]alternativeSource

// of returns, by reference, the alternatives of the origins the containers of pods declare and
// of the retained ones, and what each of those origins was offered. A container the webhook
// never rewrites, under imagePullPolicy Never or in a static pod, is offered none. A retained
// origin keeps what it was offered at the last report a pod declared it, in previous, and is
// retained with them; one previous never saw, after a restart, is matched against every
// ImageAlternative, whatever its selectors. See docs/v3/spec.md, "What the webhook never
// rewrites".
func (o *alternativeOffers) of(pods []*corev1.Pod, retained []kuikv1alpha1.RetainedImage, previous offered) (map[string]*monitoredAlternative, offered) {
	seen := offered{}
	offer := func(origin, ref string, source alternativeSource) {
		if seen[origin] == nil {
			seen[origin] = map[string]alternativeSource{}
		}
		if current, ok := seen[origin][ref]; !ok || source.before(current) {
			seen[origin][ref] = source
		}
	}
	candidates := func(origin string, list []routing.Candidate) {
		for _, c := range list {
			// The original, a mirror destination, or an entry naming the origin's own
			// repository: none is an alternative of it.
			if c.Resource == nil || c.Resource.Kind != routing.KindImageAlternative || c.Reference == origin {
				continue
			}
			offer(origin, c.Reference, alternativeSource{
				via: c.Resource.Name, always: c.Policy == kuikv1alpha1.RewritePolicyAlways, config: c.Config,
			})
		}
	}

	// lists caches the candidate lists of one call: the containers of a workload share their
	// origin, namespace and pod labels, as the webhook groups them.
	lists := map[string][]routing.Candidate{}
	// carried are the references containers carry in place of their origin, by origin.
	carried := map[string]map[string]bool{}
	for _, pod := range pods {
		static := attribution.Static(pod)
		policies := map[string]corev1.PullPolicy{}
		for _, c := range slices.Concat(pod.Spec.InitContainers, pod.Spec.Containers) {
			policies[c.Name] = c.ImagePullPolicy
		}
		podLabels := labels.Set(pod.Labels).String()
		for _, c := range attribution.Containers(pod) {
			if _, ok := seen[c.Origin]; !ok {
				seen[c.Origin] = map[string]alternativeSource{}
			}
			origin, err := imagepath.Parse(c.Origin)
			if err != nil || static || policies[c.Name] == corev1.PullNever {
				continue
			}
			key := c.Origin + "\x00" + pod.Namespace + "\x00" + podLabels
			list, ok := lists[key]
			if !ok {
				req := routing.Request{Image: origin, PodLabels: pod.Labels, NamespaceLabels: o.namespaces[pod.Namespace]}
				list = o.scoped.Candidates(req, routing.Options{}).Candidates
				lists[key] = list
			}
			candidates(c.Origin, list)
			if c.State == attribution.Rewritten {
				if carried[c.Origin] == nil {
					carried[c.Origin] = map[string]bool{}
				}
				carried[c.Origin][c.Image] = true
			}
		}
	}
	live := make(map[string]bool, len(seen))
	for origin := range seen {
		live[origin] = true
	}
	for _, e := range retained {
		seen[e.Ref] = map[string]alternativeSource{}
		origin, err := imagepath.Parse(e.Ref)
		if err != nil {
			continue
		}
		list := o.unscoped.Candidates(routing.Request{Image: origin}, routing.Options{}).Candidates
		if sources, ok := previous[e.Ref]; ok {
			// Only what its pods were offered, as the ImageAlternatives offer it now: an entry
			// marked unavailable or removed since, or a deleted ImageAlternative, stops it.
			list = slices.DeleteFunc(list, func(c routing.Candidate) bool {
				source, was := sources[c.Reference]
				return c.Resource == nil || !was || source.via != c.Resource.Name
			})
		}
		candidates(e.Ref, list)
	}

	alternatives := map[string]*monitoredAlternative{}
	for origin, sources := range seen {
		for ref, source := range sources {
			a, ok := alternatives[ref]
			if !ok {
				a = &monitoredAlternative{origins: map[string]alternativeSource{}}
				alternatives[ref] = a
			}
			a.origins[origin] = source
			a.live = a.live || live[origin]
			a.running = a.running || carried[origin][ref]
		}
	}
	return alternatives, seen
}

// alternativeCounts partitions the alternatives by what the cluster does with them: running
// where a container carries one in place of its origin, standby where an origin a pod declares
// offers it otherwise, retained where only retained origins do. See docs/v3/status.md,
// "ImageMonitor".
func alternativeCounts(alternatives map[string]*monitoredAlternative) imagemetrics.Counts {
	var counts imagemetrics.Counts
	for _, a := range alternatives {
		switch {
		case a.running:
			counts.Running++
		case a.live:
			counts.Standby++
		default:
			counts.Retained++
		}
	}
	return counts
}

// alternativesStatus is the images.alternatives block of counts.
func alternativesStatus(counts imagemetrics.Counts) *kuikv1alpha1.ImageCounts {
	return &kuikv1alpha1.ImageCounts{
		Tracked:     counts.Running + counts.Standby + counts.Retained,
		Running:     counts.Running,
		Standby:     counts.Standby,
		Retained:    counts.Retained,
		Available:   counts.Available,
		Unavailable: counts.Unavailable,
	}
}

// watchingAlternatives maps an ImageAlternative to the ImageMonitors that monitor alternatives:
// an edit changes what they track, whatever their pods do.
func (r *ImageMonitorReconciler) watchingAlternatives(ctx context.Context, _ client.Object) []reconcile.Request {
	var list kuikv1alpha1.ImageMonitorList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, im := range list.Items {
		if im.Spec.MonitorsAlternatives() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: im.Name}})
		}
	}
	return requests
}
