package kuik

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath/plan"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/routing"
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

// sweep lists every repository of status.repositories, records in pendingDeletion the tags of
// this cluster the live references do not expect, deletes by tag those whose retention
// elapsed, and returns the new pendingDeletion and the repositories to retire. A listing that
// fails interrupts the pass, which records, deletes and retires nothing.
func (r *ImageMirrorReconciler) sweep(ctx context.Context, im *kuikv1alpha1.ImageMirror, st *mirrorState, live []imagepath.Reference, pending []kuikv1alpha1.PendingDeletion, now time.Time) ([]kuikv1alpha1.PendingDeletion, []string, error) {
	log := logf.FromContext(ctx)
	manage, err := r.manageAuth(ctx, im)
	if err != nil {
		return nil, nil, err
	}
	endpoint := func(ref string) registry.Endpoint {
		return registry.Endpoint{Reference: ref, Insecure: im.Spec.Destination.Insecure, Auth: manage(ref)}
	}
	listed := map[string][]string{}
	for _, repository := range im.Status.Repositories {
		tags, err := r.registry.ListTags(ctx, endpoint(repository))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errPassInterrupted, err)
		}
		listed[repository] = tags
	}

	retention := 7 * 24 * time.Hour
	if im.Spec.Cleanup != nil && im.Spec.Cleanup.Retention != nil {
		retention = im.Spec.Cleanup.Retention.Duration
	}
	result := plan.Sweep{
		ClusterID: r.config.Load().ClusterID,
		Path:      im.Spec.Destination.Path,
		Retention: retention,
		Live:      live,
		Listed:    listed,
		Pending:   pending,
		Now:       now,
	}.Run()

	known := map[string]bool{}
	for _, entry := range pending {
		known[entry.Ref] = true
	}
	for _, entry := range result.Pending {
		// The entry persists, so a restart or a leader change does not announce it again.
		if entry.Origin == "" && !known[entry.Ref] {
			r.recorder.Eventf(im, nil, corev1.EventTypeWarning, "OrphanTagFound", "Sweep",
				"Found %s, a tag of this cluster no tracked reference accounts for; it is deleted once its retention elapsed", entry.Ref)
		}
	}

	entries := make(map[string]kuikv1alpha1.PendingDeletion, len(result.Pending))
	for _, entry := range result.Pending {
		entries[entry.Ref] = entry
	}
	deleted := map[string]bool{}
	for _, ref := range result.Delete {
		err := r.registry.DeleteTag(ctx, endpoint(ref))
		switch {
		case err == nil:
			deleted[ref] = true
			r.metrics.deletedTag(routing.KindImageMirror, im.Name, entries[ref])
			st.mu.Lock()
			st.deleteUnsupported = false
			st.mu.Unlock()
			// Events expire: the log is what records every deletion.
			log.Info("Deleted ImageMirror tag", "image", ref, "reason", deletionReason(entries[ref]))
			r.recorder.Eventf(im, nil, corev1.EventTypeNormal, "ImageDeleted", "Delete", "Deleted %s, unused for longer than its retention", ref)
		case errors.Is(err, registry.ErrDeleteUnsupported):
			// The registry will keep refusing: stop there, the next pass tries once more.
			log.Info("Stopped deleting ImageMirror tags: the destination refuses tag deletion", "image", ref, "error", err.Error())
			st.mu.Lock()
			st.deleteUnsupported = true
			st.mu.Unlock()
		default:
			log.Info("Failed to delete ImageMirror tag", "image", ref, "error", err.Error())
			r.recorder.Eventf(im, nil, corev1.EventTypeWarning, "ImageDeletionFailed", "Delete", "Failed to delete %s: %v", ref, err)
		}
		if errors.Is(err, registry.ErrDeleteUnsupported) {
			break
		}
	}
	remaining := slices.DeleteFunc(result.Pending, func(e kuikv1alpha1.PendingDeletion) bool { return deleted[e.Ref] })
	return remaining, result.Retire, nil
}

// deletionReason is why a tag was deleted: Unused when a pod event recorded it with its
// origin, Orphan when the sweep found it with none.
func deletionReason(entry kuikv1alpha1.PendingDeletion) string {
	if entry.Origin == "" {
		return deletedOrphan
	}
	return deletedUnused
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
