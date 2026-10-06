// Package imagemetrics exports the image series an ImageMonitor and an ImageMirror both
// produce: kuik_images_tracked, kuik_images_checked and kuik_image_drifted. One definition
// serves both controllers, so the two register the same collectors. See
// docs/v3/observability.md, "Aggregates" and "Anomalies signal by presence".
package imagemetrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// The populations of the reference label, named after the status block they mirror.
const (
	ReferenceOrigin       = "origin"
	ReferenceAlternatives = "alternatives"
	ReferenceCopy         = "copy"
)

// Counts are the gauges of one population of a resource.
type Counts struct {
	Running, Standby, Retained int32
	Available, Unavailable     int32
}

// Exporter exports the image series of the resources of one controller.
type Exporter struct{}

// New returns an exporter on registerer, reusing the collectors another controller registered
// there.
func New(registerer prometheus.Registerer) (*Exporter, error) {
	return &Exporter{}, nil
}

// SetCounts exports the counts of the population reference of the resource (kind, name).
func (e *Exporter) SetCounts(kind, name, reference string, c Counts) {}

// SetDrifted exports one kuik_image_drifted series per image of the resource (kind, name),
// images being origin references, and deletes the series of the images it no longer lists.
func (e *Exporter) SetDrifted(kind, name string, images []string) {}

// Forget deletes every series of the resource (kind, name).
func (e *Exporter) Forget(kind, name string) {}
