package v1

import (
	_ "crypto/sha256"
	"errors"
	"regexp"
	"testing"

	"github.com/adisplayname/kube-image-keeper/internal/controller/core"
	"github.com/adisplayname/kube-image-keeper/internal/registry"
	. "github.com/onsi/gomega"
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
			{Name: "d", Image: "185.145.250.247:30042/alpine"},
			{Name: "e", Image: "185.145.250.247:30042/alpine:latest"},
			{Name: "f", Image: "invalid:image:8080"},
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

		ir.RewriteImages(&podStub, false)
		g.Expect(podStub.Annotations[core.AnnotationRewriteImagesName]).To(Equal("false"))

		ir.RewriteImages(&podStub, true)

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

		g.Expect(podStub.Spec.InitContainers).To(Equal(rewrittenInitContainers))
		g.Expect(podStub.Spec.Containers).To(Equal(rewrittenContainers))

		g.Expect(podStub.Labels[core.LabelManagedName]).To(Equal("true"))

		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("a", true)]).To(Equal("original-init"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("b", false)]).To(Equal("original"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("c", false)]).To(Equal("original-2"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("d", false)]).To(Equal("185.145.250.247:30042/alpine"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("e", false)]).To(Equal("185.145.250.247:30042/alpine:latest"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("f", false)]).To(Equal(""))

		ir.RewriteImages(&podStub, false)
		g.Expect(podStub.Annotations[core.AnnotationRewriteImagesName]).To(Equal("true"))
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
		ir.RewriteImages(&podStub, true)

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

		g.Expect(podStub.Labels[core.LabelManagedName]).To(Equal("true"))

		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("a", true)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("b", false)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("c", false)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("d", false)]).To(Equal("185.145.250.247:30042/alpine"))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("e", false)]).To(Equal(""))
		g.Expect(podStub.Annotations[registry.ContainerAnnotationKey("f", false)]).To(Equal(""))
	})
}

func Test_isImageRewritable(t *testing.T) {
	emptyRegexps := []*regexp.Regexp{}
	someRegexps := []*regexp.Regexp{
		regexp.MustCompile("alpine"),
		regexp.MustCompile(".*:latest"),
	}

	tests := []struct {
		name                   string
		image                  string
		imagePullPolicy        corev1.PullPolicy
		ignorePullPolicyAlways bool
		regexps                []*regexp.Regexp
		err                    error
	}{
		{
			name:    "No regex",
			image:   "alpine",
			regexps: emptyRegexps,
			err:     nil,
		},
		{
			name:    "No regex with digest",
			image:   "alpine:latest@sha256:5b161f051d017e55d358435f295f5e9a297e66158f136321d9b04520ec6c48a3",
			regexps: emptyRegexps,
			err:     errImageContainsDigests,
		},
		{
			name:    "Match first regex",
			image:   "alpine",
			regexps: someRegexps,
			err:     errors.New("image matches alpine"),
		},
		{
			name:    "Match second regex",
			image:   "nginx:latest",
			regexps: someRegexps,
			err:     errors.New("image matches .*:latest"),
		},
		{
			name:    "Match no regex",
			image:   "nginx",
			regexps: someRegexps,
			err:     nil,
		},
		{
			name:                   "Ignore ImagePullPolicy: Always",
			image:                  "nginx:1.0.0",
			imagePullPolicy:        corev1.PullAlways,
			ignorePullPolicyAlways: true,
			err:                    errPullPolicyAlways,
		},
		{
			name:                   "Ignore ImagePullPolicy: Always with tag latest",
			image:                  "nginx:latest",
			ignorePullPolicyAlways: true,
			err:                    errPullPolicyAlways,
		},
		{
			name:                   "Ignore ImagePullPolicy: Always without any tag",
			image:                  "nginx",
			ignorePullPolicyAlways: true,
			err:                    errPullPolicyAlways,
		},
		{
			name:                   "Ignore ImagePullPolicy: Always with tag latest but ImagePullPolicy: IfNotPresent",
			image:                  "nginx:latest",
			imagePullPolicy:        corev1.PullIfNotPresent,
			ignorePullPolicyAlways: true,
			err:                    nil,
		},
		{
			name:            "ImagePullPolicy: Never",
			image:           "nginx:1.0.0",
			imagePullPolicy: corev1.PullNever,
			err:             errPullPolicyNever,
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imageRewriter := ImageRewriter{
				IgnoreImages:           tt.regexps,
				IgnorePullPolicyAlways: tt.ignorePullPolicyAlways,
			}

			err := imageRewriter.isImageRewritable(&corev1.Container{
				Image:           tt.image,
				ImagePullPolicy: tt.imagePullPolicy,
			})

			if tt.err == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(Equal(tt.err))
			}

		})
	}
}
