package v1alpha1ext1

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestDefault(t *testing.T) {
	cachedImageStub := CachedImage{}

	g := NewWithT(t)
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
			cachedImage := cachedImageStub.DeepCopy()
			cachedImage.Spec.SourceImage = tt.sourceImage

			err := (&CachedImage{}).Default(context.TODO(), cachedImage)

			if tt.wantErr == nil {
				g.Expect(cachedImage.Labels).ToNot(BeNil())
				g.Expect(cachedImage.Labels[RepositoryLabelName]).To(Equal(tt.expectedRepositoryLabel))
			} else {
				g.Expect(err).To(Equal(tt.wantErr))
			}

		})
	}
}
