package v1alpha1

import (
	"context"
	"testing"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestDefault(t *testing.T) {
	cachedImageStub := kuikv1alpha1.CachedImage{}

	tests := []struct {
		name                    string
		sourceImage             string
		expectedRepositoryLabel string
		wantErr                 error
	}{
		{
			name:                    "Simple image name",
			sourceImage:             "alpine",
			expectedRepositoryLabel: "docker.io-library-alpine",
		},
		{
			name:                    "Advanced image name",
			sourceImage:             "quay.io/jetstack/cert-manager-controller:v1.13.2",
			expectedRepositoryLabel: "quay.io-jetstack-cert-manager-controller",
		},
		{
			name:        "Invalid image name",
			sourceImage: "@@@",
			wantErr:     field.Invalid(field.NewPath("spec.sourceImage"), "@@@", "invalid reference format"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			cachedImage := cachedImageStub.DeepCopy()
			cachedImage.Spec.SourceImage = tt.sourceImage

			defaulter := CachedImageCustomDefaulter{}
			err := defaulter.Default(context.TODO(), cachedImage)

			if tt.wantErr == nil {
				g.Expect(cachedImage.Labels).ToNot(BeNil())
				g.Expect(cachedImage.Labels[kuikv1alpha1.RepositoryLabelName]).To(Equal(tt.expectedRepositoryLabel))
			} else {
				g.Expect(err).To(Equal(tt.wantErr))
			}

		})
	}
}
