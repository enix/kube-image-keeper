package kuik

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// BackfillOriginalField ensures MonitoredImage.Original is set for pre-existing
// CISA status entries that were created before the Original field was introduced.
// It runs once at manager startup.
func (r *ClusterImageSetAvailabilityReconciler) BackfillOriginalField(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("backfill-original")

	var cisaList kuikv1alpha1.ClusterImageSetAvailabilityList
	if err := r.List(ctx, &cisaList); err != nil {
		return err
	}

	if len(cisaList.Items) == 0 {
		return nil
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return err
	}

	for i := range cisaList.Items {
		cisa := &cisaList.Items[i]

		needsBackfill := map[string]struct{}{}
		for _, image := range cisa.Status.Images {
			if !image.Original {
				needsBackfill[image.Image] = struct{}{}
			}
		}
		if len(needsBackfill) == 0 {
			continue
		}

		currentImages := map[string]bool{}
		for j := range pods.Items {
			for imageName, original := range normalizedImageNamesMapFromAnnotatedPod(ctx, &pods.Items[j]) {
				if _, exists := needsBackfill[imageName]; exists {
					currentImages[imageName] = currentImages[imageName] || original
				}
			}
		}

		original := cisa.DeepCopy()
		changed := false
		for j := range cisa.Status.Images {
			image := &cisa.Status.Images[j]
			if !image.Original {
				if isOriginal := currentImages[image.Image]; isOriginal {
					image.Original = true
					changed = true
					log.V(1).Info("backfilled original field", "cisa", cisa.Name, "image", image.Image)
				}
			}
		}

		if changed {
			if err := r.Status().Patch(ctx, cisa, client.MergeFrom(original)); err != nil {
				log.Error(err, "failed to update CISA status after backfill", "cisa", cisa.Name)
				continue
			}
			log.Info("backfilled original field for CISA", "cisa", cisa.Name)
		}
	}

	return nil
}
