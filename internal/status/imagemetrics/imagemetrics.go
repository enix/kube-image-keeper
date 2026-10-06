// Package imagemetrics exports the image series an ImageMonitor and an ImageMirror both
// produce: kuik_images_tracked, kuik_images_checked and kuik_image_drifted. One definition
// serves both controllers, so the two register the same collectors. See
// docs/v3/observability.md, "Aggregates" and "Anomalies signal by presence".
package imagemetrics

import (
	"errors"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// The populations of the reference label, named after the status block they mirror.
const (
	ReferenceOrigin       = "origin"
	ReferenceAlternatives = "alternatives"
	ReferenceCopy         = "copy"
)

// The labels of the series.
const (
	labelKind      = "kind"
	labelName      = "name"
	labelReference = "reference"
	labelState     = "state"
	labelImage     = "image"
	labelRegistry  = "registry"
)

// Counts are the gauges of one population of a resource.
type Counts struct {
	Running, Standby, Retained int32
	Available, Unavailable     int32
}

// Exporter exports the image series of the resources of one controller.
type Exporter struct {
	tracked, checked, drifted *prometheus.GaugeVec

	mu sync.Mutex
	// images are the images each resource has a kuik_image_drifted series for.
	images map[resource]map[string]bool
}

type resource struct{ kind, name string }

// New returns an exporter on registerer, reusing the collectors another controller registered
// there.
func New(registerer prometheus.Registerer) (*Exporter, error) {
	// The HELP texts are those of docs/v3/observability.md, word for word: the two
	// controllers register the same definition.
	tracked, err := register(registerer, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kuik_images_tracked",
		Help: "References a resource tracks, by what the cluster does with them",
	}, []string{labelKind, labelName, labelReference, labelState}))
	if err != nil {
		return nil, err
	}
	checked, err := register(registerer, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kuik_images_checked",
		Help: "References whose last check returned a verdict, by verdict",
	}, []string{labelKind, labelName, labelReference, labelState}))
	if err != nil {
		return nil, err
	}
	drifted, err := register(registerer, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kuik_image_drifted",
		Help: "1 while the digest a resource accounts for differs from the upstream digest of that tag. " +
			"`image` is the origin reference in both cases",
	}, []string{labelKind, labelName, labelImage, labelRegistry}))
	if err != nil {
		return nil, err
	}
	return &Exporter{tracked: tracked, checked: checked, drifted: drifted, images: map[resource]map[string]bool{}}, nil
}

// register registers c, or returns the collector already registered under its name.
func register(registerer prometheus.Registerer, c *prometheus.GaugeVec) (*prometheus.GaugeVec, error) {
	err := registerer.Register(c)
	if are := (prometheus.AlreadyRegisteredError{}); errors.As(err, &are) {
		if existing, ok := are.ExistingCollector.(*prometheus.GaugeVec); ok {
			return existing, nil
		}
	}
	return c, err
}

// SetCounts exports the counts of the population reference of the resource (kind, name).
func (e *Exporter) SetCounts(kind, name, reference string, c Counts) {
	for state, value := range map[string]int32{"running": c.Running, "standby": c.Standby, "retained": c.Retained} {
		e.tracked.WithLabelValues(kind, name, reference, state).Set(float64(value))
	}
	for state, value := range map[string]int32{"available": c.Available, "unavailable": c.Unavailable} {
		e.checked.WithLabelValues(kind, name, reference, state).Set(float64(value))
	}
}

// SetDrifted exports one kuik_image_drifted series per image of the resource (kind, name),
// images being origin references, and deletes the series of the images it no longer lists:
// deleting rather than zeroing resolves an alert at once.
func (e *Exporter) SetDrifted(kind, name string, images []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := resource{kind, name}
	current := map[string]bool{}
	for _, image := range images {
		current[image] = true
		e.drifted.WithLabelValues(kind, name, image, host(image)).Set(1)
	}
	for image := range e.images[key] {
		if !current[image] {
			e.drifted.DeleteLabelValues(kind, name, image, host(image))
		}
	}
	e.images[key] = current
}

// Forget deletes every series of the resource (kind, name).
func (e *Exporter) Forget(kind, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	labels := prometheus.Labels{labelKind: kind, labelName: name}
	e.tracked.DeletePartialMatch(labels)
	e.checked.DeletePartialMatch(labels)
	e.drifted.DeletePartialMatch(labels)
	delete(e.images, resource{kind, name})
}

// host is the registry host of a normalised image reference.
func host(image string) string {
	h, _, _ := strings.Cut(image, "/")
	return h
}
