package kuik

import (
	"strings"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/enix/kube-image-keeper/internal/routing"
)

// resourceLog is the logger of the lines about the kuik resource kind/name: named after its
// kind in lowercase, carrying it under "resource" as <kind>/<name>, the keys of the logs page
// (docs/observability.md, "Logs").
func resourceLog(base logr.Logger, kind, name string) logr.Logger {
	return base.WithName(strings.ToLower(kind)).WithValues("resource", routing.Resource{Kind: kind, Name: name}.String())
}

// reconcileLog builds the logger of a reconcile of a resource of kind, in place of
// controller-runtime's default: the resource replaces the keys it names the object with
// (<Kind>, namespace, name, controller...). A line of the controller itself, with no
// request, carries the logger name alone.
func reconcileLog(base logr.Logger, kind string) func(*reconcile.Request) logr.Logger {
	return func(req *reconcile.Request) logr.Logger {
		if req == nil {
			return base.WithName(strings.ToLower(kind))
		}
		return resourceLog(base, kind, req.Name)
	}
}
