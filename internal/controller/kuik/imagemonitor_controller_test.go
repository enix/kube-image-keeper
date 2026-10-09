package kuik

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

const (
	kindImageMonitorLabel = "ImageMonitor"
	labelReference        = "reference"
	labelState            = "state"
	sidecarContainer      = "sidecar"
	skipLabel             = "skip"
	unknownOperator       = "Bogus"
	digestA               = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestC               = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// newMonitor creates an ImageMonitor selecting the namespaces labelled with its own name, so
// that the pods of other specs never count.
func newMonitor(name string, mutate ...func(*kuikv1alpha1.ImageMonitor)) *kuikv1alpha1.ImageMonitor {
	im := &kuikv1alpha1.ImageMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kuikv1alpha1.ImageMonitorSpec{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{testLabel: name}},
		},
	}
	for _, m := range mutate {
		m(im)
	}
	Expect(k8sClient.Create(ctx, im)).To(Succeed())
	return im
}

func monitorStatusOf(name string) kuikv1alpha1.ImageMonitorStatus {
	var im kuikv1alpha1.ImageMonitor
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &im)).To(Succeed())
	return im.Status
}

// originOf returns the origin counts of the monitor, zero when it reports none.
func originOf(name string) kuikv1alpha1.OriginImageCounts {
	s := monitorStatusOf(name)
	if s.Images == nil || s.Images.Origin == nil {
		return kuikv1alpha1.OriginImageCounts{}
	}
	return *s.Images.Origin
}

// podOption shapes a pod before it is created.
type podOption func(*corev1.Pod)

func withPodLabels(labels map[string]string) podOption {
	return func(p *corev1.Pod) { p.Labels = labels }
}

func withInitContainer(name, image string) podOption {
	return func(p *corev1.Pod) {
		p.Spec.InitContainers = append(p.Spec.InitContainers, corev1.Container{Name: name, Image: image})
	}
}

// asStaticPod makes the pod the mirror pod the kubelet publishes for a static pod, bound to
// its node as the API server requires.
func asStaticPod() podOption {
	return func(p *corev1.Pod) {
		p.Annotations["kubernetes.io/config.mirror"] = "mirror"
		p.Spec.NodeName = "node-a"
	}
}

// concededBy records that resource had placed rewrittenTo in container, and that another
// webhook replaced it.
func concededBy(container, resource, origin, rewrittenTo string) podOption {
	return func(p *corev1.Pod) {
		value, err := json.Marshal(map[string]podrecord.Rewrite{container: {
			By: resource, Origin: origin, RewrittenTo: rewrittenTo, Policy: string(kuikv1alpha1.RewritePolicyOnFailure),
		}})
		Expect(err).NotTo(HaveOccurred())
		p.Annotations[podrecord.AnnotationConcededRewrites] = string(value)
	}
}

// runningPod creates a Running pod in namespace, with the annotations its containers need.
func runningPod(namespace string, containers []container, opts ...podOption) *corev1.Pod {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: unique("pod"), Namespace: namespace, Annotations: map[string]string{}}}
	rewrites := map[string]podrecord.Rewrite{}
	for _, c := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: c.name, Image: c.image})
		if c.rewrite != nil {
			rewrites[c.name] = *c.rewrite
		}
	}
	if len(rewrites) > 0 {
		value, err := json.Marshal(rewrites)
		Expect(err).NotTo(HaveOccurred())
		pod.Annotations[podrecord.AnnotationRewrites] = string(value)
	}
	for _, o := range opts {
		o(pod)
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	setPhase(pod, corev1.PodRunning)
	return pod
}

// ran records in the status of pod that its container started at startedAt from digest.
func ran(pod *corev1.Pod, name, repository, digest string, startedAt time.Time) {
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
		Name:    name,
		ImageID: repository + "@" + digest,
		State:   corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(startedAt)}},
	})
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

func deletePod(pod *corev1.Pod) {
	Expect(k8sClient.Delete(ctx, pod, client.GracePeriodSeconds(0))).To(Succeed())
}

