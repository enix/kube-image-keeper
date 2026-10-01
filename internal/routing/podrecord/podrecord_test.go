package podrecord

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	nginx       = "docker.io/library/nginx:1.27"
	mirrorNginx = "registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a"
	otherNginx  = "internal.tld/nginx:1.27"
)

var mirrorRewrite = Rewrite{By: "ImageMirror/prod", Origin: nginx, RewrittenTo: mirrorNginx, Policy: "OnFailure"}

// pod builds a pod with one container per name/image pair and the given annotations.
func pod(annotations map[string]string, containers ...corev1.Container) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", Annotations: annotations},
		Spec:       corev1.PodSpec{Containers: containers},
	}
}

func container(name, image string) corev1.Container {
	return corev1.Container{Name: name, Image: image}
}

var _ = Describe("Pod records", func() {
	Describe("Read", func() {
		It("decodes the entries of the three annotations, keyed by container name", func() {
			p := pod(map[string]string{
				AnnotationRewrites:         `{"web":{"by":"ImageMirror/prod","origin":"docker.io/library/nginx:1.27","rewrittenTo":"registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a","policy":"OnFailure"}}`,
				AnnotationConcededRewrites: `{"proxy":{"by":"ImageAlternative/quay","origin":"quay.io/acme/proxy:1","rewrittenTo":"ghcr.io/acme/proxy:1","policy":"Always"}}`,
				AnnotationNoAlternatives:   `{"reloader":["ImageAlternative/quay","ImageMirror/prod"]}`,
			})
			r, err := Read(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Rewrites).To(Equal(map[string]Rewrite{"web": mirrorRewrite}))
			Expect(r.Conceded).To(Equal(map[string]Rewrite{"proxy": {
				By: "ImageAlternative/quay", Origin: "quay.io/acme/proxy:1", RewrittenTo: "ghcr.io/acme/proxy:1", Policy: "Always",
			}}))
			Expect(r.NoAlternatives).To(Equal(map[string][]string{"reloader": {"ImageAlternative/quay", "ImageMirror/prod"}}))
		})

		DescribeTable("a malformed annotation",
			func(annotation string) {
				r, err := Read(pod(map[string]string{annotation: "{not json"}))
				Expect(err).To(MatchError(ContainSubstring(annotation)))
				Expect(r.Rewrites).To(BeEmpty())
				Expect(r.Conceded).To(BeEmpty())
				Expect(r.NoAlternatives).To(BeEmpty())
			},
			Entry("reads kuik.enix.io/rewrites as empty and names it in the error", AnnotationRewrites),
			Entry("reads kuik.enix.io/conceded-rewrites as empty and names it in the error", AnnotationConcededRewrites),
			Entry("reads kuik.enix.io/no-alternatives as empty and names it in the error", AnnotationNoAlternatives),
		)

		It("still decodes the well-formed annotations next to a malformed one", func() {
			r, err := Read(pod(map[string]string{
				AnnotationRewrites:       "{not json",
				AnnotationNoAlternatives: `{"reloader":["ImageMirror/prod"]}`,
			}))
			Expect(err).To(HaveOccurred())
			Expect(r.Rewrites).To(BeEmpty())
			Expect(r.NoAlternatives).To(HaveKey("reloader"))
		})
	})

	Describe("Standing", func() {
		rewrites := `{"web":{"by":"ImageMirror/prod","origin":"docker.io/library/nginx:1.27","rewrittenTo":"registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a","policy":"OnFailure"}}`

		It("returns the rewrites whose container still runs rewrittenTo", func() {
			p := pod(map[string]string{AnnotationRewrites: rewrites}, container("web", mirrorNginx))
			Expect(Standing(p)).To(Equal(map[string]Rewrite{"web": mirrorRewrite}))
		})

		It("leaves out a rewrite whose container was edited after admission", func() {
			p := pod(map[string]string{AnnotationRewrites: rewrites}, container("web", otherNginx))
			Expect(Standing(p)).To(BeEmpty())
		})

		It("leaves out a rewrite naming a container the pod no longer has", func() {
			p := pod(map[string]string{AnnotationRewrites: rewrites}, container("sidecar", mirrorNginx))
			Expect(Standing(p)).To(BeEmpty())
		})

		It("reads the initContainers like the containers", func() {
			p := pod(map[string]string{AnnotationRewrites: rewrites})
			p.Spec.InitContainers = []corev1.Container{container("web", mirrorNginx)}
			Expect(Standing(p)).To(HaveKey("web"))
		})

		It("never returns a conceded rewrite", func() {
			p := pod(map[string]string{
				AnnotationConcededRewrites: rewrites,
			}, container("web", mirrorNginx))
			Expect(Standing(p)).To(BeEmpty())
		})
	})
})
