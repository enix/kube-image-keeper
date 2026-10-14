// Package routing builds the ordered candidate list of a container image from the
// ImageAlternative and ImageMirror resources that apply to its pod. See docs/v3/spec.md,
// "Candidate ordering".
package routing

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
)

// The kinds of the routing resources, as `by` and the metric labels name them.
const (
	KindImageAlternative = "ImageAlternative"
	KindImageMirror      = "ImageMirror"
)

// KindImageMonitor names the resource that reports an original unavailable. It offers no
// candidate, so it is never a `by`.
const KindImageMonitor = "ImageMonitor"

// Resource identifies a routing resource.
type Resource struct {
	Kind string
	Name string
}

// String renders the resource as `<kind>/<name>`.
func (r Resource) String() string {
	return r.Kind + "/" + r.Name
}

// ParseResource reads back the `<kind>/<name>` form of String. It rejects a kind other than
// ImageAlternative or ImageMirror and a missing name.
func ParseResource(s string) (Resource, error) {
	kind, name, ok := strings.Cut(s, "/")
	if !ok || name == "" || (kind != KindImageAlternative && kind != KindImageMirror) {
		return Resource{}, fmt.Errorf("%q is not a routing resource as <kind>/<name>", s)
	}
	return Resource{Kind: kind, Name: name}, nil
}

// Config is what a candidate is probed and pulled with.
type Config struct {
	// Auth is the entry's `auth`, or the mirror's `destination.pull.auth`. Nil for none.
	Auth *kuikv1alpha1.Auth
	// Insecure reaches the registry over plain HTTP.
	Insecure bool
}

// Candidate is one reference a container may be routed to.
type Candidate struct {
	// Reference is the image reference to place in the container.
	Reference string
	// Config is what the reference is probed and pulled with.
	Config Config
	// Resource is the resource that offered it, nil for the original.
	Resource *Resource
	// Policy is the policy of the band the candidate sits in, empty for the original.
	Policy kuikv1alpha1.RewritePolicy
}

// Request is the container a candidate list is built for.
type Request struct {
	// Image is the container's normalised reference.
	Image imagepath.Reference
	// PullPolicy is the container's imagePullPolicy.
	PullPolicy corev1.PullPolicy
	// PodLabels are the labels of the pod.
	PodLabels map[string]string
	// NamespaceLabels are the labels of the pod's namespace.
	NamespaceLabels map[string]string
}

// Options are the global config settings the candidate list depends on.
type Options struct {
	// ClusterID suffixes the tag of a mirror candidate.
	ClusterID string
	// DemoteMirrorWithPullPolicyAlways ignores an ImageMirror's `rewritePolicy: Always` for a
	// container with `imagePullPolicy: Always`.
	DemoteMirrorWithPullPolicyAlways bool
	// DemoteKnownFailures sends the candidates the loops report failing to the end of the
	// list.
	DemoteKnownFailures bool
}

// Result is the candidate list of a container.
type Result struct {
	// Candidates are the references to probe, in order, the original included.
	Candidates []Candidate
	// Offering are the resources that offered at least one candidate.
	Offering []Resource
	// Demoted are the candidates sent to the end of the list as known failures, in order.
	Demoted []Demotion
}

// Demotion is a candidate a status lists failing, sent to the end of the list.
type Demotion struct {
	// Candidate is the reference of the demoted candidate.
	Candidate string
	// ReportedBy is the resource whose status lists it failing.
	ReportedBy Resource
}

// Index holds the routing resources and answers candidate lists from them. It is built
// whole and never modified: a change of the resources builds a new one.
type Index struct {
	alternatives []alternativeResource
	mirrors      []mirrorResource
	// entries holds every valid entry of alternatives, keyed by its resource and position.
	entries *imagepath.Trie[entryRef]
	// unavailable holds the origins the ImageMonitors list in status.unavailableImages, in
	// canonical form, each with the names of the monitors listing it, sorted.
	unavailable map[string][]string
	// unavailableAlternatives holds the alternatives the ImageMonitors list in
	// status.unavailableAlternatives, keyed by the name of the ImageAlternative their `via`
	// names, then by reference in canonical form, each with the names of the monitors listing
	// it, sorted.
	unavailableAlternatives map[string]map[string][]string
	// failedCopies holds the origins each ImageMirror lists in status.failedImageCopies, in
	// canonical form, keyed by mirror name.
	failedCopies map[string]map[string]bool
}

// scope is where a resource applies: its selectors, nil when one does not parse, in which
// case the resource applies nowhere.
type scope struct {
	pods       labels.Selector
	namespaces labels.Selector
}