// routedToGhcr is an app container kuik rewrote from origin to the ghcr.io thanos under
// OnFailure, still running it.
func routedToGhcr(origin string) container {
	return container{name: appContainer, image: ghcrThanosImage, rewrite: &podrecord.Rewrite{
		By: routing.KindImageAlternative + "/other", Origin: origin, RewrittenTo: ghcrThanosImage, Policy: string(kuikv1alpha1.RewritePolicyOnFailure),
	}}
}

func running(name, image string) container {
	return container{name: name, image: image}
}

var _ = Describe("ImageMonitor", func() {
	var (
		registry   *prometheus.Registry
		recorder   *eventRecorder
		clock      *clocktesting.FakeClock
		reconciler *ImageMonitorReconciler
		name       string
	)

	BeforeEach(func() {
		registry = prometheus.NewRegistry()
		recorder = &eventRecorder{}
		// Status times are written to the second.
		clock = clocktesting.NewFakeClock(time.Now().Truncate(time.Second))
		var err error
		reconciler, err = NewImageMonitorReconciler(k8sClient, k8sClient.Scheme(), ImageMonitorOptions{
			Recorder:   recorder,
			Registerer: registry,
			Clock:      clock,
		})
		Expect(err).NotTo(HaveOccurred())
		name = unique("imon")
	})

	reconcileIt := func() ctrl.Result {
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	// selectedNamespace creates a namespace the monitor under test selects.
	selectedNamespace := func() string {
		ns := unique("ns")
		createNamespace(ns, map[string]string{testLabel: name})
		return ns
	}

	Context("tracking", func() {
		It("tracks the image of every container of the pods its podSelector and namespaceSelector select", func() {
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, thanosImage), running(sidecarContainer, reloaderImage)})
			runningPod(ns, []container{running(appContainer, ghcrThanosImage)})
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(3)))
			Expect(originOf(name).Running).To(Equal(int32(3)))
		})

		It("tracks nothing from a pod its selectors leave out", func() {
			newMonitor(name, func(im *kuikv1alpha1.ImageMonitor) {
				im.Spec.PodSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: skipLabel, Operator: metav1.LabelSelectorOpDoesNotExist},
				}}
			})
			ns := selectedNamespace()
			other := unique("ns")
			createNamespace(other, nil)
			runningPod(ns, []container{running(appContainer, thanosImage)})
			runningPod(ns, []container{running(appContainer, reloaderImage)}, withPodLabels(map[string]string{skipLabel: "yes"}))
			runningPod(other, []container{running(appContainer, ghcrThanosImage)})
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("tracks the images of init containers", func() {
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, thanosImage)},
				withInitContainer("init", reloaderImage))
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(2)))
		})

		It("never tracks the image of an ephemeral container", func() {
			newMonitor(name)
			pod := runningPod(selectedNamespace(), []container{running(appContainer, thanosImage)})
			pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{
				Name: "debug", Image: reloaderImage,
				ImagePullPolicy: corev1.PullIfNotPresent, TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			}}}
			Expect(k8sClient.SubResource("ephemeralcontainers").Update(ctx, pod)).To(Succeed())
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("tracks the images of a static pod", func() {
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, thanosImage)}, asStaticPod())
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("stops counting a pod once it is Succeeded or Failed", func() {
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, thanosImage)})
			succeeded := runningPod(ns, []container{running(appContainer, reloaderImage)})
			failed := runningPod(ns, []container{running(appContainer, ghcrThanosImage)})
			setPhase(succeeded, corev1.PodSucceeded)
			setPhase(failed, corev1.PodFailed)
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("tracks the origin of a container whose rewrite stands, never the reference kuik placed", func() {
			newMonitor(name)
			ns := selectedNamespace()
			// thanos was rewritten to its ghcr.io copy; another pod runs that copy as its own origin.
			runningPod(ns, []container{routedToGhcr(thanosImage)})
			runningPod(ns, []container{running(appContainer, ghcrThanosImage)})
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(2)))
		})

		It("tracks the live image of a container whose rewrite is stale", func() {
			newMonitor(name)
			ns := selectedNamespace()
			// The record says thanos went to ghcr.io, but the pod was edited to run the reloader.
			stale := routedToGhcr(thanosImage)
			stale.image = reloaderImage
			runningPod(ns, []container{stale})
			runningPod(ns, []container{running(appContainer, thanosImage)})
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(2)))
			Expect(originOf(name).Running).To(Equal(int32(2)))
		})

		It("tracks the live image of a container whose rewrite was conceded", func() {
			newMonitor(name)
			ns := selectedNamespace()
			// kuik had placed ghcr.io, another webhook replaced it with the reloader.
			runningPod(ns, []container{running(appContainer, reloaderImage)},
				concededBy(appContainer, routing.KindImageAlternative+"/other", thanosImage, ghcrThanosImage))
			runningPod(ns, []container{running(appContainer, thanosImage)})
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(2)))
			Expect(originOf(name).Running).To(Equal(int32(2)))
		})

		It("counts an image several pods declare once", func() {
			newMonitor(name)
			ns := selectedNamespace()
			for range 3 {
				runningPod(ns, []container{running(appContainer, thanosImage)})
			}
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("counts an origin a container carries as running", func() {
			newMonitor(name)
			ns := selectedNamespace()
			// One pod runs thanos itself, another was routed to ghcr.io: a container carries it.
			runningPod(ns, []container{running(appContainer, thanosImage)})
			runningPod(ns, []container{routedToGhcr(thanosImage)})
			reconcileIt()
			Expect(originOf(name).Running).To(Equal(int32(1)))
			Expect(originOf(name).Standby).To(BeZero())
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("counts an origin every container of which was routed elsewhere as standby", func() {
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{routedToGhcr(thanosImage)})
			runningPod(ns, []container{routedToGhcr(thanosImage)})
			reconcileIt()
			Expect(originOf(name).Standby).To(Equal(int32(1)))
			Expect(originOf(name).Running).To(BeZero())
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("counts an origin as running when a container another rewrite placed it in carries it", func() {
			newMonitor(name)
			ns := selectedNamespace()
			// ghcr.io thanos is the origin of a container routed to the reloader, and the
			// reference kuik placed in a container whose origin is thanos.
			placedGhcr := routedToGhcr(thanosImage)
			routedAway := container{name: appContainer, image: reloaderImage, rewrite: &podrecord.Rewrite{
				By: routing.KindImageAlternative + "/other", Origin: ghcrThanosImage, RewrittenTo: reloaderImage,
				Policy: string(kuikv1alpha1.RewritePolicyOnFailure),
			}}
			runningPod(ns, []container{placedGhcr})
			runningPod(ns, []container{routedAway})
			reconcileIt()
			// thanos is standby, ghcr.io thanos runs in the first pod.
			Expect(originOf(name).Standby).To(Equal(int32(1)))
			Expect(originOf(name).Running).To(Equal(int32(1)))
		})

		It("requeues instead of dropping its images when the pod cache returns no pod while images are tracked", func() {
			newMonitor(name)
			pod := runningPod(selectedNamespace(), []container{running(appContainer, thanosImage)})
			reconcileIt()
			Expect(originOf(name).Running).To(Equal(int32(1)))
			deletePod(pod)
			Expect(reconcileIt().RequeueAfter).To(BeNumerically(">", 0))
			Expect(originOf(name).Running).To(Equal(int32(1)))
			Expect(monitorStatusOf(name).RetainedImages).To(BeEmpty())
		})
	})

	Context("retention", func() {
		// retain runs thanos in two pods next to a reloader pod, then deletes the thanos pods:
		// thanos is then declared by no pod, while the reloader keeps the selection non-empty.
		retain := func(ns string) {
			runningPod(ns, []container{running(appContainer, reloaderImage)})
			older := runningPod(ns, []container{running(appContainer, thanosImage)})
			newer := runningPod(ns, []container{running(appContainer, thanosImage)})
			ran(older, appContainer, "quay.io/thanos/thanos", digestA, clock.Now().Add(-2*time.Hour))
			ran(newer, appContainer, "quay.io/thanos/thanos", digestC, clock.Now().Add(-time.Hour))
			reconcileIt()
			deletePod(older)
			deletePod(newer)
			reconcileIt()
		}

		It("moves an image no pod declares any more to retainedImages, stamped with when it became unused and the digest its most recently started container ran", func() {
			newMonitor(name)
			retain(selectedNamespace())
			Expect(monitorStatusOf(name).RetainedImages).To(ConsistOf(kuikv1alpha1.RetainedImage{
				Ref: thanosImage, UnusedSince: metav1.NewTime(clock.Now()), Digest: digestC,
			}))
			Expect(originOf(name).Retained).To(Equal(int32(1)))
			Expect(originOf(name).Running).To(Equal(int32(1)))
			Expect(originOf(name).Tracked).To(Equal(int32(2)))
		})

		It("keeps the unusedSince of a retained image across reconciles", func() {
			newMonitor(name)
			retain(selectedNamespace())
			since := clock.Now()
			clock.Step(time.Hour)
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(HaveLen(1))
			Expect(monitorStatusOf(name).RetainedImages[0].UnusedSince.Time).To(BeTemporally("==", since))
		})

		It("stops tracking a retained image once unusedImageRetention has elapsed", func() {
			newMonitor(name, func(im *kuikv1alpha1.ImageMonitor) {
				im.Spec.UnusedImageRetention = &metav1.Duration{Duration: time.Hour}
			})
			retain(selectedNamespace())
			clock.Step(30 * time.Minute)
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(HaveLen(1))
			clock.Step(31 * time.Minute)
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(BeEmpty())
			Expect(originOf(name).Retained).To(BeZero())
			Expect(originOf(name).Tracked).To(Equal(int32(1)))
		})

		It("takes an image out of retainedImages when a pod declares it again", func() {
			newMonitor(name)
			ns := selectedNamespace()
			retain(ns)
			runningPod(ns, []container{running(appContainer, thanosImage)})
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(BeEmpty())
			Expect(originOf(name).Running).To(Equal(int32(2)))
			Expect(originOf(name).Retained).To(BeZero())
		})
	})

	Context("conditions", func() {
		readiness := func() *metav1.Condition {
			return meta.FindStatusCondition(monitorStatusOf(name).Conditions, kuikv1alpha1.ConditionReady)
		}

		It("sets Ready to True with reason IsReady", func() {
			newMonitor(name)
			reconcileIt()
			Expect(readiness()).NotTo(BeNil())
			Expect(readiness().Status).To(Equal(metav1.ConditionTrue))
			Expect(readiness().Reason).To(Equal(kuikv1alpha1.ReasonIsReady))
		})

		It("sets Ready to False with reason InvalidConfig when a selector does not parse", func() {
			newMonitor(name, func(im *kuikv1alpha1.ImageMonitor) {
				im.Spec.PodSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: appContainer, Operator: unknownOperator, Values: []string{appContainer}},
				}}
			})
			reconcileIt()
			Expect(readiness()).NotTo(BeNil())
			Expect(readiness().Status).To(Equal(metav1.ConditionFalse))
			Expect(readiness().Reason).To(Equal(kuikv1alpha1.ReasonInvalidConfig))
		})
	})

	Context("metrics", func() {
		It("exports kuik_images_tracked from its image counts", func() {
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, thanosImage)})
			runningPod(ns, []container{routedToGhcr(reloaderImage)})
			reconcileIt()
			for state, want := range map[string]float64{"running": 1, "standby": 1, "retained": 0} {
				v, ok := gauge(registry, "kuik_images_tracked", map[string]string{
					labelKind: kindImageMonitorLabel, labelName: name, labelReference: "origin", labelState: state,
				})
				Expect(ok).To(BeTrue(), state)
				Expect(v).To(Equal(want), state)
			}
		})
	})

	Context("when deleted", func() {
		It("deletes its series", func() {
			im := newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, thanosImage)})
			reconcileIt()
			series := map[string]string{labelKind: kindImageMonitorLabel, labelName: name}
			_, ok := gauge(registry, "kuik_images_tracked", series)
			Expect(ok).To(BeTrue())
			Expect(k8sClient.Delete(ctx, im)).To(Succeed())
			reconcileIt()
			_, ok = gauge(registry, "kuik_images_tracked", series)
			Expect(ok).To(BeFalse())
		})
	})
})
