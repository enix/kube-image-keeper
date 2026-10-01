// Package routingstatus builds the routing side of a status, field for field the same on an
// ImageAlternative and an ImageMirror: the pods and containers gauges, the four anomaly lists,
// the FallbackActive and AlternativesExhausted conditions, the pod events and the routing
// series. See docs/v3/status.md, "ImageAlternative", and docs/v3/observability.md.
package routingstatus

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/capped"
)

// The pod events. See docs/v3/observability.md, "Catalogue".
const (
	EventImageFallback          = "ImageFallback"
	EventNoAlternativeAvailable = "NoAlternativeAvailable"
	EventRewriteConceded        = "RewriteConceded"
	EventRewriteStale           = "RewriteStale"
)

// Tracker reports the routing side of the resources of one controller. It remembers the pod
// events it emitted, so each is emitted once per lease.
type Tracker struct{}

// NewTracker returns a tracker emitting with recorder and exporting to registerer.
func NewTracker(recorder events.EventRecorder, registerer prometheus.Registerer) (*Tracker, error) {
	return &Tracker{}, nil
}

// Elected records when the reconciler acquired its lease. Before it, no pod event is emitted;
// after it, ImageFallback, NoAlternativeAvailable and RewriteConceded go only to pods created
// since.
func (r *Tracker) Elected(at time.Time) {}

// Input is what one report of a resource reads.
type Input struct {
	// Resource is the resource reported on.
	Resource routing.Resource
	// Pods are the live pods the resource selects.
	Pods []*corev1.Pod
	// Previous is the routing side of the status as last written, `since` being carried
	// forward from it.
	Previous kuikv1alpha1.RoutingStatus
	// Now stamps the entries that appear.
	Now metav1.Time
}

// Report returns the routing side of the status, its lists whole and oldest first, emits the
// pod events and publishes the routing series, which hold every entry.
func (r *Tracker) Report(in Input) kuikv1alpha1.RoutingStatus {
	return kuikv1alpha1.RoutingStatus{}
}

// Forget removes the series and the memory of a deleted resource.
func (r *Tracker) Forget(resource routing.Resource) {}

// SetConditions sets FallbackActive and AlternativesExhausted on conditions from status,
// read before it is capped. kind words the message: an ImageMirror routes to the mirror.
func SetConditions(conditions *[]metav1.Condition, kind string, status kuikv1alpha1.RoutingStatus, generation int64) {
}

// Cap caps the four anomaly lists of status in pass, under their status field names.
func Cap(pass *capped.Pass, status *kuikv1alpha1.RoutingStatus) {}
