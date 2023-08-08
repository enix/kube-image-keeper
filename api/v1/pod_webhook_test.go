package v1

import (
	_ "crypto/sha256"
	"regexp"
	"testing"

	"github.com/enix/kube-image-keeper/controllers"
	"github.com/enix/kube-image-keeper/internal/registry"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var podStub = corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-pod",
		Namespace: "default",
	},
	Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{
			{Name: "a", Image: "original-init"},
		},
		Containers: []corev1.Container{
			{Name: "b", Image: "original"},
			{Name: "c", Image: "localhost:1313/original-2"},
			{Name: "d", Image: "185.145.250.247:30042/alpine"},
			{Name: "e", Image: "185.145.250.247:30042/alpine:latest"},
			{Name: "f", Image: "invalid:image:8080"},
		},
		EphemeralContainers: []corev1.EphemeralContainer{
			{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{
					Name: "g", Image: "original",
				},
			},
		},
	},
}

func TestRewriteImages(t *testing.T) {
	podStub := *podStub.DeepCopy()

	g := NewWithT(t)
	t.Run("Rewrite image", func(t *testing.T) {
		ir := ImageRewriter{
			ProxyPort: 4242,
		}
		ir.RewriteImages(&podStub)

		rewrittenInitContainers := []corev1.Container{
			{Name: "a", Image: "localhost:4242/original-init"},
		}

		rewrittenContainers := []corev1.Container{
			{Name: "b", Image: "localhost:4242/original"},
			{Name: "c", Image: "localhost:4242/original-2"},
			{Name: "d", Image: "localhost:4242/185.145.250.247-30042/alpine"},
			{Name: "e", Image: "localhost:4242/185.145.250.247-30042/alpine:latest"},
			{Name: "f", Image: "invalid:image:8080"},
		}

		rewrittenEphemeralContainers := []corev1.EphemeralContainer{
			{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{
					Name: "g", Image: "localhost:4242/original",
				},
			},
		}

		g.Expect(podStub.Spec.InitContainers).To(Equal(rewrittenInitContainers))
		g.Expect(podStub.Spec.Containers).To(Equal(rewrittenContainers))
		g.Expect(podStub.Spec.EphemeralContainers).To(Equal(rewrittenEphemeralContainers))

		g.Expect(podStub.Labels[controllers.LabelImageRewrittenName]).To(Equal("true"))

		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("a", registry.InitContainer)]).To(Equal("original-init"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("b", registry.Container)]).To(Equal("original"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("c", registry.Container)]).To(Equal("original-2"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("d", registry.Container)]).To(Equal("185.145.250.247:30042/alpine"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("e", registry.Container)]).To(Equal("185.145.250.247:30042/alpine:latest"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("f", registry.Container)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("g", registry.EphemeralContainer)]).To(Equal("original"))
	})
}

func TestRewriteImagesWithIgnore(t *testing.T) {
	podStub := *podStub.DeepCopy()

	g := NewWithT(t)
	t.Run("Rewrite image", func(t *testing.T) {
		ir := ImageRewriter{
			ProxyPort: 4242,
			IgnoreImages: []*regexp.Regexp{
				regexp.MustCompile("original"),
				regexp.MustCompile("alpine:latest"),
			},
		}
		ir.RewriteImages(&podStub)

		rewrittenInitContainers := []corev1.Container{
			{Name: "a", Image: "original-init"},
		}

		rewrittenContainers := []corev1.Container{
			{Name: "b", Image: "original"},
			{Name: "c", Image: "localhost:1313/original-2"},
			{Name: "d", Image: "localhost:4242/185.145.250.247-30042/alpine"},
			{Name: "e", Image: "185.145.250.247:30042/alpine:latest"},
			{Name: "f", Image: "invalid:image:8080"},
		}

		g.Expect(podStub.Spec.InitContainers).To(Equal(rewrittenInitContainers))
		g.Expect(podStub.Spec.Containers).To(Equal(rewrittenContainers))

		g.Expect(podStub.Labels[controllers.LabelImageRewrittenName]).To(Equal("true"))

		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("a", registry.InitContainer)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("b", registry.Container)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("c", registry.Container)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("d", registry.Container)]).To(Equal("185.145.250.247:30042/alpine"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("e", registry.Container)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("f", registry.Container)]).To(Equal(""))
	})
}

func TestInjectDecoder(t *testing.T) {
	g := NewWithT(t)
	t.Run("Inject decoder", func(t *testing.T) {
		ir := ImageRewriter{}
		decoder := &admission.Decoder{}

		g.Expect(ir.decoder).To(BeNil())
		err := ir.InjectDecoder(decoder)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(ir.decoder).To(Not(BeNil()))
		g.Expect(ir.decoder).To(Equal(decoder))
	})
}
