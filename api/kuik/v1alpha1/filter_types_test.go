package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pod(namespace string, labels, annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
	}
}

func TestFilterIsEmpty(t *testing.T) {
	g := NewWithT(t)

	g.Expect(Filter{}.IsEmpty()).To(BeTrue())
	g.Expect(ClusterFilter{}.IsEmpty()).To(BeTrue())
	g.Expect(Filter{Include: []FilterItem{{Image: "nginx"}}}.IsEmpty()).To(BeFalse())
	g.Expect(Filter{Exclude: []FilterItem{{Image: "nginx"}}}.IsEmpty()).To(BeFalse())
	g.Expect(ClusterFilter{Include: []ClusterFilterItem{{Namespace: "ns"}}}.IsEmpty()).To(BeFalse())
}

func TestFilterBuildImageFilter(t *testing.T) {
	tests := []struct {
		name        string
		filter      Filter
		image       string
		shouldMatch bool
	}{
		{
			name:        "empty image dimension matches everything",
			filter:      Filter{Include: []FilterItem{{Label: "app=foo"}}},
			image:       "docker.io/library/nginx:latest",
			shouldMatch: true,
		},
		{
			name:        "single include matches",
			filter:      Filter{Include: []FilterItem{{Image: "docker.io/library/nginx.*"}}},
			image:       "docker.io/library/nginx:latest",
			shouldMatch: true,
		},
		{
			name:        "single include rejects non-match",
			filter:      Filter{Include: []FilterItem{{Image: "docker.io/library/nginx.*"}}},
			image:       "docker.io/library/redis:latest",
			shouldMatch: false,
		},
		{
			name: "items within image dimension are OR'd",
			filter: Filter{Include: []FilterItem{
				{Image: "docker.io/library/nginx.*"},
				{Image: "docker.io/library/redis.*"},
			}},
			image:       "docker.io/library/redis:latest",
			shouldMatch: true,
		},
		{
			name: "exclude wins over include",
			filter: Filter{
				Include: []FilterItem{{Image: "docker.io/library/.*"}},
				Exclude: []FilterItem{{Image: "docker.io/library/redis.*"}},
			},
			image:       "docker.io/library/redis:latest",
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			f, err := tt.filter.BuildImageFilter()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(f.Match(tt.image)).To(Equal(tt.shouldMatch))
		})
	}
}

func TestFilterBuildPodMatcher(t *testing.T) {
	tests := []struct {
		name        string
		filter      Filter
		pod         *corev1.Pod
		shouldMatch bool
	}{
		{
			name:        "empty label/annotation dimensions match every pod",
			filter:      Filter{Include: []FilterItem{{Image: "nginx"}}},
			pod:         pod("any", nil, nil),
			shouldMatch: true,
		},
		{
			name:        "label include matches",
			filter:      Filter{Include: []FilterItem{{Label: "app=foo"}}},
			pod:         pod("any", map[string]string{"app": "foo"}, nil),
			shouldMatch: true,
		},
		{
			name:        "label include rejects non-match",
			filter:      Filter{Include: []FilterItem{{Label: "app=foo"}}},
			pod:         pod("any", map[string]string{"app": "bar"}, nil),
			shouldMatch: false,
		},
		{
			name: "label and annotation dimensions are AND'd across",
			filter: Filter{Include: []FilterItem{
				{Label: "app=foo"},
				{Annotation: "monitoring=enabled"},
			}},
			pod:         pod("any", map[string]string{"app": "foo"}, map[string]string{"monitoring": "enabled"}),
			shouldMatch: true,
		},
		{
			name: "missing one of the AND'd dimensions rejects",
			filter: Filter{Include: []FilterItem{
				{Label: "app=foo"},
				{Annotation: "monitoring=enabled"},
			}},
			pod:         pod("any", map[string]string{"app": "foo"}, nil),
			shouldMatch: false,
		},
		{
			name:        "exclude label wins",
			filter:      Filter{Exclude: []FilterItem{{Label: "app=foo"}}},
			pod:         pod("any", map[string]string{"app": "foo"}, nil),
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			match, err := tt.filter.BuildPodMatcher()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(match(tt.pod)).To(Equal(tt.shouldMatch))
		})
	}
}

func TestClusterFilterBuildPodMatcherNamespace(t *testing.T) {
	tests := []struct {
		name        string
		filter      ClusterFilter
		pod         *corev1.Pod
		shouldMatch bool
	}{
		{
			name:        "empty namespace dimension matches every namespace",
			filter:      ClusterFilter{Include: []ClusterFilterItem{{FilterItem: FilterItem{Label: "app=foo"}}}},
			pod:         pod("whatever", map[string]string{"app": "foo"}, nil),
			shouldMatch: true,
		},
		{
			name:        "namespace include matches",
			filter:      ClusterFilter{Include: []ClusterFilterItem{{Namespace: "allowed"}}},
			pod:         pod("allowed", nil, nil),
			shouldMatch: true,
		},
		{
			name:        "namespace include rejects non-match",
			filter:      ClusterFilter{Include: []ClusterFilterItem{{Namespace: "allowed"}}},
			pod:         pod("denied", nil, nil),
			shouldMatch: false,
		},
		{
			name: "namespace AND'd with label dimension",
			filter: ClusterFilter{Include: []ClusterFilterItem{
				{Namespace: "allowed"},
				{FilterItem: FilterItem{Label: "app=foo"}},
			}},
			pod:         pod("allowed", map[string]string{"app": "foo"}, nil),
			shouldMatch: true,
		},
		{
			name: "matching label but wrong namespace rejects",
			filter: ClusterFilter{Include: []ClusterFilterItem{
				{Namespace: "allowed"},
				{FilterItem: FilterItem{Label: "app=foo"}},
			}},
			pod:         pod("denied", map[string]string{"app": "foo"}, nil),
			shouldMatch: false,
		},
		{
			name:        "exclude namespace wins",
			filter:      ClusterFilter{Exclude: []ClusterFilterItem{{Namespace: "denied"}}},
			pod:         pod("denied", nil, nil),
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			match, err := tt.filter.BuildPodMatcher()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(match(tt.pod)).To(Equal(tt.shouldMatch))
		})
	}
}

func TestClusterFilterToFilterDropsNamespace(t *testing.T) {
	g := NewWithT(t)

	cf := ClusterFilter{
		Include: []ClusterFilterItem{
			{FilterItem: FilterItem{Image: "nginx"}, Namespace: "allowed"},
			{FilterItem: FilterItem{Label: "app=foo"}},
		},
		Exclude: []ClusterFilterItem{
			{Namespace: "denied"},
		},
	}

	f := cf.ToFilter()
	g.Expect(f.Include).To(Equal([]FilterItem{{Image: "nginx"}, {Label: "app=foo"}}))
	g.Expect(f.Exclude).To(Equal([]FilterItem{{}}))

	// The projected filter no longer constrains the namespace: a pod that the
	// cluster filter would reject by namespace is accepted once projected.
	match, err := f.BuildPodMatcher()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(match(pod("denied", map[string]string{"app": "foo"}, nil))).To(BeTrue())
}

func TestClusterFilterBuildImageFilter(t *testing.T) {
	g := NewWithT(t)

	cf := ClusterFilter{Include: []ClusterFilterItem{
		{FilterItem: FilterItem{Image: "docker.io/library/nginx.*"}, Namespace: "allowed"},
	}}

	f, err := cf.BuildImageFilter()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(f.Match("docker.io/library/nginx:latest")).To(BeTrue())
	g.Expect(f.Match("docker.io/library/redis:latest")).To(BeFalse())
}
