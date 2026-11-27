package kuik

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/status/condition"
)

// deleteByDigestHint points a mirror whose destination refuses tag deletion to the opt-in
// that deletes by manifest instead, with what makes it unsafe.
const deleteByDigestHint = "cleanup.deleteByDigest: true deletes by manifest instead, when this ImageMirror is the only writer of its destination"

// manifestRefusal starts the Ready message of a mirror whose destination refused a deletion by
// digest: what tells a restart which way the refused deletion was made.
const manifestRefusal = "the destination refuses manifest deletion"

// digestDeletion is how deleting one tag by the manifest it points at ended.
type digestDeletion struct {
	// manifest is the digest reference deleted, empty when nothing was.
	manifest string
	// keptBy is the tag of the guard sharing the manifest, which kept the tag.
	keptBy string
}

// deleteByDigest deletes the destination tag ref under cleanup.deleteByDigest: it reads the
// digest ref points at, keeps ref when a tag of guard points at it too, as guards reads it, and
// otherwise deletes that manifest, every tag pointing at it with it. A tag already gone counts
// as deleted, with nothing to delete. Every request goes to the destination, through endpoint.
func (r *ImageMirrorReconciler) deleteByDigest(ctx context.Context, endpoint func(string) registry.Endpoint, ref string, guard []string, guards *digestReader) (digestDeletion, error) {
	digest, found, err := r.registry.ManifestDigest(ctx, endpoint(ref))
	if err != nil || !found {
		return digestDeletion{}, err
	}
	for _, other := range guard {
		shared, err := guards.digest(ctx, other)
		if err != nil {
			return digestDeletion{}, err
		}
		if shared == digest.String() {
			return digestDeletion{keptBy: other}, nil
		}
	}
	manifest := ref[:strings.LastIndex(ref, ":")] + "@" + digest.String()
	if err := r.registry.DeleteManifest(ctx, endpoint(manifest)); err != nil {
		return digestDeletion{}, err
	}
	return digestDeletion{manifest: manifest}, nil
}

// refusedSweep is why a mirror whose destination refused its last deletion, made the way its
// spec says now, in a way that lasts is not ready, nil when it did not. The hint to delete by
// digest goes to a mirror deleting by tag on a destination that does not support it: deleting
// by digest needs the same right as deleting by tag.
func refusedSweep(im *kuikv1alpha1.ImageMirror, st *mirrorState) *condition.NotReady {
	byDigest := im.Spec.Cleanup.DeletesByDigest()
	refusal, refusing := st.refusesDeletion(byDigest)
	if !refusing {
		return nil
	}
	if refusal.unauthorized {
		return &condition.NotReady{
			Reason:  kuikv1alpha1.ReasonRegistryDeleteUnauthorized,
			Message: refusal.unauthorizedMessage(byDigest, "set cleanup.enabled: false"),
		}
	}
	if byDigest {
		return &condition.NotReady{
			Reason:  kuikv1alpha1.ReasonRegistryDeleteUnsupported,
			Message: manifestRefusal + ": cleanup cannot make progress",
		}
	}
	return &condition.NotReady{
		Reason:  kuikv1alpha1.ReasonRegistryDeleteUnsupported,
		Message: "the destination refuses tag deletion: cleanup cannot make progress; " + deleteByDigestHint,
	}
}

// digestReader reads the digest a destination tag points at, for the guards of one sweep: a
// tag whose digest the mirror knows is not read, and a tag read is read once per sweep.
type digestReader struct {
	r        *ImageMirrorReconciler
	endpoint func(string) registry.Endpoint
	// known are the digests the expected tags point at, or will once copied, as the last pass
	// or copy saw their references (see knownDigests).
	known map[string]string
	// read are the digests read so far, empty for a tag the destination does not hold.
	read map[string]string
}

// digest returns the digest the tag ref points at, empty when the destination does not hold
// it. A failed read is not kept.
func (d *digestReader) digest(ctx context.Context, ref string) (string, error) {
	if digest, ok := d.known[ref]; ok {
		return digest, nil
	}
	if digest, ok := d.read[ref]; ok {
		return digest, nil
	}
	digest, found, err := d.r.registry.ManifestDigest(ctx, d.endpoint(ref))
	if err != nil {
		return "", err
	}
	d.read[ref] = ""
	if found {
		d.read[ref] = digest.String()
	}
	return d.read[ref], nil
}

// retained are live plus the origins pending records, whose tags wait out their retention.
func retained(live []imagepath.Reference, pending []kuikv1alpha1.PendingDeletion) []imagepath.Reference {
	refs := slices.Clone(live)
	for _, entry := range pending {
		if ref, err := imagepath.Parse(entry.Origin); entry.Origin != "" && err == nil {
			refs = append(refs, ref)
		}
	}
	return refs
}