func newScope(pods, namespaces *metav1.LabelSelector) scope {
	p, errPods := selector(pods)
	n, errNamespaces := selector(namespaces)
	if errPods != nil || errNamespaces != nil {
		return scope{}
	}
	return scope{pods: p, namespaces: n}
}

// selector parses a label selector, an absent one matching everything.
func selector(s *metav1.LabelSelector) (labels.Selector, error) {
	if s == nil {
		return labels.Everything(), nil
	}
	return metav1.LabelSelectorAsSelector(s)
}

func (s scope) applies(req Request) bool {
	return s.pods != nil &&
		s.pods.Matches(labels.Set(req.PodLabels)) &&
		s.namespaces.Matches(labels.Set(req.NamespaceLabels))
}

type alternativeResource struct {
	name    string
	policy  kuikv1alpha1.RewritePolicy
	scope   scope
	entries []entry
}

type entry struct {
	spec kuikv1alpha1.Alternative
	// path is the parsed `repository` or `repositoryGroup`; valid is false when it does not
	// parse, and the entry then neither matches nor produces a candidate.
	path  imagepath.Path
	valid bool
}

type entryRef struct {
	alternative int
	entry       int
}

type mirrorResource struct {
	name  string
	spec  kuikv1alpha1.ImageMirrorSpec
	scope scope
}

// NewIndex builds an index over the given resources: the spec of the routing resources, and
// the status lists of the ImageMirrors and the ImageMonitors that report known failures.
func NewIndex(alternatives []kuikv1alpha1.ImageAlternative, mirrors []kuikv1alpha1.ImageMirror, monitors ...kuikv1alpha1.ImageMonitor) *Index {
	index := &Index{
		entries:                 imagepath.NewTrie[entryRef](),
		unavailable:             map[string][]string{},
		unavailableAlternatives: map[string]map[string][]string{},
		failedCopies:            map[string]map[string]bool{},
	}

	alternatives = slices.SortedFunc(slices.Values(alternatives), func(a, b kuikv1alpha1.ImageAlternative) int {
		return cmp.Compare(a.Name, b.Name)
	})
	for i, cr := range alternatives {
		alt := alternativeResource{
			name:   cr.Name,
			policy: policyOrDefault(cr.Spec.RewritePolicy),
			scope:  newScope(cr.Spec.PodSelector, cr.Spec.NamespaceSelector),
		}
		for j, spec := range cr.Spec.Alternatives {
			path, kind, err := imagepath.ValidateEntry(spec.Repository, spec.RepositoryGroup)
			alt.entries = append(alt.entries, entry{spec: spec, path: path, valid: err == nil})
			if err == nil {
				index.entries.Insert(path, kind, entryRef{alternative: i, entry: j})
			}
		}
		index.alternatives = append(index.alternatives, alt)
	}

	mirrors = slices.SortedFunc(slices.Values(mirrors), func(a, b kuikv1alpha1.ImageMirror) int {
		return cmp.Compare(a.Name, b.Name)
	})
	for _, cr := range mirrors {
		// A mirror being deleted waits for the pods running its copies to go before its tags
		// are deleted: routing a new pod to it would keep it waiting.
		if cr.DeletionTimestamp != nil {
			continue
		}
		index.mirrors = append(index.mirrors, mirrorResource{
			name:  cr.Name,
			spec:  cr.Spec,
			scope: newScope(cr.Spec.PodSelector, cr.Spec.NamespaceSelector),
		})
		// A failed copy of a tag listed in driftedImages is a failed resync: the destination
		// still serves the previous copy, so the mirror candidate is no known failure.
		drifted := map[string]bool{}
		for _, e := range cr.Status.DriftedImages {
			if ref, ok := canonical(e.Ref); ok {
				drifted[ref] = true
			}
		}
		failed := map[string]bool{}
		for _, e := range cr.Status.FailedImageCopies {
			if ref, ok := canonical(e.Ref); ok && !drifted[ref] {
				failed[ref] = true
			}
		}
		index.failedCopies[cr.Name] = failed
	}

	monitors = slices.SortedFunc(slices.Values(monitors), func(a, b kuikv1alpha1.ImageMonitor) int {
		return cmp.Compare(a.Name, b.Name)
	})
	for _, cr := range monitors {
		for _, e := range cr.Status.UnavailableImages {
			if ref, ok := canonical(e.Ref); ok && !slices.Contains(index.unavailable[ref], cr.Name) {
				index.unavailable[ref] = append(index.unavailable[ref], cr.Name)
			}
		}
		for _, e := range cr.Status.UnavailableAlternatives {
			name, isAlternative := strings.CutPrefix(e.Via, KindImageAlternative+"/")
			ref, ok := canonical(e.Ref)
			if !isAlternative || name == "" || !ok {
				continue
			}
			byRef := index.unavailableAlternatives[name]
			if byRef == nil {
				byRef = map[string][]string{}
				index.unavailableAlternatives[name] = byRef
			}
			if !slices.Contains(byRef[ref], cr.Name) {
				byRef[ref] = append(byRef[ref], cr.Name)
			}
		}
	}
	return index
}

