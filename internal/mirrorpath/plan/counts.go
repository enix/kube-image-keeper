package plan

import (
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// Observed is what a mirror's status counts are computed from.
type Observed struct {
	// ClusterID and Path render the destination reference of a reference.
	ClusterID string
	Path      string
	// Live are the references the selected pods declare.
	Live []imagepath.Reference
	// Images are the live images of the containers of the selected pods, normalised.
	Images []string
	// SelfChecked are the references the last self-check found at the destination, keyed by
	// their String form.
	SelfChecked map[string]bool
	// Copied are the references copied since the last self-check, keyed the same way.
	Copied map[string]bool
	// Status is the mirror's status: pendingDeletion, failedImageCopies and driftedImages.
	Status kuikv1alpha1.ImageMirrorStatus
}

// Counts computes status.images.copy. See docs/v3/status.md, "ImageMirror".
func Counts(o Observed) kuikv1alpha1.CopyCounts {
	return kuikv1alpha1.CopyCounts{}
}

// CopyFailureEvent is one ImageCopyFailed event: a reason and the message naming what failed.
type CopyFailureEvent struct {
	Reason  kuikv1alpha1.CopyFailureReason
	Message string
}

// CopyFailureEvents coalesces the copy failures of one pass: one event per reason and host,
// naming a single image, or the count of images under their narrowest common prefix. See
// docs/v3/observability.md, "A copy failure is coalesced, never generalised".
func CopyFailureEvents(failed []kuikv1alpha1.FailedImageCopy) []CopyFailureEvent {
	return nil
}
