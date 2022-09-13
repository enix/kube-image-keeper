package controllers

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	dcrenixiov1alpha1 "gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDesiredCachedImages(t *testing.T) {
	tests := []struct {
		name         string
		pod          corev1.Pod
		cachedImages []v1alpha1.CachedImage
		wantErr      error
	}{
		{
			name: "basic",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Annotations: map[string]string{
						"original-init-image-0": "original-init",
						"original-image-0":      "original",
						"original-image-1":      "original-2",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{Image: "test-init"},
					},
					Containers: []corev1.Container{
						{Image: "test"},
						{Image: "test-2"},
					},
				},
			},
			cachedImages: []v1alpha1.CachedImage{
				{Spec: dcrenixiov1alpha1.CachedImageSpec{
					Image:       "test",
					SourceImage: "original",
				}},
				{Spec: dcrenixiov1alpha1.CachedImageSpec{
					Image:       "test-2",
					SourceImage: "original-2",
				}},
				{Spec: dcrenixiov1alpha1.CachedImageSpec{
					Image:       "test-init",
					SourceImage: "original-init",
				}},
			},
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cachedImages, err := desiredCachedImages(context.Background(), &tt.pod)
			if tt.wantErr != nil {
				g.Expect(err).To(Equal(tt.wantErr))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(cachedImages).To(HaveLen(len(tt.cachedImages)))
				for i, cachedImage := range cachedImages {
					g.Expect(cachedImage.Spec.Image).To(Equal(tt.cachedImages[i].Spec.Image))
					g.Expect(cachedImage.Spec.SourceImage).To(Equal(tt.cachedImages[i].Spec.SourceImage))
					g.Expect(cachedImage.Spec.PullSecretsNamespace).To(Equal(tt.pod.Namespace))

					pullSecretNames := []string{}
					for _, pullSecret := range tt.pod.Spec.ImagePullSecrets {
						pullSecretNames = append(pullSecretNames, pullSecret.Name)
					}
					g.Expect(cachedImage.Spec.PullSecretNames).To(ConsistOf(pullSecretNames))

				}
			}
		})
	}
}
