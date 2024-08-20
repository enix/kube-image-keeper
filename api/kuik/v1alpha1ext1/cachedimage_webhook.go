package v1alpha1ext1

import (
	"context"

	"github.com/adisplayname/kube-image-keeper/internal/registry"
	"github.com/distribution/reference"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *CachedImage) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		WithDefaulter(r).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-kuik-enix-io-v1alpha1-cachedimage,mutating=true,failurePolicy=fail,sideEffects=None,groups=kuik.enix.io,resources=cachedimages,verbs=create;update,versions=v1alpha1,name=mcachedimage.kb.io,admissionReviewVersions=v1

func (r *CachedImage) Default(ctx context.Context, obj runtime.Object) error {
	cachedImage := obj.(*CachedImage)

	named, err := reference.ParseNormalizedNamed(cachedImage.Spec.SourceImage)
	if err != nil {
		return field.Invalid(field.NewPath("spec.sourceImage"), cachedImage.Spec.SourceImage, err.Error())
	}

	if cachedImage.Labels == nil {
		cachedImage.Labels = map[string]string{}
	}

	cachedImage.Labels[RepositoryLabelName] = registry.RepositoryLabel(named.Name())

	return nil
}
