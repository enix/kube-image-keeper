package kuik

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// errPassInterrupted ends a destination pass that could not read the whole destination.
var errPassInterrupted = errors.New("destination pass interrupted")

// passDue reports whether the destination pass of st is due at now, and when the next one is.
// The first pass runs at once: it is what covers a copy recorded but never made.
func (r *ImageMirrorReconciler) passDue(st *mirrorState, now time.Time) (bool, time.Duration) {
	interval := r.config.Load().Mirror.DestinationScan.Interval.Duration
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastPass.IsZero() {
		return true, interval
	}
	if elapsed := now.Sub(st.lastPass); elapsed < interval {
		return false, interval - elapsed
	}
	return true, interval
}

// selfCheck compares the whole desired state of im with its destination, one manifest HEAD per
// reference, and records what it found: present references, lost ones to copy again first,
// and the ones whose tag only is gone, written back without a blob. A destination that does
// not answer interrupts the pass, which then changes nothing. It returns when the pass
// ended, or errPassInterrupted.
func (r *ImageMirrorReconciler) selfCheck(ctx context.Context, im *kuikv1alpha1.ImageMirror, st *mirrorState, desired []imagepath.Reference) (time.Time, error) {
	now := r.clock.Now()
	manage, err := r.manageAuth(ctx, im)
	if err != nil {
		return now, err
	}
	clusterID := r.config.Load().ClusterID
	endpoint := func(ref string) registry.Endpoint {
		return registry.Endpoint{Reference: ref, Insecure: im.Spec.Destination.Insecure, Auth: manage(ref)}
	}

	st.mu.Lock()
	known := map[string]string{}
	maps.Copy(known, st.digests)
	st.mu.Unlock()

	present := map[string]bool{}
	digests := map[string]string{}
	recopy := map[string]bool{}
	writeBack := map[string]bool{}
	for _, ref := range desired {
		key := ref.String()
		destination := mirrorpath.Destination(im.Spec.Destination.Path, ref, clusterID)
		result, err := r.registry.CheckDestination(ctx, endpoint(destination))
		if err == nil {
			present[key] = true
			digests[key] = result.Descriptor.Digest.String()
			continue
		}
		if !manifestNotFound(err) {
			return now, fmt.Errorf("%w: %w", errPassInterrupted, err)
		}
		digest, ok := known[key]
		if !ok {
			// Never seen at the destination by this process: an initial copy.
			continue
		}
		if _, err := r.registry.CheckDestination(ctx, endpoint(destinationRepository(im, ref, clusterID)+"@"+digest)); err == nil {
			writeBack[key] = true
		} else if manifestNotFound(err) {
			recopy[key] = true
		} else {
			return now, fmt.Errorf("%w: %w", errPassInterrupted, err)
		}
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	st.present = present
	// What was copied since the last pass is in present now, or lost again.
	st.copied = map[string]bool{}
	maps.Copy(st.digests, digests)
	st.recopy = recopy
	st.writeBack = writeBack
	st.lastPass = now
	return now, nil
}

func manifestNotFound(err error) bool {
	var checkErr *registry.CheckError
	return errors.As(err, &checkErr) && checkErr.Reason == kuikv1alpha1.CheckManifestNotFound
}

// destinationRepository is the destination repository of im for the origin ref: a reference
// with neither tag nor digest renders as its repository.
func destinationRepository(im *kuikv1alpha1.ImageMirror, ref imagepath.Reference, clusterID string) string {
	return mirrorpath.Destination(im.Spec.Destination.Path, imagepath.Reference{Host: ref.Host, Segments: ref.Segments}, clusterID)
}

// destinationHost is the registry host of destination.path.
func destinationHost(path string) string {
	host, _, _ := strings.Cut(path, "/")
	return host
}
