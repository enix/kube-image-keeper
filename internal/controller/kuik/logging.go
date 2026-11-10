package kuik

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileLog builds the logger of a reconcile of a resource of kind.
//
//nolint:unused // A stub, wired in SetupWithManager by the fix.
func reconcileLog(base logr.Logger, kind string) func(*reconcile.Request) logr.Logger {
	return func(req *reconcile.Request) logr.Logger {
		if req == nil {
			return base
		}
		return base.WithValues("controllerKind", kind, "namespace", req.Namespace, "name", req.Name)
	}
}
