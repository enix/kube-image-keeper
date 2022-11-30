package v1

import (
	_ "crypto/sha256"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"gitlab.enix.io/products/docker-cache-registry/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		},
	},
}

func TestRewriteImages(t *testing.T) {
	g := NewWithT(t)
	t.Run("rewrite image", func(t *testing.T) {
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
		}

		g.Expect(podStub.Spec.InitContainers).To(Equal(rewrittenInitContainers))
		g.Expect(podStub.Spec.Containers).To(Equal(rewrittenContainers))

		g.Expect(podStub.Labels[controllers.LabelImageRewrittenName]).To(Equal("true"))

		g.Expect(podStub.Annotations[fmt.Sprintf(controllers.AnnotationOriginalInitImageTemplate, "a")]).To(Equal("original-init"))
		g.Expect(podStub.Annotations[fmt.Sprintf(controllers.AnnotationOriginalImageTemplate, "b")]).To(Equal("original"))
		g.Expect(podStub.Annotations[fmt.Sprintf(controllers.AnnotationOriginalImageTemplate, "c")]).To(Equal("original-2"))
	})
}
