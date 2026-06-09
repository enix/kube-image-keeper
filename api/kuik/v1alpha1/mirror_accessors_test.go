package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func podWith(namespace string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Labels: labels},
	}
}

func TestImageSetMirrorAccessors(t *testing.T) {
	g := NewWithT(t)

	ism := &ImageSetMirror{
		Spec: ImageSetMirrorSpec{
			ImageSetMirrorBase: ImageSetMirrorBase{
				PodFilter: PodFilterDefinition{
					Labels: SelectorFilter{Include: []string{"app=foo"}},
				},
			},
		},
	}

	g.Expect(ism.MirrorSpec()).To(BeIdenticalTo(&ism.Spec.ImageSetMirrorBase))
	g.Expect(ism.MirrorStatus()).To(BeIdenticalTo(&ism.Status))

	match, err := ism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())

	// The namespaced kind ignores namespace entirely: only the pod label filter applies.
	g.Expect(match(podWith("any-ns", map[string]string{"app": "foo"}))).To(BeTrue())
	g.Expect(match(podWith("other-ns", map[string]string{"app": "foo"}))).To(BeTrue())
	g.Expect(match(podWith("any-ns", map[string]string{"app": "bar"}))).To(BeFalse())
}

func TestClusterImageSetMirrorAccessorsFoldNamespace(t *testing.T) {
	g := NewWithT(t)

	cism := &ClusterImageSetMirror{
		Spec: ClusterImageSetMirrorSpec{
			ImageSetMirrorBase: ImageSetMirrorBase{
				PodFilter: PodFilterDefinition{
					Labels: SelectorFilter{Include: []string{"app=foo"}},
				},
			},
			NamespaceFilter: NamespaceFilterDefinition{
				Include: []string{"allowed-ns"},
			},
		},
	}

	// MirrorSpec unwraps the embedded base; MirrorStatus exposes the shared status type.
	g.Expect(cism.MirrorSpec()).To(BeIdenticalTo(&cism.Spec.ImageSetMirrorBase))
	g.Expect(cism.MirrorStatus()).To(BeIdenticalTo((*ImageSetMirrorStatus)(&cism.Status)))

	match, err := cism.PodMatcher()
	g.Expect(err).NotTo(HaveOccurred())

	// The cluster kind folds the namespace filter into PodMatcher: both the pod label
	// filter AND the namespace filter must pass.
	g.Expect(match(podWith("allowed-ns", map[string]string{"app": "foo"}))).To(BeTrue())
	g.Expect(match(podWith("denied-ns", map[string]string{"app": "foo"}))).To(BeFalse(),
		"pod in a non-matching namespace must be rejected")
	g.Expect(match(podWith("allowed-ns", map[string]string{"app": "bar"}))).To(BeFalse(),
		"pod with a non-matching label must be rejected even in an allowed namespace")
}
