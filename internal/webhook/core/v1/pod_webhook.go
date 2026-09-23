package v1

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// nolint:unused
// log is for logging in this package.
var podlog = logf.Log.WithName("pod-resource")

// SetupPodWebhookWithManager registers the webhook for Pod in the manager.
func SetupPodWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &corev1.Pod{}).
		WithDefaulter(&PodDefaulter{}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// The three settings below are prescribed by docs/v3/architecture.md, "The admission path":
// fail open, CREATE only, reinvocable.
// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=ignore,reinvocationPolicy=IfNeeded,sideEffects=None,groups="",resources=pods,verbs=create,versions=v1,name=mpod-v1.kb.io,admissionReviewVersions=v1

// The webhook reads what it matches pods against and writes nothing: no status, no Secret,
// no pod (the AdmissionReview carries it).
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives;imagemirrors;imagemonitors,verbs=get;list;watch,roleName=webhook
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=webhook
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=webhook

// Cluster-wide Secret read, for the imagePullSecrets of the pod an admission probe checks. The
// chart binds it according to secretAccess, to the webhook and the reconciler, never to the syncer.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,roleName=secret-reader

// PodDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Pod when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type PodDefaulter struct {
	// TODO(user): Add more fields as needed for defaulting
}

// Default implements admission.Defaulter so a webhook will be registered for the Kind Pod.
func (d *PodDefaulter) Default(_ context.Context, obj *corev1.Pod) error {
	podlog.Info("Defaulting for Pod", "name", obj.GetName())

	// TODO(user): fill in your defaulting logic.

	return nil
}
