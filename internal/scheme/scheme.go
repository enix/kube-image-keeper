package scheme

import (

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	kuikv1alpha1ext1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1ext1"
	//+kubebuilder:scaffold:imports
)

func NewScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(kuikv1alpha1ext1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme

	return scheme
}
