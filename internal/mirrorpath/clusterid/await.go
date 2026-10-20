package clusterid

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// errNotRead is what Check reports until the cluster ID is read.
var errNotRead = errors.New("cluster ID not read yet from ConfigMap " + ConfigMapName)

// The webhook only reads the ConfigMap the reconciler creates.
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get,namespace=kuik-system,roleName=webhook

// Awaiter waits for the ConfigMap the reconciler creates and reads the cluster ID from it. It
// never creates the ConfigMap: a process that does not hold the reconciler's permissions
// waits for it instead.
type Awaiter struct {
	reader    client.Reader
	namespace string
	id        atomic.Pointer[string]
	// minDelay and maxDelay bound the delay between two reads, doubled after each one.
	minDelay, maxDelay time.Duration
}

// Await returns an Awaiter reading the ConfigMap of namespace through r, which must read from
// the API server: the cache of a manager does not run before it starts. Add it to the manager
// to start polling.
func Await(r client.Reader, namespace string) *Awaiter {
	return &Awaiter{reader: r, namespace: namespace, minDelay: 500 * time.Millisecond, maxDelay: 5 * time.Second}
}

// Start polls the ConfigMap until it holds a valid cluster ID or ctx is done.
func (a *Awaiter) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithValues("configmap", klog.KRef(a.namespace, ConfigMapName))
	delay := a.minDelay
	var last string
	for {
		id, err := read(ctx, a.reader, a.namespace)
		if err == nil {
			a.id.Store(&id)
			log.Info("Loaded cluster ID", "clusterID", id)
			return nil
		}
		// A read cut short by the shutdown is no failure to report.
		if ctx.Err() != nil {
			return nil
		}
		// Each distinct failure is logged once, not at every read.
		if err.Error() != last {
			last = err.Error()
			switch {
			case apierrors.IsNotFound(err):
				log.Info("Waiting for ConfigMap, created by the reconciler")
			case errors.As(err, new(*invalidError)):
				log.Error(err, "Found an invalid cluster ID, fix the ConfigMap")
			case apierrors.IsForbidden(err):
				log.Error(err, "Failed to read ConfigMap, fix the RBAC of the webhook")
			default:
				log.Info("Failed to read ConfigMap", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(2*delay, a.maxDelay)
	}
}

// NeedLeaderElection is false: every replica of the webhook answers admission requests.
func (a *Awaiter) NeedLeaderElection() bool { return false }

// ID returns the cluster ID, and false until it is read.
func (a *Awaiter) ID() (string, bool) {
	if id := a.id.Load(); id != nil {
		return *id, true
	}
	return "", false
}

// Check is a healthz.Checker failing until the cluster ID is read.
func (a *Awaiter) Check(*http.Request) error {
	if _, read := a.ID(); !read {
		return errNotRead
	}
	return nil
}
