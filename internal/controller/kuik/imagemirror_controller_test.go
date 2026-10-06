package kuik

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	kuikregistry "github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
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
		Context("with a destination credential that cannot be read", func() {
			var src *registrytest.Registry
			BeforeEach(func() {
				src = h.source()
				src.Push("acme/app:v1", registrytest.Image())
				src.Reset()
				createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			})

			It("keeps copying while only the pull secretRef cannot be read", func() {
				manage := unique("manage")
				createSecret(installNamespace, manage, corev1.SecretTypeDockerConfigJson)
				h.mirror(withCredentials(manage, unique("missing")))
				h.reconcile()
				h.window()

				Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
				Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionFalse))
			})

			It("copies nothing while the manage secretRef cannot be read", func() {
				pull := unique("pull")
				createSecret(installNamespace, pull, corev1.SecretTypeDockerConfigJson)
				h.mirror(withCredentials(unique("missing"), pull))
				h.reconcile()
				h.window()

				Consistently(func() []registrytest.Request { return src.Requests("", "/manifests/") }).Should(BeEmpty())
				Expect(h.copied(destinationTag(src, "acme/app"))()).NotTo(Succeed())
			})
		})

		It("leaves Ready True when the copy of one image fails", func() {
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			rejectPushes(h.destination)
			h.mirror()
			h.reconcile()
			h.window()

			Eventually(func() []kuikv1alpha1.FailedImageCopy {
				h.reconcile()
				return h.status().FailedImageCopies
			}).Should(HaveLen(1))
			Expect(h.condition(kuikv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Describe("the desired state", func() {
		It("copies the images of the live pods its podSelector and namespaceSelector select, and only those", func() {
			src := h.source()
			for _, repository := range []string{"acme/app", "acme/other", "acme/done"} {
				src.Push(repository+":v1", registrytest.Image())
			}
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			unselected := unique("ns")
			createNamespace(unselected, nil)
			createPod(unselected, unique("pod"), container{name: appContainer, image: src.Host() + "/acme/other:v1"})
			done := createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/done:v1"})
			setPhase(done, corev1.PodSucceeded)
			h.mirror()

			for range 3 {
				h.reconcile()
				h.window()
			}
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
			Consistently(h.copied(destinationTag(src, "acme/other"))).ShouldNot(Succeed())
			Expect(h.copied(destinationTag(src, "acme/done"))()).NotTo(Succeed())
		})

		PIt("lets a reference go once its pods are Succeeded or Failed, starting its retention", func() {})

		It("never copies the images of static pods", func() {
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			src.Push("acme/static:v1", registrytest.Image())
			ns := h.namespace()
			createPod(ns, unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			staticPod(ns, src.Host()+"/acme/static:v1")
			h.mirror()

			for range 2 {
				h.reconcile()
				h.window()
			}
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
			Consistently(h.copied(destinationTag(src, "acme/static"))).ShouldNot(Succeed())
		})
		PIt("keeps the origin of a rewritten pod out of pendingDeletion while the pod runs the mirror copy", func() {})
	})

	Describe("copying", func() {
		It("copies a reference on a copy window of its origin host", func() {
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			h.mirror()
			h.reconcile()

			Consistently(h.copied(destinationTag(src, "acme/app"))).ShouldNot(Succeed())
			h.window()
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
		})

		It("writes to a destination that requires its manage credential", func() {
			h.destination = h.source(registrytest.WithBasicAuth("writer", "s3cr3t"))
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			h.mirror(func(im *kuikv1alpha1.ImageMirror) {
				im.Spec.Destination.Manage = &kuikv1alpha1.DestinationCredentials{Auth: kuikv1alpha1.Auth{
					SecretRef: &kuikv1alpha1.SecretReference{Name: createCredentials(h.destination.Host(), "writer", "s3cr3t")},
				}}
			})
			h.reconcile()

			h.window()
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
		})

		It("records the repository in status.repositories before the first push into it", func() {
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			rejectPushes(h.destination)
			h.mirror()
			h.reconcile()
			h.window()

			Eventually(func() []string { return h.status().Repositories }).Should(ContainElement(h.destinationRepository(src, "acme/app")))
			Expect(h.copied(destinationTag(src, "acme/app"))()).NotTo(Succeed())
		})

		It("pushes the origin-derived tag of a tagged reference, plus the anchor of a pinned one, and the anchor alone without a tag", func() {
			src := h.source()
			app := src.Push("acme/app:v1", registrytest.Image())
			tool := src.Push("acme/tool:v2", registrytest.Image())
			createPod(h.namespace(), unique("pod"),
				container{name: "tagged", image: src.Host() + "/acme/app:v1"},
				container{name: "pinned", image: src.Host() + "/acme/app:v1@" + app.String()},
				container{name: "digest", image: src.Host() + "/acme/tool@" + tool.String()},
			)
			h.mirror()
			// A window opening while the previous copy still runs is lost: each copy writes
			// its manifest before the next window.
			for copies := range 3 {
				h.reconcile()
				h.window()
				Eventually(func() int {
					return len(h.destination.Requests(http.MethodPut, "/manifests/"))
				}).Should(BeNumerically(">", copies))
			}

			anchor := func(d v1.Hash) string { return "sha256-" + d.Hex + "_" + mirrorClusterID }
			tags := func(repository string) func() []string {
				return func() []string {
					listed, _ := kuikregistry.NewClient().ListTags(ctx, kuikregistry.Endpoint{
						Reference: h.destinationRepository(src, repository), Insecure: true,
					})
					return listed
				}
			}
			Eventually(tags("acme/app")).Should(ConsistOf("v1_"+mirrorClusterID, anchor(app)))
			Eventually(tags("acme/tool")).Should(ConsistOf(anchor(tool)))
		})
		It("skips a reference whose repository is inventoried and which the destination holds", func() {
			src := h.source()
			image := registrytest.Image()
			src.Push("acme/app:v1", image)
			h.destination.Push(destinationTag(src, "acme/app"), image)
			src.Reset()
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			h.mirror()
			var im kuikv1alpha1.ImageMirror
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: h.name}, &im)).To(Succeed())
			im.Status.Repositories = []string{h.destinationRepository(src, "acme/app")}
			Expect(k8sClient.Status().Update(ctx, &im)).To(Succeed())

			h.reconcile()
			h.window()
			Consistently(func() []registrytest.Request { return src.Requests("", "/manifests/") }).Should(BeEmpty())
		})

		It("performs a re-copy before the initial copies still pending", func() {
			src := h.source()
			src.Push("acme/zzz:v1", registrytest.Image())
			src.Push("acme/aaa:v1", registrytest.Image())
			ns := h.namespace()
			createPod(ns, unique("pod"), container{name: appContainer, image: src.Host() + "/acme/zzz:v1"})
			h.mirror()
			h.reconcile()
			h.window()
			Eventually(h.copied(destinationTag(src, "acme/zzz"))).Should(Succeed())
			h.reconcile()

			h.deleteManifest(destinationTag(src, "acme/zzz"))
			createPod(ns, unique("pod"), container{name: appContainer, image: src.Host() + "/acme/aaa:v1"})
			h.pass()
			h.reconcile()
			h.window()
			Eventually(h.copied(destinationTag(src, "acme/zzz"))).Should(Succeed())
			Expect(h.copied(destinationTag(src, "acme/aaa"))()).NotTo(Succeed())
		})
		It("emits ImageCopied on the first copy of an image", func() {
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			h.mirror()
			h.reconcile()
			h.window()

			Eventually(func() []recordedEvent { return h.recorder.withReason("ImageCopied") }).Should(HaveLen(1))
			event := h.recorder.withReason("ImageCopied")[0]
			Expect(event.regarding.(*kuikv1alpha1.ImageMirror).Name).To(Equal(h.name))
			Expect(event.eventType).To(Equal(corev1.EventTypeNormal))
		})
	})

	Describe("choosing the source", func() {
		It("reads an ImageAlternative covering the image when the origin does not answer, for a first copy and a re-copy alike, and writes to the destination derived from the origin", func() {
			origin, alternative := h.source(), h.source()
			alternative.Push("acme/app:v1", registrytest.Image())
			h.alternativeTo(appEntry(origin), appEntry(alternative))
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: origin.Host() + "/acme/app:v1"})
			h.mirror()

			h.fallBack(alternative)
			h.window()
			Eventually(h.copied(destinationTag(origin, "acme/app"))).Should(Succeed())
			h.reconcile()

			h.deleteManifest(destinationTag(origin, "acme/app"))
			h.pass()
			h.fallBack(alternative)
			h.window()
			Eventually(h.copied(destinationTag(origin, "acme/app"))).Should(Succeed())
		})
		It("reads a private alternative with its own credentials when the origin does not answer", func() {
			origin := h.source()
			private := h.source(registrytest.WithBasicAuth("mirror", "s3cr3t"))
			private.Push("acme/app:v1", registrytest.Image())
			entry := appEntry(private)
			entry.Auth = &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: createCredentials(private.Host(), "mirror", "s3cr3t")}}
			h.alternativeTo(appEntry(origin), entry)
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: origin.Host() + "/acme/app:v1"})
			h.mirror()

			h.fallBack(private)
			h.window()
			Eventually(h.copied(destinationTag(origin, "acme/app"))).Should(Succeed())
		})

		It("skips the alternatives marked unavailable", func() {
			origin, dead, alive := h.source(), h.source(), h.source()
			dead.Push("acme/app:v1", registrytest.Image())
			dead.Reset()
			alive.Push("acme/app:v1", registrytest.Image())
			marked := appEntry(dead)
			marked.Unavailable = true
			h.alternativeTo(appEntry(origin), marked, appEntry(alive))
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: origin.Host() + "/acme/app:v1"})
			h.mirror()

			h.fallBack(alive)
			h.window()
			Eventually(h.copied(destinationTag(origin, "acme/app"))).Should(Succeed())
			Expect(dead.Requests("", "/manifests/")).To(BeEmpty())
		})

		It("tries last an origin matching an alternative marked unavailable", func() {
			origin, alive := h.source(), h.source()
			origin.Push("acme/app:v1", registrytest.Image())
			origin.Reset()
			alive.Push("acme/app:v1", registrytest.Image())
			marked := appEntry(origin)
			marked.Unavailable = true
			h.alternativeTo(marked, appEntry(alive))
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: origin.Host() + "/acme/app:v1"})
			h.mirror()

			for range 2 {
				h.reconcile()
				h.window()
			}
			Eventually(h.copied(destinationTag(origin, "acme/app"))).Should(Succeed())
			Expect(origin.Requests("", "/manifests/")).To(BeEmpty())
		})

		It("spends a copy window of the alternative's host, not the origin's, on a copy that alternative serves", func() {
			origin, alternative := h.source(), h.source()
			alternative.Push("acme/app:v1", registrytest.Image())
			slow := mirrorConfig()
			slow.Registries.Hosts = map[string]config.RegistryPacing{alternative.Host(): {Copy: &config.Window{
				Interval: &config.Duration{Duration: 5 * time.Minute}, Timeout: &config.Duration{},
			}}}
			h.scheduler.SetConfig(slow)
			h.alternativeTo(appEntry(origin), appEntry(alternative))
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: origin.Host() + "/acme/app:v1"})
			h.mirror()

			h.fallBack(alternative)
			h.clock.Step(3 * time.Minute)
			Consistently(h.copied(destinationTag(origin, "acme/app"))).ShouldNot(Succeed())
			h.window()
			Eventually(h.copied(destinationTag(origin, "acme/app"))).Should(Succeed())
		})
	})

	Describe("copy failures", func() {
		var src *registrytest.Registry
		// failingCopy makes the copy of src's acme/app:v1 fail at its first window.
		failingCopy := func() {
			src = h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			rejectPushes(h.destination)
			h.mirror()
			h.reconcile()
			h.window()
		}
		failures := func() []kuikv1alpha1.FailedImageCopy {
			h.reconcile()
			return h.status().FailedImageCopies
		}

		It("records the reason of a failed copy in failedImageCopies, since stamped once and lastAttempt refreshed on each retry", func() {
			failingCopy()
			Eventually(failures).Should(ConsistOf(HaveField("Reason", kuikv1alpha1.CopyPushRejected)))
			first := h.status().FailedImageCopies[0]

			h.window()
			Eventually(func() time.Time {
				return failures()[0].LastAttempt.Time
			}).Should(BeTemporally(">", first.LastAttempt.Time))
			Expect(h.status().FailedImageCopies[0].Since).To(Equal(first.Since))
			Expect(first.Ref).To(Equal(src.Host() + "/acme/app:v1"))
		})

		It("records SourceNotFound when no source answers, and emits ImageUnrecoverable", func() {
			gone := h.source()
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: gone.Host() + "/acme/gone:v1"})
			h.mirror()
			h.reconcile()
			h.window()

			Eventually(failures).Should(ConsistOf(HaveField("Reason", kuikv1alpha1.CopySourceNotFound)))
			events := h.recorder.withReason("ImageUnrecoverable")
			Expect(events).To(HaveLen(1))
			Expect(events[0].eventType).To(Equal(corev1.EventTypeWarning))
		})

		It("removes the failedImageCopies entry once the copy succeeds", func() {
			failingCopy()
			Eventually(failures).Should(HaveLen(1))

			acceptPushes(h.destination)
			h.window()
			Eventually(failures).Should(BeEmpty())
		})

		It("emits ImageCopyFailed coalesced per reason over the failures of a pass", func() {
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			src.Push("acme/tool:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"),
				container{name: "app", image: src.Host() + "/acme/app:v1"},
				container{name: "tool", image: src.Host() + "/acme/tool:v1"},
			)
			rejectPushes(h.destination)
			h.mirror()
			h.reconcile()
			// One copy per window, and a window opening while a copy still runs is lost: open
			// windows until both images failed, with no reconcile in between.
			pushed := func(repository string) bool {
				return len(h.destination.Requests(http.MethodPut, "/acme/"+repository+"/manifests/")) > 0
			}
			Eventually(func() bool {
				h.window()
				return pushed("app") && pushed("tool")
			}).Should(BeTrue())

			h.reconcile()
			events := h.recorder.withReason("ImageCopyFailed")
			Expect(events).To(HaveLen(1))
			Expect(events[0].note).To(ContainSubstring("2 images under " + src.Host() + "/acme/"))
		})

		It("retries a failed copy on a later copy window", func() {
			failingCopy()
			Eventually(failures).Should(HaveLen(1))

			acceptPushes(h.destination)
			h.window()
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
		})
	})

	Describe("the self-check", func() {
		var src *registrytest.Registry
		// copiedApp copies src's acme/app:v1 to the destination through one window.
		copiedApp := func() {
			GinkgoHelper()
			src = h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			h.mirror()
			h.reconcile()
			h.window()
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
		}
		destinationHeads := func() int {
			return len(h.destination.Requests(http.MethodHead, "/manifests/"))
		}

		It("runs a first pass at startup, then once per mirror.destinationScan.interval", func() {
			src := h.source()
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			h.mirror()
			h.reconcile()
			Expect(destinationHeads()).To(BeNumerically(">", 0))
			h.destination.Reset()

			h.reconcile()
			Expect(destinationHeads()).To(BeZero())
			h.pass()
			h.reconcile()
			Expect(destinationHeads()).To(BeNumerically(">", 0))
		})

		It("stamps selfChecked at the end of a full pass and exports kuik_mirror_self_checked_timestamp_seconds", func() {
			h.mirror()
			h.reconcile()

			Expect(h.status().SelfChecked).NotTo(BeNil())
			Expect(h.status().SelfChecked.Time).To(BeTemporally("~", h.clock.Now(), time.Second))
			v, ok := gauge(h.metrics, "kuik_mirror_self_checked_timestamp_seconds", map[string]string{labelKind: routing.KindImageMirror, labelName: h.name})
			Expect(ok).To(BeTrue())
			Expect(v).To(BeNumerically("~", float64(h.clock.Now().Unix()), 1))
		})

		It("leaves selfChecked as it was when a pass is interrupted", func() {
			copiedApp()
			h.reconcile()
			before := h.status().SelfChecked

			h.destination.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusInternalServerError, nil))
			h.pass()
			h.reconcile()
			Expect(h.status().SelfChecked).To(Equal(before))
		})

		It("re-copies a manifest missing from the destination and emits ImageRecopied", func() {
			copiedApp()
			h.reconcile()
			h.deleteManifest(destinationTag(src, "acme/app"))

			h.pass()
			h.reconcile()
			h.window()
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
			// The copy writes its tags before it announces them.
			Eventually(func() []recordedEvent { return h.recorder.withReason("ImageRecopied") }).Should(HaveLen(1))
			Expect(h.recorder.withReason("ImageRecopied")[0].eventType).To(Equal(corev1.EventTypeWarning))
		})

		It("writes back a tag of this cluster missing while the manifest is present, without moving a blob", func() {
			copiedApp()
			h.reconcile()
			h.deleteTag(destinationTag(src, "acme/app"))
			h.destination.Reset()

			h.pass()
			h.reconcile()
			h.window()
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
			Expect(h.destination.Requests(http.MethodPost, "/blobs/uploads/")).To(BeEmpty())
			Expect(h.recorder.withReason("ImageRecopied")).To(BeEmpty())
		})

		It("sets DestinationOutOfSync True with reason MissingImages while a desired reference is missing, and removes it once complete", func() {
			src := h.source()
			src.Push("acme/app:v1", registrytest.Image())
			createPod(h.namespace(), unique("pod"), container{name: appContainer, image: src.Host() + "/acme/app:v1"})
			h.mirror()
			h.reconcile()
			outOfSync := h.condition(kuikv1alpha1.ConditionDestinationOutOfSync)
			Expect(outOfSync).NotTo(BeNil())
			Expect(outOfSync.Status).To(Equal(metav1.ConditionTrue))
			Expect(outOfSync.Reason).To(Equal(kuikv1alpha1.ReasonMissingImages))

			h.window()
			Eventually(h.copied(destinationTag(src, "acme/app"))).Should(Succeed())
			Eventually(func() *metav1.Condition {
				h.reconcile()
				return h.condition(kuikv1alpha1.ConditionDestinationOutOfSync)
			}).Should(BeNil())
		})

		It("exports kuik_registry_interval_seconds with operation Scan for its destination host, through the scheduler's collector", func() {
			h.mirror()
			h.reconcile()

			scheduling := prometheus.NewRegistry()
			scheduling.MustRegister(h.scheduler.Collector())
			v, ok := gauge(scheduling, "kuik_registry_interval_seconds", map[string]string{"registry": h.destination.Host(), "operation": "Scan"})
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(time.Hour.Seconds()))
		})
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
