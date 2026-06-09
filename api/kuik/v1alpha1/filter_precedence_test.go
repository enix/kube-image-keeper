package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"
)

// These tests cover the accessor precedence wiring after the legacy podFilter /
// namespaceFilter fields were removed: image selection prefers spec.filter and
// falls back to the deprecated imageFilter; pod/namespace selection comes from
// spec.filter, matching every pod when filter is unset.

func TestImageSetMirrorImageFilterPrecedence(t *testing.T) {
	g := NewWithT(t)

	// filter set: its image dimension wins, the deprecated imageFilter is ignored.
	ism := &ImageSetMirror{Spec: ImageSetMirrorSpec{
		ImageSetMirrorBase: ImageSetMirrorBase{ImageFilter: ImageFilterDefinition{Include: []string{"legacy-only"}}},
		Filter:             Filter{Include: []FilterItem{{Image: "nginx"}}},
	}}
	imgFilter, err := ism.ImageFilter()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(imgFilter.Match("nginx")).To(BeTrue())
	g.Expect(imgFilter.Match("legacy-only")).To(BeFalse(), "deprecated imageFilter must be ignored when filter is set")

	// filter unset: imageFilter drives selection.
	ism = &ImageSetMirror{Spec: ImageSetMirrorSpec{ImageSetMirrorBase: ImageSetMirrorBase{
		ImageFilter: ImageFilterDefinition{Include: []string{"legacy-only"}},
	}}}
	imgFilter, err = ism.ImageFilter()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(imgFilter.Match("legacy-only")).To(BeTrue())
	g.Expect(imgFilter.Match("nginx")).To(BeFalse())
}

func TestImageSetMirrorPodMatcher(t *testing.T) {
	g := NewWithT(t)

	// filter unset: every pod matches.
	ism := &ImageSetMirror{}
	match, err := ism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("any", nil, nil))).To(BeTrue())

	// filter set: only matching pods.
	ism = &ImageSetMirror{Spec: ImageSetMirrorSpec{Filter: Filter{Include: []FilterItem{{Label: "app=foo"}}}}}
	match, err = ism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("any", map[string]string{"app": "foo"}, nil))).To(BeTrue())
	g.Expect(match(pod("any", map[string]string{"app": "bar"}, nil))).To(BeFalse())
}

func TestClusterImageSetMirrorPodMatcherNamespace(t *testing.T) {
	g := NewWithT(t)

	// filter unset: every pod matches (every namespace).
	cism := &ClusterImageSetMirror{}
	match, err := cism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("whatever", nil, nil))).To(BeTrue())

	// filter set with a namespace dimension: only that namespace matches.
	cism = &ClusterImageSetMirror{Spec: ClusterImageSetMirrorSpec{Filter: ClusterFilter{Include: []ClusterFilterItem{{Namespace: "allowed"}}}}}
	match, err = cism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("allowed", nil, nil))).To(BeTrue())
	g.Expect(match(pod("denied", nil, nil))).To(BeFalse())
}

func TestClusterImageSetAvailabilityFilterPrecedence(t *testing.T) {
	g := NewWithT(t)

	cisa := &ClusterImageSetAvailability{Spec: ClusterImageSetAvailabilitySpec{
		ImageFilter: ImageFilterDefinition{Include: []string{"legacy-only"}},
		Filter: ClusterFilter{Include: []ClusterFilterItem{
			{Namespace: "allowed"},
			{FilterItem: FilterItem{Image: "nginx"}},
		}},
	}}

	match, err := cisa.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("allowed", nil, nil))).To(BeTrue())
	g.Expect(match(pod("denied", nil, nil))).To(BeFalse())

	imgFilter, err := cisa.ImageFilter()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(imgFilter.Match("nginx")).To(BeTrue())
	g.Expect(imgFilter.Match("legacy-only")).To(BeFalse(), "deprecated imageFilter must be ignored when filter is set")
}

func TestClusterReplicatedImageSetFilterNamespace(t *testing.T) {
	g := NewWithT(t)

	cris := &ClusterReplicatedImageSet{Spec: ClusterReplicatedImageSetSpec{Filter: ClusterFilter{Include: []ClusterFilterItem{{Namespace: "allowed"}}}}}
	match, err := cris.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("allowed", nil, nil))).To(BeTrue())
	g.Expect(match(pod("denied", nil, nil))).To(BeFalse())
}

func TestReplicatedImageSetFilterLabel(t *testing.T) {
	g := NewWithT(t)

	ris := &ReplicatedImageSet{
		Spec: ReplicatedImageSetSpec{
			Filter: Filter{Include: []FilterItem{{Label: "app=foo"}}},
		},
	}
	match, err := ris.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("any", map[string]string{"app": "foo"}, nil))).To(BeTrue())
	g.Expect(match(pod("any", map[string]string{"app": "bar"}, nil))).To(BeFalse())
}
