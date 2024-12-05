package v1alpha1

// import (
// 	"context"

// 	"github.com/distribution/reference"
// 	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
// 	runtime "k8s.io/apimachinery/pkg/runtime"
// 	ctrl "sigs.k8s.io/controller-runtime"
// )

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/distribution/reference"
	"github.com/enix/kube-image-keeper/internal/registry"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var cachedimagelog = logf.Log.WithName("cachedimage-resource")

// SetupCachedImageWebhookWithManager registers the webhook for CachedImage in the manager.
func SetupCachedImageWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&kuikv1alpha1.CachedImage{}).
		WithDefaulter(&CachedImageCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-kuik-enix-io-v1alpha1-cachedimage,mutating=true,failurePolicy=fail,sideEffects=None,groups=kuik.enix.io,resources=cachedimages,verbs=create;update,versions=v1alpha1,name=mcachedimage.kb.io,admissionReviewVersions=v1

// CachedImageCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind CachedImage when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type CachedImageCustomDefaulter struct {
	// TODO(user): Add more fields as needed for defaulting
}

var _ webhook.CustomDefaulter = &CachedImageCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind CachedImage.
func (d *CachedImageCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	cachedImage, ok := obj.(*kuikv1alpha1.CachedImage)

	if !ok {
		return fmt.Errorf("expected an CachedImage object but got %T", obj)
	}
	cachedimagelog.Info("Defaulting for CachedImage", "name", cachedImage.GetName())

	named, err := reference.ParseNormalizedNamed(cachedImage.Spec.SourceImage)
	if err != nil {
		return field.Invalid(field.NewPath("spec.sourceImage"), cachedImage.Spec.SourceImage, err.Error())
	}

	if cachedImage.Labels == nil {
		cachedImage.Labels = map[string]string{}
	}

	cachedImage.Labels[kuikv1alpha1.RepositoryLabelName] = registry.RepositoryLabel(named.Name())

	return nil
}