// knownDigests maps the destination tags of refs to the digest the mirror expects each one to
// point at: the digest the last pass or copy saw its reference at. The self-check of the same
// pass reads every reference whose manifest the destination holds, a pinned one by its digest,
// so a tag that could share a manifest with a due tag is known. A tag two references expect
// at different digests (app:v1 and app:v1@<older digest>) is left out, as is one a reference
// of unknown digest expects too: the guard reads them. st.mu is not held.
func (r *ImageMirrorReconciler) knownDigests(im *kuikv1alpha1.ImageMirror, st *mirrorState, refs []imagepath.Reference) map[string]string {
	st.mu.Lock()
	defer st.mu.Unlock()
	known := map[string]string{}
	unsure := map[string]bool{}
	for _, ref := range refs {
		digest := st.digests[ref.String()]
		repository := destinationRepository(im, ref, r.clusterID)
		for _, tag := range mirrorpath.Tags(ref, r.clusterID) {
			key := repository + ":" + tag
			if previous, ok := known[key]; digest == "" || (ok && previous != digest) {
				unsure[key] = true
			}
			if digest != "" {
				known[key] = digest
			}
		}
	}
	maps.DeleteFunc(known, func(key, _ string) bool { return unsure[key] })
	return known
}

// deletionRefusal is how the destination refused a deletion, by tag or by digest, in a way that
// lasts: it will keep refusing until an operator acts.
type deletionRefusal struct {
	// unauthorized is set when the destination answered 401 or 403, and unset when it does not
	// support the deletion (400, 405, 501).
	unauthorized bool
	// status is the HTTP status of the answer, 0 when unknown.
	status int
	// message is the registry's own error message. It goes in the event only: it may change
	// from a try to the next, and Ready would be rewritten each time.
	message string
}

// registryMessageLimit is how much of the registry's error message an event carries: the API
// server refuses an event whose note is longer than 1024 bytes.
const registryMessageLimit = 256

// refusedDeletion reports whether err is the destination refusing a deletion in a way that
// lasts, and how. A deletion tried with several credentials may be refused both ways:
// unsupported wins, since no credential makes a registry delete what it does not support.
func refusedDeletion(err error) (deletionRefusal, bool) {
	kinds := []error{registry.ErrDeleteUnsupported, registry.ErrManifestDeleteUnsupported}
	unsupported := errors.Is(err, kinds[0]) || errors.Is(err, kinds[1])
	if !unsupported {
		if !errors.Is(err, registry.ErrDeleteUnauthorized) {
			return deletionRefusal{}, false
		}
		kinds = []error{registry.ErrDeleteUnauthorized}
	}
	refusal := deletionRefusal{unauthorized: !unsupported}
	for _, kind := range kinds {
		if detail, ok := registry.RefusalDetail(err, kind); ok {
			refusal.status, refusal.message = detail.StatusCode, detail.Message
			break
		}
	}
	return refusal, true
}

// httpStatus is the HTTP status of the refusal as the messages give it, empty when unknown.
func (d deletionRefusal) httpStatus() string {
	if d.status == 0 {
		return ""
	}
	return fmt.Sprintf(" (HTTP %d)", d.status)
}

// note is the note of the ImageDeletionFailed event of the refused deletion of ref: the HTTP
// status and what the registry answered. A 403 is not always a matter of credential (an
// immutable tag, a retention policy), so the note does not assert it.
func (d deletionRefusal) note(ref string) string {
	note := fmt.Sprintf("Failed to delete %s: the destination does not support the deletion%s", ref, d.httpStatus())
	if d.unauthorized {
		note = fmt.Sprintf("Failed to delete %s: the destination refuses the deletion%s; the manage credential may lack the right to delete", ref, d.httpStatus())
	}
	if d.message == "" {
		return note
	}
	message := d.message
	if len(message) > registryMessageLimit {
		message = strings.ToValidUTF8(message[:registryMessageLimit], "") + "..."
	}
	return note + ". The registry answered: " + message
}

// unauthorizedMessage is the Ready message of a mirror whose destination refuses the credential
// of a deletion, by tag or by digest, with the HTTP status it answered; otherwise is what the
// operator can do instead of granting the right. It starts as the message of an unsupported
// deletion does, which tells a restart the way the deletion was made.
func (d deletionRefusal) unauthorizedMessage(byDigest bool, otherwise string) string {
	refuses := "the destination refuses tag deletion"
	if byDigest {
		refuses = manifestRefusal
	}
	return refuses + d.httpStatus() + ": cleanup cannot make progress; the manage credential may lack the right to delete: grant it, or " + otherwise
}

// refusedStatus reads back the HTTP status a Ready message built by unauthorizedMessage holds,
// 0 when it holds none.
func refusedStatus(message string) int {
	_, after, _ := strings.Cut(message, "(HTTP ")
	code, _, _ := strings.Cut(after, ")")
	status, _ := strconv.Atoi(code)
	return status
}
