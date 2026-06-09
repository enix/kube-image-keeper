package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"
)

// These tests cover the accessor precedence wiring: when spec.filter is set it
// wins over the legacy imageFilter / podFilter / namespaceFilter fields; when it
// is empty the legacy fields are used (parity with the pre-filter behaviour).

func TestImageSetMirrorFilterPrecedence(t *testing.T) {
	g := NewWithT(t)

	// filter set: it wins, the legacy podFilter/imageFilter are ignored.
	ism := &ImageSetMirror{Spec: ImageSetMirrorSpec{
		ImageSetMirrorBase: ImageSetMirrorBase{
			PodFilter:   PodFilterDefinition{Labels: SelectorFilter{Include: []string{"legacy=yes"}}},
			ImageFilter: ImageFilterDefinition{Include: []string{"legacy-only"}},
		},
		Filter: Filter{Include: []FilterItem{{Label: "app=foo"}, {Image: "nginx"}}},
	}}

	match, err := ism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("any", map[string]string{"app": "foo"}, nil))).To(BeTrue(), "filter label should match")
	g.Expect(match(pod("any", map[string]string{"legacy": "yes"}, nil))).To(BeFalse(), "legacy podFilter must be ignored when filter is set")

	imgFilter, err := ism.ImageFilter()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(imgFilter.Match("nginx")).To(BeTrue())
	g.Expect(imgFilter.Match("legacy-only")).To(BeFalse(), "legacy imageFilter must be ignored when filter is set")
}

func TestImageSetMirrorLegacyFallback(t *testing.T) {
	g := NewWithT(t)

	// filter empty: legacy fields drive matching.
	ism := &ImageSetMirror{Spec: ImageSetMirrorSpec{ImageSetMirrorBase: ImageSetMirrorBase{
		PodFilter:   PodFilterDefinition{Labels: SelectorFilter{Include: []string{"legacy=yes"}}},
		ImageFilter: ImageFilterDefinition{Include: []string{"legacy-only"}},
	}}}

	match, err := ism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("any", map[string]string{"legacy": "yes"}, nil))).To(BeTrue())
	g.Expect(match(pod("any", map[string]string{"app": "foo"}, nil))).To(BeFalse())

	imgFilter, err := ism.ImageFilter()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(imgFilter.Match("legacy-only")).To(BeTrue())
	g.Expect(imgFilter.Match("nginx")).To(BeFalse())
}

func TestClusterImageSetMirrorFilterNamespacePrecedence(t *testing.T) {
	g := NewWithT(t)

	// filter set with a namespace item: it replaces namespaceFilter+podFilter.
	cism := &ClusterImageSetMirror{Spec: ClusterImageSetMirrorSpec{
		ImageSetMirrorBase: ImageSetMirrorBase{PodFilter: PodFilterDefinition{Labels: SelectorFilter{Include: []string{"legacy=yes"}}}},
		NamespaceFilter:    NamespaceFilterDefinition{Include: []string{"legacy-ns"}},
		Filter:             ClusterFilter{Include: []ClusterFilterItem{{Namespace: "allowed"}}},
	}}

	match, err := cism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("allowed", nil, nil))).To(BeTrue())
	g.Expect(match(pod("legacy-ns", map[string]string{"legacy": "yes"}, nil))).To(BeFalse(),
		"legacy namespaceFilter/podFilter must be ignored when filter is set")
}

func TestClusterImageSetAvailabilityFilterPrecedence(t *testing.T) {
	g := NewWithT(t)

	cisa := &ClusterImageSetAvailability{Spec: ClusterImageSetAvailabilitySpec{
		ImageFilter:     ImageFilterDefinition{Include: []string{"legacy-only"}},
		NamespaceFilter: NamespaceFilterDefinition{Include: []string{"legacy-ns"}},
		Filter: ClusterFilter{Include: []ClusterFilterItem{
			{Namespace: "allowed"},
			{FilterItem: FilterItem{Image: "nginx"}},
		}},
	}}

	match, err := cisa.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("allowed", nil, nil))).To(BeTrue())
	g.Expect(match(pod("legacy-ns", nil, nil))).To(BeFalse())

	imgFilter, err := cisa.ImageFilter()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(imgFilter.Match("nginx")).To(BeTrue())
	g.Expect(imgFilter.Match("legacy-only")).To(BeFalse())
}

func TestClusterReplicatedImageSetFilterNamespace(t *testing.T) {
	g := NewWithT(t)

	cris := &ClusterReplicatedImageSet{Spec: ClusterReplicatedImageSetSpec{
		NamespaceFilter: NamespaceFilterDefinition{Include: []string{"legacy-ns"}},
		Filter:          ClusterFilter{Include: []ClusterFilterItem{{Namespace: "allowed"}}},
	}}

	match, err := cris.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("allowed", nil, nil))).To(BeTrue())
	g.Expect(match(pod("legacy-ns", nil, nil))).To(BeFalse())
}
