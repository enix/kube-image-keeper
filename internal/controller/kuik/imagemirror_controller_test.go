package kuik

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	kuikregistry "github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
	"github.com/enix/kube-image-keeper/internal/status/condition"
)

var _ = Describe("ImageMirror Controller", func() {
	var h *mirrorHarness
	BeforeEach(func() {
		h = newMirrorHarness()
	})

	withCredentials := func(manage, pull string) func(*kuikv1alpha1.ImageMirror) {
		return func(im *kuikv1alpha1.ImageMirror) {
			im.Spec.Destination.Manage = secretAuth(manage)
			im.Spec.Destination.Pull = secretAuth(pull)
		}
	}
	notReadyGauge := func(reason string) (float64, bool) {
		return gauge(h.metrics, "kuik_resource_not_ready", map[string]string{
			labelKind: routing.KindImageMirror, labelName: h.name, "reason": reason,
		})
	}

	Describe("Ready", func() {
		It("sets Ready True with reason IsReady for a mirror whose manage and pull secretRefs resolve", func() {
			manage, pull := unique("manage"), unique("pull")
			createSecret(installNamespace, manage, corev1.SecretTypeDockerConfigJson)
			createSecret(installNamespace, pull, corev1.SecretTypeDockerConfigJson)
			h.mirror(withCredentials(manage, pull))
			h.reconcile()
			Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(h.condition(kuikv1alpha1.ConditionReady).Reason).To(Equal(kuikv1alpha1.ReasonIsReady))
		})

		It("sets Ready False with reason SecretNotFound for a manage secretRef naming an absent Secret", func() {
			pull := unique("pull")
			createSecret(installNamespace, pull, corev1.SecretTypeDockerConfigJson)
			h.mirror(withCredentials(unique("missing"), pull))
			h.reconcile()
			Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionFalse))
			Expect(h.condition(kuikv1alpha1.ConditionReady).Reason).To(Equal(kuikv1alpha1.ReasonSecretNotFound))
		})

		It("sets Ready False with reason SecretMalformed for a pull secretRef naming a Secret that is not a dockerconfigjson", func() {
			manage, pull := unique("manage"), unique("opaque")
			createSecret(installNamespace, manage, corev1.SecretTypeDockerConfigJson)
			createSecret(installNamespace, pull, corev1.SecretTypeOpaque)
			h.mirror(withCredentials(manage, pull))
			h.reconcile()
			Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionFalse))
			Expect(h.condition(kuikv1alpha1.ConditionReady).Reason).To(Equal(kuikv1alpha1.ReasonSecretMalformed))
		})

		It("sets Ready False with reason InvalidConfig for a selector that does not parse", func() {
			h.mirror(func(im *kuikv1alpha1.ImageMirror) {
				im.Spec.PodSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: appContainer, Operator: "Bogus", Values: []string{"web"}},
				}}
			})
			h.reconcile()
			Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionFalse))
			Expect(h.condition(kuikv1alpha1.ConditionReady).Reason).To(Equal(kuikv1alpha1.ReasonInvalidConfig))
		})

		PIt("sets Ready False with reason RegistryDeleteUnsupported once the destination refuses a tag deletion, and stops deleting there", func() {})

		It("emits ResourceNotReady on the mirror and exports kuik_resource_not_ready while Ready is False", func() {
			h.mirror(withCredentials(unique("missing"), unique("missing")))
			h.reconcile()
			events := h.recorder.withReason(condition.EventResourceNotReady)
			Expect(events).To(HaveLen(1))
			Expect(events[0].regarding.(*kuikv1alpha1.ImageMirror).Name).To(Equal(h.name))
			v, ok := notReadyGauge(kuikv1alpha1.ReasonSecretNotFound)
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
		})

		It("sets Ready back to IsReady, emits ResourceReady and removes kuik_resource_not_ready once the cause is fixed", func() {
			manage, pull := unique("manage"), unique("pull")
			createSecret(installNamespace, pull, corev1.SecretTypeDockerConfigJson)
			h.mirror(withCredentials(manage, pull))
			h.reconcile()
			Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionFalse))

			createSecret(installNamespace, manage, corev1.SecretTypeDockerConfigJson)
			h.reconcile()
			Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(h.recorder.withReason(condition.EventResourceReady)).To(HaveLen(1))
			_, ok := notReadyGauge(kuikv1alpha1.ReasonSecretNotFound)
			Expect(ok).To(BeFalse())
		})
		PIt("leaves Ready True when the copy of one image fails", func() {})
	})

	Describe("the desired state", func() {
		PIt("copies the images of the live pods its podSelector and namespaceSelector select, and only those", func() {})
		PIt("lets a reference go once its pods are Succeeded or Failed, starting its retention", func() {})
		PIt("never copies the images of static pods", func() {})
		PIt("keeps the origin of a rewritten pod out of pendingDeletion while the pod runs the mirror copy", func() {})
	})

	Describe("copying", func() {
		PIt("copies a reference on a copy window of its origin host", func() {})
		PIt("records the repository in status.repositories before the first push into it", func() {})
		PIt("pushes the origin-derived tag of a tagged reference, plus the anchor of a pinned one, and the anchor alone without a tag", func() {})
		PIt("skips a reference whose repository is inventoried and which the destination holds", func() {})
		PIt("performs a re-copy before the initial copies still pending", func() {})
		PIt("emits ImageCopied on the first copy of an image", func() {})
		PIt("reaches the destination over plain HTTP only when destination.insecure is set", func() {})
	})

	Describe("choosing the source", func() {
		PIt("reads an ImageAlternative covering the image when the origin does not answer, for a first copy and a re-copy alike, and writes to the destination derived from the origin", func() {})
		PIt("reads a private alternative with its own credentials when the origin does not answer", func() {})
		PIt("skips the alternatives marked unavailable", func() {})
		PIt("tries last an origin matching an alternative marked unavailable", func() {})
		PIt("spends a copy window of the alternative's host, not the origin's, on a copy that alternative serves", func() {})
	})

	Describe("copy failures", func() {
		PIt("records the reason of a failed copy in failedImageCopies, since stamped once and lastAttempt refreshed on each retry", func() {})
		PIt("records SourceNotFound when no source answers, and emits ImageUnrecoverable", func() {})
		PIt("removes the failedImageCopies entry once the copy succeeds", func() {})
		PIt("emits ImageCopyFailed coalesced per reason over the failures of a pass", func() {})
		PIt("retries a failed copy on a later copy window", func() {})
	})

	Describe("the self-check", func() {
		PIt("runs a first pass at startup, then once per mirror.destinationScan.interval", func() {})
		PIt("stamps selfChecked at the end of a full pass and exports kuik_mirror_self_checked_timestamp_seconds", func() {})
		PIt("leaves selfChecked as it was when a pass is interrupted", func() {})
		PIt("re-copies a manifest missing from the destination and emits ImageRecopied", func() {})
		PIt("writes back a tag of this cluster missing while the manifest is present, without moving a blob", func() {})
		PIt("sets DestinationOutOfSync True with reason MissingImages while a desired reference is missing, and removes it once complete", func() {})
		PIt("exports kuik_registry_interval_seconds with operation Scan for its destination host, through the scheduler's collector", func() {})
	})

	Describe("drift", func() {
		PIt("re-reads no upstream tag and reports no checks.registries under driftPolicy Ignore", func() {})
		PIt("re-reads the copied tags of each source host on a ring of that host under Warn or Sync", func() {})
		PIt("puts no reference pinned without a tag on a ring, never in driftedImages nor drifted", func() {})
		PIt("persists the ring cursor in checks.registries and resumes from it", func() {})
		PIt("reports each ring's size, cycleStarted and cycleDuration in checks.registries and as the kuik_check_* series", func() {})
		PIt("reports a moved upstream digest in driftedImages and emits CopyOutOfDate under Warn, leaving the copy as it is", func() {})
		PIt("re-pushes the upstream's new digest, repoints the tag and emits ImageResynced under Sync", func() {})
		PIt("keeps the anchor of a pinned digest when Sync repoints its tag", func() {})
		PIt("removes the driftedImages entry once the copy matches the upstream again", func() {})
	})

	Describe("cleanup", func() {
		PIt("emits OrphanTagFound once when the sweep finds a tag of this cluster the desired state does not expect", func() {})
		PIt("removes a tag from the destination once its retention elapsed, leaving the other clusters' tags, and emits ImageDeleted", func() {})
		PIt("records no orphan, deletes nothing and retires no repository in a pass where the destination does not answer", func() {})
		PIt("re-copies a retained reference missing from the destination", func() {})
		PIt("emits ImageDeletionFailed when the destination refuses a deletion", func() {})
		PIt("deletes nothing and holds nothing in pendingDeletion when cleanup is disabled", func() {})
	})

	Describe("deleting the ImageMirror", func() {
		PIt("holds its finalizer while a pod still runs one of its destination references", func() {})
		PIt("deletes the tags of this cluster once no pod runs one of its destination references, then releases its finalizer", func() {})
		PIt("releases its finalizer without deleting anything when cleanup is disabled, once no pod runs one of its destination references", func() {})
		PIt("removes the series of a deleted mirror", func() {})
	})

	Describe("the routing side", func() {
		// routedPod is a pod in a selected namespace with one container the mirror rewrote and
		// one no candidate could serve.
		routedPod := func() {
			mirrored := h.destination.Host() + "/mirror/quay.io/thanos/thanos:v0.42.2_" + mirrorClusterID
			createPod(h.namespace(), unique("pod"),
				container{name: "thanos", image: mirrored, rewrite: &podrecord.Rewrite{
					By: routing.KindImageMirror + "/" + h.name, Origin: thanosImage, RewrittenTo: mirrored,
					Policy: string(kuikv1alpha1.RewritePolicyOnFailure),
				}},
				container{name: "gone", image: "quay.io/acme/gone:1.0", offering: []string{routing.KindImageMirror + "/" + h.name}},
			)
		}

		It("writes the pods and containers gauges, the anomaly lists and their conditions from the pods' annotations", func() {
			h.mirror()
			routedPod()
			h.reconcile()

			status := h.status()
			Expect(status.Pods).NotTo(BeNil())
			Expect(status.Pods.Tracked).To(Equal(int32(1)))
			Expect(status.Containers.Rewritten).To(Equal(int32(1)))
			Expect(status.NoAlternatives).To(HaveLen(1))
			Expect(h.condition(kuikv1alpha1.ConditionAlternativesExhausted).Status).To(Equal(metav1.ConditionTrue))
		})

		It("leaves the routing side empty under rewritePolicy None", func() {
			h.mirror(func(im *kuikv1alpha1.ImageMirror) { im.Spec.RewritePolicy = kuikv1alpha1.RewritePolicyNone })
			routedPod()
			h.reconcile()

			status := h.status()
			Expect(status.RoutingStatus).To(Equal(kuikv1alpha1.RoutingStatus{}))
			Expect(h.condition(kuikv1alpha1.ConditionAlternativesExhausted)).To(BeNil())
		})
	})

	Describe("metrics", func() {
		PIt("exports the images.copy gauges as kuik_images_tracked, kuik_images_checked and kuik_mirror_tags_orphan", func() {})
		PIt("exports kuik_image_copy_failed for each failing copy, its registry label naming the side that failed, and drops it once the copy succeeds", func() {})
		PIt("exports kuik_image_drifted for each driftedImages entry, and drops it once the copy matches again", func() {})
		PIt("counts kuik_mirror_copies_total by Initial, Recopy and Resync", func() {})
		PIt("counts kuik_mirror_tags_deleted_total by Unused and Orphan", func() {})
		PIt("observes kuik_mirror_copy_duration_seconds only when metrics.copyDuration is enabled", func() {})
	})

	Describe("bounded lists", func() {
		PIt("caps failedImageCopies and driftedImages over 500 entries, records it in truncated and sets ListCapacityPressure", func() {})
		PIt("never caps repositories, pendingDeletion or checks.registries", func() {})
	})

	Describe("writing the status", func() {
		It("writes no status before the lease is held, and writes it once the lease is held", func() {
			fresh, err := NewImageMirrorReconciler(k8sClient, k8sClient.Scheme(), ImageMirrorOptions{
				APIReader: k8sClient, ClusterResourceNamespace: installNamespace, Recorder: h.recorder,
				Registerer: prometheus.NewRegistry(), Scheduler: h.scheduler, Registry: kuikregistry.NewClient(),
				Config: h.config, Clock: h.clock,
			})
			Expect(err).NotTo(HaveOccurred())
			h.mirror()
			request := reconcile.Request{NamespacedName: types.NamespacedName{Name: h.name}}

			result, err := fresh.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(h.condition(kuikv1alpha1.ConditionReady)).To(BeNil())

			fresh.Elected(h.clock.Now())
			_, err = fresh.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(h.condition(kuikv1alpha1.ConditionReady)).NotTo(BeNil())
		})

		It("skips the write when the status did not change", func() {
			h.mirror()
			h.reconcile()
			var before kuikv1alpha1.ImageMirror
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: h.name}, &before)).To(Succeed())

			h.reconcile()
			var after kuikv1alpha1.ImageMirror
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: h.name}, &after)).To(Succeed())
			Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
		})
	})

	Describe("with a manager", func() {
		PIt("reports again after the debounce when a selected pod is created or deleted", func() {})
		PIt("reports again after the debounce when its spec changes", func() {})
	})
})