// canonical is the canonical form of a reference a status lists, false when it does not
// parse.
func canonical(ref string) (string, bool) {
	parsed, err := imagepath.Parse(ref)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

// policyOrDefault is policy, or OnFailure when unset.
func policyOrDefault(policy kuikv1alpha1.RewritePolicy) kuikv1alpha1.RewritePolicy {
	if policy == "" {
		return kuikv1alpha1.RewritePolicyOnFailure
	}
	return policy
}

// bands holds the candidates of each band, in the order docs/v3/spec.md, "Candidate
// ordering", merges them.
type bands struct {
	alwaysMirror, alwaysAlternative, onFailureAlternative, onFailureMirror []Candidate
	// demoteOriginal sends the original to the end: it matched an entry marked unavailable.
	demoteOriginal bool
}

// Candidates returns the candidate list of a container.
func (i *Index) Candidates(req Request, opts Options) Result {
	var b bands
	i.alternativeCandidates(req, &b)
	i.mirrorCandidates(req, opts, &b)

	original := Candidate{Reference: req.Image.String()}
	list := slices.Concat(b.alwaysMirror, b.alwaysAlternative)
	if !b.demoteOriginal {
		list = append(list, original)
	}
	list = append(list, b.onFailureAlternative...)
	list = append(list, b.onFailureMirror...)
	if b.demoteOriginal {
		list = append(list, original)
	}
	// A known failure is decided before deduplication: the original deduplicated away behind
	// an alternative entry producing its reference is still known to fail.
	known := func(Candidate) (Resource, bool) { return Resource{}, false }
	if opts.DemoteKnownFailures {
		origin := req.Image.String()
		known = func(c Candidate) (Resource, bool) { return i.knownFailure(origin, c) }
	}
	candidates, reporters := deduplicate(list, known)
	result := Result{Offering: offering(list)}
	result.Candidates, result.Demoted = demoteKnownFailures(candidates, reporters)
	return result
}

// demoteKnownFailures moves the candidates of list a status lists failing, those with a
// reporter, to the end, both parts keeping their order. Nothing is dropped. A demoted
// candidate of an Always band is served only once the original failed, a fallback: it takes
// the OnFailure policy, as an Always mirror demoted for imagePullPolicy Always does. See
// docs/v3/spec.md, "demoteKnownFailures: reusing what the loops already know".
func demoteKnownFailures(list []Candidate, reporters []*Resource) ([]Candidate, []Demotion) {
	kept := make([]Candidate, 0, len(list))
	var moved []Candidate
	var demoted []Demotion
	for k, c := range list {
		if reporters[k] == nil {
			kept = append(kept, c)
			continue
		}
		if c.Policy == kuikv1alpha1.RewritePolicyAlways {
			c.Policy = kuikv1alpha1.RewritePolicyOnFailure
		}
		moved = append(moved, c)
		demoted = append(demoted, Demotion{Candidate: c.Reference, ReportedBy: *reporters[k]})
	}
	return append(kept, moved...), demoted
}

// knownFailure tells whether a status lists the candidate c of origin failing, and the
// resource that does, the first by name: the original when an ImageMonitor lists it in
// unavailableImages, a mirror candidate when its own ImageMirror lists the origin in
// failedImageCopies, an alternative candidate when an ImageMonitor lists its reference in
// unavailableAlternatives via its own ImageAlternative, whatever origin it was derived from.
// unavailableImages lists origins: a reference it lists demotes no alternative candidate.
func (i *Index) knownFailure(origin string, c Candidate) (Resource, bool) {
	switch {
	case c.Resource == nil:
		if monitors := i.unavailable[origin]; len(monitors) > 0 {
			return Resource{Kind: KindImageMonitor, Name: monitors[0]}, true
		}
	case c.Resource.Kind == KindImageAlternative:
		// A rewrite may read short, docker.io/nginx for a docker.io group: the status lists
		// it in canonical form once read back.
		ref, ok := canonical(c.Reference)
		if !ok {
			break
		}
		if monitors := i.unavailableAlternatives[c.Resource.Name][ref]; len(monitors) > 0 {
			return Resource{Kind: KindImageMonitor, Name: monitors[0]}, true
		}
	case c.Resource.Kind == KindImageMirror:
		if i.failedCopies[c.Resource.Name][origin] {
			return *c.Resource, true
		}
	}
	return Resource{}, false
}

// alternativeCandidates adds the entries of every ImageAlternative matching the image, the
// most specific entry of each deciding the remainder, the other entries producing
// candidates in declared order. Any matching entry marked unavailable demotes the original,
// whether or not it is the most specific.
func (i *Index) alternativeCandidates(req Request, b *bands) {
	all := i.entries.Match(req.Image)
	for _, m := range all {
		alt := i.alternatives[m.Value.alternative]
		if alt.entries[m.Value.entry].spec.Unavailable && alt.scope.applies(req) {
			b.demoteOriginal = true
		}
	}
	matches := imagepath.MostSpecificPerOwner(all, func(r entryRef) int { return r.alternative })
	// Matches come most specific first; bands are sorted by name, the order of alternatives.
	slices.SortFunc(matches, func(x, y imagepath.Match[entryRef]) int {
		return cmp.Compare(x.Value.alternative, y.Value.alternative)
	})
	for _, m := range matches {
		alt := i.alternatives[m.Value.alternative]
		if !alt.scope.applies(req) {
			continue
		}
		resource := &Resource{Kind: KindImageAlternative, Name: alt.name}
		for j, e := range alt.entries {
			if j == m.Value.entry || !e.valid || e.spec.Unavailable {
				continue
			}
			c := Candidate{
				Reference: imagepath.Rewrite(req.Image, m, e.path).String(),
				Config:    Config{Auth: e.spec.Auth, Insecure: e.spec.Insecure},
				Resource:  resource,
				Policy:    alt.policy,
			}
			if alt.policy == kuikv1alpha1.RewritePolicyAlways {
				b.alwaysAlternative = append(b.alwaysAlternative, c)
			} else {
				b.onFailureAlternative = append(b.onFailureAlternative, c)
			}
		}
	}
}

// mirrorCandidates adds the destination reference of every ImageMirror applying to the pod,
// but under rewritePolicy None, for an excluded image or one under its own destination.
func (i *Index) mirrorCandidates(req Request, opts Options, b *bands) {
	demote := opts.DemoteMirrorWithPullPolicyAlways && req.PullPolicy == corev1.PullAlways
	for _, m := range i.mirrors {
		policy := policyOrDefault(m.spec.RewritePolicy)
		if policy == kuikv1alpha1.RewritePolicyNone || !m.scope.applies(req) ||
			mirrorpath.Excluded(m.spec.ExcludeImages, req.Image) ||
			mirrorpath.UnderDestination(m.spec.Destination.Path, req.Image) {
			continue
		}
		c := Candidate{
			Reference: mirrorpath.Destination(m.spec.Destination.Path, req.Image, opts.ClusterID),
			Config:    Config{Insecure: m.spec.Destination.Insecure},
			Resource:  &Resource{Kind: KindImageMirror, Name: m.name},
			Policy:    kuikv1alpha1.RewritePolicyOnFailure,
		}
		if m.spec.Destination.Pull != nil {
			c.Config.Auth = &m.spec.Destination.Pull.Auth
		}
		if policy == kuikv1alpha1.RewritePolicyAlways && !demote {
			c.Policy = kuikv1alpha1.RewritePolicyAlways
			b.alwaysMirror = append(b.alwaysMirror, c)
		} else {
			b.onFailureMirror = append(b.onFailureMirror, c)
		}
	}
}

// deduplicate keeps the first occurrence of each (reference, config), and returns along the
// resource known reports failing for each kept candidate, nil for none. A kept candidate is
// judged on its own occurrence, and also carries the failure of an original deduplicated
// behind it: the original is known to fail whoever offers its reference, while a mirror or
// an alternative candidate is only through the resource that offered it.
func deduplicate(list []Candidate, known func(Candidate) (Resource, bool)) ([]Candidate, []*Resource) {
	seen := map[string]int{}
	kept := make([]Candidate, 0, len(list))
	reporters := make([]*Resource, 0, len(list))
	for _, c := range list {
		// Config holds plain values only, so its JSON form cannot fail and identifies it.
		config, _ := json.Marshal(c.Config)
		key := c.Reference + "\x00" + string(config)
		var reporter *Resource
		if by, failing := known(c); failing {
			reporter = &by
		}
		if k, ok := seen[key]; ok {
			if reporters[k] == nil && c.Resource == nil {
				reporters[k] = reporter
			}
			continue
		}
		seen[key] = len(kept)
		kept = append(kept, c)
		reporters = append(reporters, reporter)
	}
	return kept, reporters
}

// offering lists the resources of list, in order of first appearance.
func offering(list []Candidate) []Resource {
	var resources []Resource
	for _, c := range list {
		if c.Resource != nil && !slices.Contains(resources, *c.Resource) {
			resources = append(resources, *c.Resource)
		}
	}
	return resources
}
