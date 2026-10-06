package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

var _ = Describe("Desired", func() {
	mirror := Mirror{Path: destinationPath, CleanupEnabled: true}

	desired := func(m Mirror, pods []*corev1.Pod, pending ...kuikv1alpha1.PendingDeletion) []string {
		GinkgoHelper()
		got := Desired(m, pods, pending)
		refs := make([]string, 0, len(got))
		for _, ref := range got {
			refs = append(refs, ref.String())
		}
		return refs
	}
	pending := func(origin string) kuikv1alpha1.PendingDeletion {
		return kuikv1alpha1.PendingDeletion{
			Ref:         destinationPath + "ghcr.io/acme/report-job:v42_" + clusterID,
			Origin:      origin,
			UnusedSince: metav1.Now(),
		}
	}

	Context("from the containers of the selected pods", func() {
		It("takes each container's origin, never the mirror reference a rewritten container runs", func() {
			pod := rewritten(runningPod(
				container{"app", "mirror.other.tld/acme/app:v1"},
				container{"sidecar", "quay.io/acme/sidecar:v2"},
			), "app", "quay.io/acme/app:v1", "ImageAlternative/other")

			Expect(desired(mirror, []*corev1.Pod{pod})).To(ConsistOf("quay.io/acme/app:v1", "quay.io/acme/sidecar:v2"))
		})

		It("keeps the same image pulled from two registries as two references", func() {
			pod := runningPod(container{"a", "docker.io/acme/app:v1"}, container{"b", "ghcr.io/acme/app:v1"})

			Expect(desired(mirror, []*corev1.Pod{pod})).To(ConsistOf("docker.io/acme/app:v1", "ghcr.io/acme/app:v1"))
		})

		It("keeps a tagged reference and the same image pinned by digest as two references", func() {
			pod := runningPod(container{"a", "quay.io/acme/app:v1"}, container{"b", "quay.io/acme/app@" + digest})

			Expect(desired(mirror, []*corev1.Pod{pod})).To(ConsistOf("quay.io/acme/app:v1", "quay.io/acme/app@"+digest))
		})
	})

	Context("exclusions", func() {
		It("leaves out a reference matching excludeImages", func() {
			m := mirror
			m.ExcludeImages = []string{"ghcr.io/acme/**"}
			pod := runningPod(container{"a", "ghcr.io/acme/app:v1"}, container{"b", "quay.io/acme/app:v1"})

			Expect(desired(m, []*corev1.Pod{pod})).To(ConsistOf("quay.io/acme/app:v1"))
		})

		It("leaves out a reference under its own destination.path, whatever excludeImages says", func() {
			m := mirror
			m.ExcludeImages = []string{"docker.io/**"}
			pod := runningPod(
				container{"a", destinationPath + "quay.io/acme/app:v1_" + clusterID},
				container{"b", "quay.io/acme/tool:v3"},
			)

			Expect(desired(m, []*corev1.Pod{pod})).To(ConsistOf("quay.io/acme/tool:v3"))
		})
	})

	Context("retained references", func() {
		It("keeps a pendingDeletion entry carrying an origin while cleanup is enabled", func() {
			Expect(desired(mirror, nil, pending("ghcr.io/acme/report-job:v42"))).To(ConsistOf("ghcr.io/acme/report-job:v42"))
		})

		It("leaves out a pendingDeletion entry carrying no origin", func() {
			Expect(desired(mirror, nil, pending(""))).To(BeEmpty())
		})

		It("holds no reference past its last pod when cleanup is disabled", func() {
			m := mirror
			m.CleanupEnabled = false

			Expect(desired(m, nil, pending("ghcr.io/acme/report-job:v42"))).To(BeEmpty())
		})
	})
})
