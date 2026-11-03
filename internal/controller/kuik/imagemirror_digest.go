package kuik

import (
	"context"
	"errors"
	"maps"
	"slices"
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
// spec says now, as unsupported is not ready, nil when it did not. The hint to delete by digest
// goes to a mirror deleting by tag.
func refusedSweep(im *kuikv1alpha1.ImageMirror, st *mirrorState) *condition.NotReady {
	byDigest := im.Spec.Cleanup.DeletesByDigest()
	if !st.refusesDeletion(byDigest) {
		return nil
	}
	if byDigest {
		return &condition.NotReady{
			Reason:  kuikv1alpha1.ReasonRegistryDeleteUnsupported,
			Message: "the destination refuses manifest deletion: cleanup cannot make progress",
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

// deleteUnsupported reports whether err is the destination refusing a deletion, by tag or by
// digest, as unsupported: it will keep refusing.
func deleteUnsupported(err error) bool {
	return errors.Is(err, registry.ErrDeleteUnsupported) || errors.Is(err, registry.ErrManifestDeleteUnsupported)
}
