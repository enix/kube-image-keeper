package kuik

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/config"
	kuikregistry "github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
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

	// invalidSelector gives the monitor a podSelector that does not parse.
	invalidSelector := func(im *kuikv1alpha1.ImageMonitor) {
		im.Spec.PodSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: appContainer, Operator: unknownOperator, Values: []string{appContainer}},
		}}
	}

	// updateMonitor applies mutate to the monitor under test.
	updateMonitor := func(mutate func(*kuikv1alpha1.ImageMonitor)) {
		var im kuikv1alpha1.ImageMonitor
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &im)).To(Succeed())
		mutate(&im)
		Expect(k8sClient.Update(ctx, &im)).To(Succeed())
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

		It("tracks the pods of every namespace when its selectors are absent", func() {
			newMonitor(name, func(im *kuikv1alpha1.ImageMonitor) { im.Spec.NamespaceSelector = nil })
			reconcileIt()
			before := originOf(name).Tracked
			// The pods of other specs count too: an image no other spec runs, in a namespace
			// carrying no label.
			ns := unique("ns")
			createNamespace(ns, nil)
			runningPod(ns, []container{running(appContainer, "docker.io/library/nginx:"+unique("only"))})
			reconcileIt()
			Expect(originOf(name).Tracked).To(Equal(before + 1))
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

		It("keeps an unused image for 168h when unusedImageRetention is not set", func() {
			newMonitor(name)
			retain(selectedNamespace())
			clock.Step(167 * time.Hour)
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(HaveLen(1))
			clock.Step(2 * time.Hour)
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(BeEmpty())
		})

		It("retains an image whose first retention write failed", func() {
			// The next status write of an ImageMonitor fails with a conflict whenever conflict
			// is set, which that write clears.
			var conflict atomic.Bool
			watching, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
			Expect(err).NotTo(HaveOccurred())
			c := interceptor.NewClient(watching, interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					if _, ok := obj.(*kuikv1alpha1.ImageMonitor); ok && subResource == "status" && conflict.CompareAndSwap(true, false) {
						return apierrors.NewConflict(kuikv1alpha1.GroupVersion.WithResource("imagemonitors").GroupResource(), obj.GetName(), errors.New("injected"))
					}
					return c.SubResource(subResource).Update(ctx, obj, opts...)
				},
			})
			registry = prometheus.NewRegistry()
			reconciler, err = NewImageMonitorReconciler(c, k8sClient.Scheme(), ImageMonitorOptions{
				Recorder: recorder, Registerer: registry, Clock: clock,
			})
			Expect(err).NotTo(HaveOccurred())

			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, reloaderImage)})
			pod := runningPod(ns, []container{running(appContainer, thanosImage)})
			reconcileIt()
			deletePod(pod)
			conflict.Store(true)
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
			Expect(apierrors.IsConflict(err)).To(BeTrue())
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(ConsistOf(HaveField("Ref", thanosImage)))
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

		It("leaves its image counts and retainedImages as last reported while a selector does not parse", func() {
			newMonitor(name)
			pod := runningPod(selectedNamespace(), []container{running(appContainer, thanosImage)})
			reconcileIt()
			Expect(originOf(name).Running).To(Equal(int32(1)))
			updateMonitor(invalidSelector)
			deletePod(pod)
			reconcileIt()
			Expect(originOf(name).Running).To(Equal(int32(1)))
			Expect(monitorStatusOf(name).RetainedImages).To(BeEmpty())
		})

		It("emits ResourceNotReady when Ready goes False, then ResourceReady when it returns", func() {
			newMonitor(name, invalidSelector)
			reconcileIt()
			notReady := recorder.withReason("ResourceNotReady")
			Expect(notReady).To(HaveLen(1))
			Expect(notReady[0].regarding.(*kuikv1alpha1.ImageMonitor).Name).To(Equal(name))
			updateMonitor(func(im *kuikv1alpha1.ImageMonitor) { im.Spec.PodSelector = nil })
			reconcileIt()
			Expect(recorder.withReason("ResourceReady")).To(HaveLen(1))
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

		It("exports kuik_resource_not_ready while Ready is False", func() {
			newMonitor(name, invalidSelector)
			reconcileIt()
			v, ok := gauge(registry, "kuik_resource_not_ready", map[string]string{
				labelKind: kindImageMonitorLabel, labelName: name, seriesReason: kuikv1alpha1.ReasonInvalidConfig,
			})
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
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
			_, ok = gauge(registry, "kuik_status_list_entries", series)
			Expect(ok).To(BeTrue())
			Expect(k8sClient.Delete(ctx, im)).To(Succeed())
			reconcileIt()
			_, ok = gauge(registry, "kuik_images_tracked", series)
			Expect(ok).To(BeFalse())
			_, ok = gauge(registry, "kuik_status_list_entries", series)
			Expect(ok).To(BeFalse())
		})

		It("deletes its kuik_resource_not_ready series", func() {
			im := newMonitor(name, invalidSelector)
			reconcileIt()
			series := map[string]string{labelKind: kindImageMonitorLabel, labelName: name}
			_, ok := gauge(registry, "kuik_resource_not_ready", series)
			Expect(ok).To(BeTrue())
			Expect(k8sClient.Delete(ctx, im)).To(Succeed())
			reconcileIt()
			_, ok = gauge(registry, "kuik_resource_not_ready", series)
			Expect(ok).To(BeFalse())
		})
	})

	Context("checks", func() {
		var (
			scheduler *pacing.Scheduler
			// stopScheduler stops the scheduler, as a process ending would.
			stopScheduler context.CancelFunc
		)

		// start builds a reconciler checking on a scheduler of its own, paced every minute on
		// the fake clock, as a new process would: nothing of a previous memory survives. Its
		// series go to a new registry.
		start := func() {
			GinkgoHelper()
			scheduler = pacing.New(clock, mirrorConfig())
			schedulerCtx, stop := context.WithCancel(ctx)
			go func() { _ = scheduler.Start(schedulerCtx) }()
			DeferCleanup(stop)
			stopScheduler = stop
			registry = prometheus.NewRegistry()
			var err error
			reconciler, err = NewImageMonitorReconciler(k8sClient, k8sClient.Scheme(), ImageMonitorOptions{
				Recorder:   recorder,
				Registerer: registry,
				Clock:      clock,
				Scheduler:  scheduler,
				Registry:   kuikregistry.NewClient(),
			})
			Expect(err).NotTo(HaveOccurred())
		}
		BeforeEach(start)

		restart := func() {
			GinkgoHelper()
			stopScheduler()
			start()
		}

		// source starts a registry holding the tags v1 of repositories, closed when the spec
		// ends, with no request recorded yet.
		source := func(repositories ...string) *registrytest.Registry {
			GinkgoHelper()
			src := registrytest.New()
			DeferCleanup(src.Close)
			for _, repository := range repositories {
				src.Push(repository+":v1", registrytest.Image())
			}
			src.Reset()
			return src
		}

		// image is the reference of repository:v1 on src.
		image := func(src *registrytest.Registry, repository string) string {
			return src.Host() + "/" + repository + ":v1"
		}

		// window opens the next check window of every host, once the scheduler waits for it.
		window := func() {
			GinkgoHelper()
			Eventually(clock.HasWaiters).Should(BeTrue())
			clock.Step(time.Minute)
		}

		// turn opens a window when the scheduler waits for one, for the specs that poll: a
		// window opening while the previous check runs is lost, and the next poll opens another.
		turn := func() {
			if clock.HasWaiters() {
				clock.Step(time.Minute)
			}
		}

		// verdicts reconciles until the monitor counts n images with a verdict.
		verdicts := func(n int32) {
			GinkgoHelper()
			Eventually(func() int32 {
				reconcileIt()
				return originOf(name).Available + originOf(name).Unavailable
			}).Should(Equal(n))
		}

		// entryOf returns the unavailableImages entry of ref, nil when there is none.
		entryOf := func(ref string) *kuikv1alpha1.UnavailableImage {
			for _, e := range monitorStatusOf(name).UnavailableImages {
				if e.Ref == ref {
					return &e
				}
			}
			return nil
		}

		// unavailable reconciles until unavailableImages holds an entry for ref, and returns it.
		unavailable := func(ref string) kuikv1alpha1.UnavailableImage {
			GinkgoHelper()
			var entry *kuikv1alpha1.UnavailableImage
			Eventually(func() *kuikv1alpha1.UnavailableImage {
				reconcileIt()
				entry = entryOf(ref)
				return entry
			}).ShouldNot(BeNil())
			return *entry
		}

		// ringSize returns the size of the ring of the monitor on host, and whether it exists.
		ringSize := func(host string) (float64, bool) {
			scheduling := prometheus.NewRegistry()
			scheduling.MustRegister(scheduler.Collector())
			return gauge(scheduling, "kuik_check_ring_images", map[string]string{
				labelKind: kindImageMonitorLabel, labelName: name, labelRegistry: host,
			})
		}

		// originUnavailable returns the kuik_image_origin_unavailable series of ref, and
		// whether it exists.
		originUnavailable := func(src *registrytest.Registry, ref string, reason kuikv1alpha1.CheckFailureReason) (float64, bool) {
			return gauge(registry, "kuik_image_origin_unavailable", map[string]string{
				labelKind: kindImageMonitorLabel, labelName: name, labelImage: ref,
				labelRegistry: src.Host(), labelReason: string(reason),
			})
		}

		It("gives the scheduler one ring per registry host, holding the references tracked on that host", func() {
			first := source("acme/app", "acme/tool")
			second := source("acme/app")
			newMonitor(name)
			runningPod(selectedNamespace(), []container{
				running("a", image(first, "acme/app")),
				running("b", image(first, "acme/tool")),
				running("c", image(second, "acme/app")),
			})
			reconcileIt()
			size, ok := ringSize(first.Host())
			Expect(ok).To(BeTrue())
			Expect(size).To(Equal(2.0))
			size, ok = ringSize(second.Host())
			Expect(ok).To(BeTrue())
			Expect(size).To(Equal(1.0))

			// Two windows of each host: the first one reads both of its references.
			window()
			verdicts(2)
			window()
			verdicts(3)
			Expect(checked(first, "acme/app")()).To(Equal(1))
			Expect(checked(first, "acme/tool")()).To(Equal(1))
			Expect(checked(second, "acme/app")()).To(BeNumerically(">=", 1))
		})

		It("removes the ring of a host it no longer tracks anything on", func() {
			kept := source("acme/app")
			left := source("acme/app")
			newMonitor(name, func(im *kuikv1alpha1.ImageMonitor) {
				im.Spec.UnusedImageRetention = &metav1.Duration{}
			})
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, image(kept, "acme/app"))})
			leaving := runningPod(ns, []container{running(appContainer, image(left, "acme/app"))})
			reconcileIt()
			_, ok := ringSize(left.Host())
			Expect(ok).To(BeTrue())

			deletePod(leaving)
			reconcileIt()
			_, ok = ringSize(left.Host())
			Expect(ok).To(BeFalse())
			_, ok = ringSize(kept.Host())
			Expect(ok).To(BeTrue())
			Expect(monitorStatusOf(name).Checks).NotTo(BeNil())
			Expect(monitorStatusOf(name).Checks.Registries).To(ConsistOf(HaveField("Registry", kept.Host())))
		})

		It("resumes a ring from the cursor its status persisted", func() {
			src := source("acme/app", "acme/tool")
			newMonitor(name)
			runningPod(selectedNamespace(), []container{
				running("a", image(src, "acme/app")),
				running("b", image(src, "acme/tool")),
			})
			reconcileIt()
			window()
			verdicts(1)
			Expect(checked(src, "acme/tool")()).To(BeZero())
			Expect(monitorStatusOf(name).Checks.Registries).To(ConsistOf(HaveField("Cursor", image(src, "acme/app"))))

			// A new process: its own scheduler, its first window a full interval away.
			restart()
			reconcileIt()
			src.Reset()
			window()
			Eventually(checked(src, "acme/tool")).Should(Equal(1))
			Expect(checked(src, "acme/app")()).To(BeZero())
		})

		It("keeps its persisted cursors while a selector does not parse", func() {
			src := source("acme/app", "acme/tool")
			newMonitor(name)
			runningPod(selectedNamespace(), []container{
				running("a", image(src, "acme/app")),
				running("b", image(src, "acme/tool")),
			})
			reconcileIt()
			window()
			verdicts(1)

			// A new process starts while the selector does not parse: it tracks nothing.
			restart()
			updateMonitor(invalidSelector)
			reconcileIt()
			Expect(monitorStatusOf(name).Checks).NotTo(BeNil())
			Expect(monitorStatusOf(name).Checks.Registries).To(ConsistOf(HaveField("Cursor", image(src, "acme/app"))))

			updateMonitor(func(im *kuikv1alpha1.ImageMonitor) { im.Spec.PodSelector = nil })
			reconcileIt()
			src.Reset()
			window()
			Eventually(checked(src, "acme/tool")).Should(Equal(1))
			Expect(checked(src, "acme/app")()).To(BeZero())
		})

		It("reports the health of its rings in checks.registries", func() {
			src := source("acme/app")
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, image(src, "acme/app"))})
			Eventually(func() *metav1.Duration {
				turn()
				reconcileIt()
				checks := monitorStatusOf(name).Checks
				if checks == nil || len(checks.Registries) == 0 {
					return nil
				}
				return checks.Registries[0].CycleDuration
			}).ShouldNot(BeNil())

			ring := monitorStatusOf(name).Checks.Registries[0]
			Expect(ring.Registry).To(Equal(src.Host()))
			Expect(ring.Images).To(Equal(int32(1)))
			Expect(ring.Cursor).To(Equal(image(src, "acme/app")))
			Expect(ring.CycleStarted).NotTo(BeNil())
		})

		It("keeps checking a retained image", func() {
			retained := source("acme/app")
			other := source("acme/other")
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, image(other, "acme/other"))})
			pod := runningPod(ns, []container{running(appContainer, image(retained, "acme/app"))})
			reconcileIt()
			deletePod(pod)
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(ConsistOf(HaveField("Ref", image(retained, "acme/app"))))

			window()
			Eventually(checked(retained, "acme/app")).Should(Equal(1))
			verdicts(2)
			Expect(originOf(name).Available).To(Equal(int32(2)))
		})

		It("counts an image by the verdict of its last check, available or unavailable, and leaves out one not checked yet", func() {
			// The ring reads acme/app, then acme/gone, which the registry lacks, then acme/zzz.
			src := source("acme/app", "acme/zzz")
			newMonitor(name)
			runningPod(selectedNamespace(), []container{
				running("a", image(src, "acme/app")),
				running("b", image(src, "acme/gone")),
				running("c", image(src, "acme/zzz")),
			})
			reconcileIt()
			window()
			verdicts(1)
			window()
			verdicts(2)
			origin := originOf(name)
			Expect(origin.Tracked).To(Equal(int32(3)))
			Expect(origin.Available).To(Equal(int32(1)))
			Expect(origin.Unavailable).To(Equal(int32(1)))
		})

		It("lists an origin whose check failed in unavailableImages, with the reason and the number of pods declaring it", func() {
			src := source()
			newMonitor(name)
			ns := selectedNamespace()
			gone := image(src, "acme/gone")
			runningPod(ns, []container{running(appContainer, gone)})
			runningPod(ns, []container{running(appContainer, gone)})
			reconcileIt()
			window()
			entry := unavailable(gone)
			Expect(entry.Reason).To(Equal(kuikv1alpha1.CheckManifestNotFound))
			Expect(entry.Pods).To(Equal(int32(2)))
			Expect(entry.Since.Time).To(BeTemporally("==", clock.Now()))
		})

		It("keeps the since of an unavailableImages entry while the failure lasts", func() {
			src := source()
			newMonitor(name)
			gone := image(src, "acme/gone")
			runningPod(selectedNamespace(), []container{running(appContainer, gone)})
			reconcileIt()
			window()
			since := unavailable(gone).Since

			// A new process reads the failure back from the status, and its own check fails
			// for another reason: the failure goes on.
			restart()
			reconcileIt()
			Expect(entryOf(gone)).NotTo(BeNil())
			Expect(entryOf(gone).Since.Time).To(BeTemporally("==", since.Time))
			src.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusTooManyRequests, nil))
			window()
			Eventually(func() kuikv1alpha1.CheckFailureReason {
				reconcileIt()
				if e := entryOf(gone); e != nil {
					return e.Reason
				}
				return ""
			}).Should(Equal(kuikv1alpha1.CheckQuotaExceeded))
			Expect(entryOf(gone).Since.Time).To(BeTemporally("==", since.Time))
		})

		It("removes an unavailableImages entry once the image answers again", func() {
			src := source()
			newMonitor(name)
			gone := image(src, "acme/gone")
			runningPod(selectedNamespace(), []container{running(appContainer, gone)})
			reconcileIt()
			window()
			unavailable(gone)

			src.Push("acme/gone:v1", registrytest.Image())
			Eventually(func() []kuikv1alpha1.UnavailableImage {
				turn()
				reconcileIt()
				return monitorStatusOf(name).UnavailableImages
			}).Should(BeEmpty())
			Expect(originOf(name).Available).To(Equal(int32(1)))
			Expect(originOf(name).Unavailable).To(BeZero())
		})

		It("sets ImagesUnavailable with reason ChecksFailed while an origin fails its check, and removes it once none does", func() {
			src := source()
			newMonitor(name)
			gone := image(src, "acme/gone")
			runningPod(selectedNamespace(), []container{running(appContainer, gone)})
			reconcileIt()
			window()
			unavailable(gone)
			unavailableCondition := func() *metav1.Condition {
				return meta.FindStatusCondition(monitorStatusOf(name).Conditions, kuikv1alpha1.ConditionImagesUnavailable)
			}
			Expect(unavailableCondition()).NotTo(BeNil())
			Expect(unavailableCondition().Status).To(Equal(metav1.ConditionTrue))
			Expect(unavailableCondition().Reason).To(Equal(kuikv1alpha1.ReasonChecksFailed))

			src.Push("acme/gone:v1", registrytest.Image())
			Eventually(func() *metav1.Condition {
				turn()
				reconcileIt()
				return unavailableCondition()
			}).Should(BeNil())
		})

		It("emits no event when a tracked origin becomes unavailable", func() {
			src := source()
			newMonitor(name)
			gone := image(src, "acme/gone")
			runningPod(selectedNamespace(), []container{running(appContainer, gone)})
			reconcileIt()
			window()
			unavailable(gone)
			reconcileIt()
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			Expect(recorder.events).To(BeEmpty())
		})

		It("exports kuik_images_checked from the verdicts of its checks", func() {
			src := source("acme/app")
			newMonitor(name)
			runningPod(selectedNamespace(), []container{
				running("a", image(src, "acme/app")),
				running("b", image(src, "acme/gone")),
			})
			reconcileIt()
			window()
			verdicts(1)
			window()
			verdicts(2)
			for state, want := range map[string]float64{"available": 1, "unavailable": 1} {
				v, ok := gauge(registry, "kuik_images_checked", map[string]string{
					labelKind: kindImageMonitorLabel, labelName: name, labelReference: "origin", labelState: state,
				})
				Expect(ok).To(BeTrue(), state)
				Expect(v).To(Equal(want), state)
			}
		})

		It("exports one kuik_image_origin_unavailable series per failing origin, deleted once it answers", func() {
			src := source()
			newMonitor(name)
			first, second := image(src, "acme/first"), image(src, "acme/second")
			runningPod(selectedNamespace(), []container{running("a", first), running("b", second)})
			reconcileIt()
			window()
			unavailable(first)
			window()
			unavailable(second)
			for _, ref := range []string{first, second} {
				v, ok := originUnavailable(src, ref, kuikv1alpha1.CheckManifestNotFound)
				Expect(ok).To(BeTrue(), ref)
				Expect(v).To(Equal(1.0), ref)
			}

			src.Push("acme/first:v1", registrytest.Image())
			Eventually(func() bool {
				turn()
				reconcileIt()
				_, ok := originUnavailable(src, first, kuikv1alpha1.CheckManifestNotFound)
				return ok
			}).Should(BeFalse())
			_, ok := originUnavailable(src, second, kuikv1alpha1.CheckManifestNotFound)
			Expect(ok).To(BeTrue())
		})

		It("starts from an empty memory when deleted and created again under the same name before a reconcile sees it gone", func() {
			// The registry refuses every anonymous check.
			src := source()
			src.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusUnauthorized, nil))
			denied := image(src, "acme/denied")
			im := newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, denied)})
			reconcileIt()
			window()
			unavailable(denied)
			Expect(recorder.withReason("AnonymousAccessDenied")).To(HaveLen(1))

			Expect(k8sClient.Delete(ctx, im)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &kuikv1alpha1.ImageMonitor{}))
			}).Should(BeTrue())
			newMonitor(name)
			reconcileIt()
			Expect(monitorStatusOf(name).UnavailableImages).To(BeEmpty())

			window()
			unavailable(denied)
			Expect(recorder.withReason("AnonymousAccessDenied")).To(HaveLen(2))
		})

		It("removes its rings from the scheduler when deleted", func() {
			src := source("acme/app")
			im := newMonitor(name)
			gone := image(src, "acme/gone")
			runningPod(selectedNamespace(), []container{running(appContainer, image(src, "acme/app")), running("b", gone)})
			reconcileIt()
			_, ok := ringSize(src.Host())
			Expect(ok).To(BeTrue())
			window()
			verdicts(1)
			window()
			unavailable(gone)
			_, ok = originUnavailable(src, gone, kuikv1alpha1.CheckManifestNotFound)
			Expect(ok).To(BeTrue())

			Expect(k8sClient.Delete(ctx, im)).To(Succeed())
			reconcileIt()
			_, ok = ringSize(src.Host())
			Expect(ok).To(BeFalse())
			_, ok = originUnavailable(src, gone, kuikv1alpha1.CheckManifestNotFound)
			Expect(ok).To(BeFalse())
		})

		It("reports Unreachable for a check that failed without an answer from the registry", func() {
			Expect(checkFailure(errors.New("dial tcp: connection refused"))).To(Equal(kuikv1alpha1.CheckUnreachable))
		})
	})

	Context("credentials", func() {
		const (
			user     = registryUser
			password = "s3cr3t"
		)
		var (
			fallbackAuth []auth.FallbackAuthEntry
			// stopScheduler stops the scheduler, as a process ending would.
			stopScheduler context.CancelFunc
		)

		// start builds a reconciler checking with fallbackAuth on a scheduler of its own,
		// paced every minute on the fake clock, as a new process would. Its series go to a new
		// registry.
		start := func() {
			GinkgoHelper()
			pacingConfig := mirrorConfig()
			pacingConfig.FallbackAuth = fallbackAuth
			scheduler := pacing.New(clock, pacingConfig)
			schedulerCtx, stop := context.WithCancel(ctx)
			go func() { _ = scheduler.Start(schedulerCtx) }()
			DeferCleanup(stop)
			stopScheduler = stop
			registry = prometheus.NewRegistry()
			var err error
			reconciler, err = NewImageMonitorReconciler(k8sClient, k8sClient.Scheme(), ImageMonitorOptions{
				APIReader:                k8sClient,
				ClusterResourceNamespace: installNamespace,
				Recorder:                 recorder,
				Registerer:               registry,
				Clock:                    clock,
				Scheduler:                scheduler,
				Registry:                 kuikregistry.NewClient(),
				Config:                   pacingConfig,
			})
			Expect(err).NotTo(HaveOccurred())
		}
		BeforeEach(func() {
			fallbackAuth = nil
			createNamespace(installNamespace, nil)
		})

		// protected starts a registry refusing every request without the basic credentials of
		// user, holding acme/app:v1, closed when the spec ends, with no request recorded yet.
		protected := func() *registrytest.Registry {
			GinkgoHelper()
			src := registrytest.New(registrytest.WithBasicAuth(user, password))
			DeferCleanup(src.Close)
			src.Push("acme/app:v1", registrytest.Image())
			src.Reset()
			return src
		}

		// open starts a registry answering anyone, holding acme/app:v1.
		open := func() *registrytest.Registry {
			GinkgoHelper()
			src := registrytest.New()
			DeferCleanup(src.Close)
			src.Push("acme/app:v1", registrytest.Image())
			src.Reset()
			return src
		}

		app := func(src *registrytest.Registry) string {
			return src.Host() + "/acme/app:v1"
		}

		// fallback declares a fallbackAuth entry for repository, read from the Secret secret of
		// the install namespace.
		fallback := func(repository, repositoryGroup, secret string) {
			fallbackAuth = append(fallbackAuth, auth.FallbackAuthEntry{
				Repository: repository, RepositoryGroup: repositoryGroup,
				SecretRef: &kuikv1alpha1.SecretReference{Name: secret},
			})
		}

		// window opens the next check window of every host, once the scheduler waits for it.
		window := func() {
			GinkgoHelper()
			Eventually(clock.HasWaiters).Should(BeTrue())
			clock.Step(time.Minute)
		}

		// availability reconciles until the image has a verdict, and returns the origin counts.
		availability := func() kuikv1alpha1.OriginImageCounts {
			GinkgoHelper()
			Eventually(func() int32 {
				reconcileIt()
				return originOf(name).Available + originOf(name).Unavailable
			}).Should(Equal(int32(1)))
			return originOf(name)
		}

		readiness := func() *metav1.Condition {
			return meta.FindStatusCondition(monitorStatusOf(name).Conditions, kuikv1alpha1.ConditionReady)
		}

		It("checks an image with the imagePullSecrets of each pod declaring it, until one answers", func() {
			src := protected()
			start()
			newMonitor(name)
			ns := selectedNamespace()
			wrong := pullSecret(ns, src.Host(), "wrong")
			right := pullSecret(ns, src.Host(), password)
			// The pod read first carries the wrong credential, the other one the right one.
			runningPod(ns, []container{running(appContainer, app(src))}, withPodName("a-"+unique("pod")), withPullSecrets(wrong))
			runningPod(ns, []container{running(appContainer, app(src))}, withPodName("b-"+unique("pod")), withPullSecrets(right))
			reconcileIt()
			window()
			Expect(availability().Available).To(Equal(int32(1)))
		})

		It("checks an image with the most specific matching fallbackAuth entry", func() {
			src := protected()
			fallback("", src.Host(), createCredentials(src.Host(), user, "wrong"))
			fallback(src.Host()+"/acme/app", "", createCredentials(src.Host(), user, password))
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()
			window()
			Expect(availability().Available).To(Equal(int32(1)))
		})

		It("checks an image anonymously when no credential applies", func() {
			src := open()
			// Credentials for other registries and other repositories only.
			fallback("registry.example.com/acme/app", "", createCredentials("registry.example.com", user, password))
			start()
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, app(src))},
				withPullSecrets(pullSecret(ns, "registry.example.com", password)))
			reconcileIt()
			window()
			Expect(availability().Available).To(Equal(int32(1)))
			heads := src.Requests(http.MethodHead, "/manifests/")
			Expect(heads).NotTo(BeEmpty())
			Expect(heads).To(HaveEach(HaveField("Authorized", BeFalse())))
		})

		It("keeps checking an image with the next credential when its fallbackAuth Secret is missing or malformed", func() {
			missing, malformed := open(), open()
			fallback("", missing.Host(), unique("missing"))
			notDockerConfig := unique("opaque")
			createSecret(installNamespace, notDockerConfig, corev1.SecretTypeOpaque)
			fallback("", malformed.Host(), notDockerConfig)
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running("a", app(missing)), running("b", app(malformed))})
			reconcileIt()
			window()
			Eventually(func() int32 {
				reconcileIt()
				return originOf(name).Available
			}).Should(Equal(int32(2)))
		})

		It("sets Ready to False with reason SecretNotFound when the fallbackAuth Secret of a tracked image is missing", func() {
			src := open()
			fallback("", src.Host(), unique("missing"))
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()
			window()
			Eventually(func() string {
				reconcileIt()
				if c := readiness(); c != nil && c.Status == metav1.ConditionFalse {
					return c.Reason
				}
				return ""
			}).Should(Equal(kuikv1alpha1.ReasonSecretNotFound))
		})

		It("sets Ready to False with reason SecretMalformed when the fallbackAuth Secret of a tracked image is not a docker config", func() {
			src := open()
			notDockerConfig := unique("opaque")
			createSecret(installNamespace, notDockerConfig, corev1.SecretTypeOpaque)
			fallback("", src.Host(), notDockerConfig)
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()
			window()
			Eventually(func() string {
				reconcileIt()
				if c := readiness(); c != nil && c.Status == metav1.ConditionFalse {
					return c.Reason
				}
				return ""
			}).Should(Equal(kuikv1alpha1.ReasonSecretMalformed))
		})

		// deniedAnonymously runs an image a protected registry refuses to an anonymous check
		// until it enters unavailableImages with Unauthorized, and returns the registry.
		deniedAnonymously := func() *registrytest.Registry {
			GinkgoHelper()
			src := protected()
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()
			window()
			Eventually(func() []kuikv1alpha1.UnavailableImage {
				reconcileIt()
				return monitorStatusOf(name).UnavailableImages
			}).Should(ConsistOf(And(HaveField("Ref", app(src)), HaveField("Reason", kuikv1alpha1.CheckUnauthorized))))
			return src
		}

		It("emits AnonymousAccessDenied once when an origin starts failing an anonymous check with Unauthorized", func() {
			src := deniedAnonymously()
			denied := recorder.withReason("AnonymousAccessDenied")
			Expect(denied).To(HaveLen(1))
			Expect(denied[0].eventType).To(Equal(corev1.EventTypeWarning))
			Expect(denied[0].regarding.(*kuikv1alpha1.ImageMonitor).Name).To(Equal(name))
			Expect(denied[0].note).To(ContainSubstring(app(src)))

			// The next check fails the same way: the entry stays, and says it already.
			before := checked(src, "acme/app")()
			window()
			Eventually(checked(src, "acme/app")).Should(BeNumerically(">", before))
			Consistently(func() []recordedEvent {
				reconcileIt()
				return recorder.withReason("AnonymousAccessDenied")
			}).WithTimeout(500 * time.Millisecond).Should(HaveLen(1))
		})

		It("emits nothing again after a restart for an anomaly its status already holds", func() {
			src := deniedAnonymously()
			Expect(recorder.withReason("AnonymousAccessDenied")).To(HaveLen(1))

			stopScheduler()
			start()
			reconcileIt()
			before := checked(src, "acme/app")()
			window()
			Eventually(checked(src, "acme/app")).Should(BeNumerically(">", before))
			Consistently(func() []recordedEvent {
				reconcileIt()
				return recorder.withReason("AnonymousAccessDenied")
			}).WithTimeout(500 * time.Millisecond).Should(HaveLen(1))
		})

		// reasonOf reconciles and returns the reason of the unavailableImages entry of ref,
		// empty when there is none.
		reasonOf := func(ref string) kuikv1alpha1.CheckFailureReason {
			reconcileIt()
			for _, e := range monitorStatusOf(name).UnavailableImages {
				if e.Ref == ref {
					return e.Reason
				}
			}
			return ""
		}

		It("emits AnonymousAccessDenied once while an image keeps failing, alternating between Unauthorized and another reason", func() {
			// The registry answers each anonymous check with what the spec sets.
			src := open()
			src.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusUnauthorized, nil))
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()
			window()
			Eventually(func() kuikv1alpha1.CheckFailureReason { return reasonOf(app(src)) }).Should(Equal(kuikv1alpha1.CheckUnauthorized))
			Expect(recorder.withReason("AnonymousAccessDenied")).To(HaveLen(1))

			src.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusTooManyRequests, nil))
			window()
			Eventually(func() kuikv1alpha1.CheckFailureReason { return reasonOf(app(src)) }).Should(Equal(kuikv1alpha1.CheckQuotaExceeded))

			src.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusUnauthorized, nil))
			window()
			Eventually(func() kuikv1alpha1.CheckFailureReason { return reasonOf(app(src)) }).Should(Equal(kuikv1alpha1.CheckUnauthorized))
			reconcileIt()
			Expect(recorder.withReason("AnonymousAccessDenied")).To(HaveLen(1))
		})

		It("emits no AnonymousAccessDenied when a check that sent a credential fails with Unauthorized", func() {
			src := protected()
			start()
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, app(src))},
				withPullSecrets(pullSecret(ns, src.Host(), "wrong")))
			reconcileIt()
			window()
			Eventually(func() kuikv1alpha1.CheckFailureReason { return reasonOf(app(src)) }).Should(Equal(kuikv1alpha1.CheckUnauthorized))
			Consistently(func() []recordedEvent {
				reconcileIt()
				return recorder.withReason("AnonymousAccessDenied")
			}).WithTimeout(500 * time.Millisecond).Should(BeEmpty())
		})

		It("emits no AnonymousAccessDenied for an image a fallbackAuth entry covers, even when its Secret is missing", func() {
			// The registry refuses every check, which goes out anonymously: the Secret is missing.
			src := open()
			src.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusUnauthorized, nil))
			fallback("", src.Host(), unique("missing"))
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()
			window()
			Eventually(func() kuikv1alpha1.CheckFailureReason { return reasonOf(app(src)) }).Should(Equal(kuikv1alpha1.CheckUnauthorized))
			Expect(readiness()).NotTo(BeNil())
			Expect(readiness().Reason).To(Equal(kuikv1alpha1.ReasonSecretNotFound))
			Consistently(func() []recordedEvent {
				reconcileIt()
				return recorder.withReason("AnonymousAccessDenied")
			}).WithTimeout(500 * time.Millisecond).Should(BeEmpty())
		})

		It("sets Ready back to True with reason IsReady once the fallbackAuth Secret is created, without waiting for a check", func() {
			src := open()
			secret := unique("later")
			fallback("", src.Host(), secret)
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()
			window()
			Eventually(func() string {
				reconcileIt()
				if c := readiness(); c != nil && c.Status == metav1.ConditionFalse {
					return c.Reason
				}
				return ""
			}).Should(Equal(kuikv1alpha1.ReasonSecretNotFound))

			createSecret(installNamespace, secret, corev1.SecretTypeDockerConfigJson)
			reconcileIt()
			Expect(readiness()).NotTo(BeNil())
			Expect(readiness().Status).To(Equal(metav1.ConditionTrue))
			Expect(readiness().Reason).To(Equal(kuikv1alpha1.ReasonIsReady))
		})

		It("checks an image with a fallbackAuth entry a config reload added", func() {
			src := protected()
			start()
			newMonitor(name)
			runningPod(selectedNamespace(), []container{running(appContainer, app(src))})
			reconcileIt()

			reloaded := mirrorConfig()
			reloaded.FallbackAuth = []auth.FallbackAuthEntry{{
				RepositoryGroup: src.Host(),
				SecretRef:       &kuikv1alpha1.SecretReference{Name: createCredentials(src.Host(), user, password)},
			}}
			reconciler.SetConfig(reloaded)
			window()
			Expect(availability().Available).To(Equal(int32(1)))
		})

		It("leaves AnonymousAccessDenied and Ready to the monitor whose ring sent the check, when another monitor reuses its verdict", func() {
			// denied refuses every anonymous check; covered is covered by a fallbackAuth entry
			// whose Secret is missing.
			deniedSrc := open()
			deniedSrc.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusUnauthorized, nil))
			coveredSrc := open()
			fallback("", coveredSrc.Host(), unique("missing"))
			start()
			images := []container{running("a", app(deniedSrc)), running("b", app(coveredSrc))}

			// The other monitor holds the first ring of each host: it sends the checks of the
			// first window, which the monitor under test reuses at the second.
			other := unique("imon")
			newMonitor(other)
			otherNs := unique("ns")
			createNamespace(otherNs, map[string]string{testLabel: other})
			runningPod(otherNs, images)
			reconcileOther := func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: other}})
				Expect(err).NotTo(HaveOccurred())
			}
			reconcileOther()
			newMonitor(name)
			runningPod(selectedNamespace(), images)
			reconcileIt()

			window()
			Eventually(func() int32 {
				reconcileOther()
				return originOf(other).Unavailable
			}).Should(Equal(int32(1)))
			window()
			Eventually(func() int32 {
				reconcileIt()
				return originOf(name).Unavailable
			}).Should(Equal(int32(1)))
			reconcileOther()

			regarding := func(monitor string) int {
				n := 0
				for _, e := range recorder.withReason("AnonymousAccessDenied") {
					if e.regarding.(*kuikv1alpha1.ImageMonitor).Name == monitor {
						n++
					}
				}
				return n
			}
			Expect(regarding(other)).To(Equal(1))
			Expect(regarding(name)).To(BeZero())
			Expect(meta.FindStatusCondition(monitorStatusOf(other).Conditions, kuikv1alpha1.ConditionReady).Reason).To(Equal(kuikv1alpha1.ReasonSecretNotFound))
			Expect(readiness().Reason).To(Equal(kuikv1alpha1.ReasonIsReady))
		})

		It("tries a pull secret copied in several namespaces once", func() {
			src := protected()
			start()
			newMonitor(name)
			for range 3 {
				ns := selectedNamespace()
				runningPod(ns, []container{running(appContainer, app(src))},
					withPullSecrets(pullSecret(ns, src.Host(), "wrong")))
			}
			reconcileIt()
			window()
			Expect(availability().Unavailable).To(Equal(int32(1)))
			Expect(checked(src, "acme/app")()).To(Equal(1))
		})

		It("tries the pull secrets of the pods before the fallbackAuth entry", func() {
			// The registry answers any credential, and records the password of each manifest HEAD.
			src := open()
			var (
				mu        sync.Mutex
				passwords []string
			)
			src.Intercept(func(_ http.ResponseWriter, r *http.Request) bool {
				if _, pass, ok := r.BasicAuth(); ok && r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/manifests/") {
					mu.Lock()
					passwords = append(passwords, pass)
					mu.Unlock()
				}
				return false
			})
			fallback("", src.Host(), createCredentials(src.Host(), user, "from-fallback"))
			start()
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, app(src))},
				withPullSecrets(pullSecret(ns, src.Host(), "from-pod")))
			reconcileIt()
			window()
			Expect(availability().Available).To(Equal(int32(1)))
			mu.Lock()
			defer mu.Unlock()
			Expect(passwords).To(Equal([]string{"from-pod"}))
		})

		It("falls through to the fallbackAuth entry when the registry refuses the pull secrets of the pods", func() {
			src := protected()
			fallback("", src.Host(), createCredentials(src.Host(), user, password))
			start()
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, app(src))},
				withPullSecrets(pullSecret(ns, src.Host(), "wrong")))
			reconcileIt()
			window()
			Expect(availability().Available).To(Equal(int32(1)))
		})
	})

	Context("bounded lists", func() {
		// The cap holds whatever its value: a small one is exceeded by a handful of images.
		const capacity, over = 5, 6
		// unavailableList is the status field the capped list of failing origins goes to.
		const unavailableList = "unavailableImages"

		var (
			// stopScheduler stops the scheduler, as a process ending would.
			stopScheduler context.CancelFunc
			// checkPacing is the config the scheduler paces the checks with.
			checkPacing *config.Config
		)

		// start builds a reconciler capping its lists at capacity, checking on a scheduler of
		// its own, as a new process would. Its series go to a new registry.
		start := func() {
			GinkgoHelper()
			scheduler := pacing.New(clock, checkPacing)
			schedulerCtx, stop := context.WithCancel(ctx)
			go func() { _ = scheduler.Start(schedulerCtx) }()
			DeferCleanup(stop)
			stopScheduler = stop
			registry = prometheus.NewRegistry()
			var err error
			reconciler, err = NewImageMonitorReconciler(k8sClient, k8sClient.Scheme(), ImageMonitorOptions{
				Recorder:     recorder,
				Registerer:   registry,
				Clock:        clock,
				Scheduler:    scheduler,
				Registry:     kuikregistry.NewClient(),
				ListCapacity: capacity,
			})
			Expect(err).NotTo(HaveOccurred())
		}
		BeforeEach(func() {
			checkPacing = mirrorConfig()
			start()
		})

		restart := func() {
			GinkgoHelper()
			stopScheduler()
			start()
		}

		// failing runs n images a registry lacks, acme/gone0:v1 onwards, and opens one window
		// per image, so that each one fails a minute after the previous one. It returns them.
		failing := func(n int) (*registrytest.Registry, []string) {
			GinkgoHelper()
			src := registrytest.New()
			DeferCleanup(src.Close)
			refs := make([]string, 0, n)
			containers := make([]container, 0, n)
			for i := range n {
				ref := fmt.Sprintf("%s/acme/gone%d:v1", src.Host(), i)
				refs = append(refs, ref)
				containers = append(containers, running(fmt.Sprintf("c%d", i), ref))
			}
			newMonitor(name)
			runningPod(selectedNamespace(), containers)
			reconcileIt()
			for i := range n {
				Eventually(clock.HasWaiters).Should(BeTrue())
				clock.Step(time.Minute)
				Eventually(func() int32 {
					reconcileIt()
					return originOf(name).Unavailable
				}).Should(Equal(int32(i + 1)))
			}
			return src, refs
		}

		pressure := func() *metav1.Condition {
			return meta.FindStatusCondition(monitorStatusOf(name).Conditions, kuikv1alpha1.ConditionListCapacityPressure)
		}

		It("sets ListCapacityPressure once an anomaly list nears the cap", func() {
			failing(capacity - 1)
			Expect(monitorStatusOf(name).UnavailableImages).To(HaveLen(capacity - 1))
			Expect(pressure()).NotTo(BeNil())
			Expect(pressure().Status).To(Equal(metav1.ConditionTrue))
			Expect(pressure().Reason).To(Equal(kuikv1alpha1.ReasonListNearCapacity))
			Expect(monitorStatusOf(name).Truncated).To(BeEmpty())
		})

		It("caps its anomaly lists, keeping the oldest entries and recording the rest in truncated", func() {
			// The images fail in the reverse order of their references: acme/gone5 first.
			src := registrytest.New()
			DeferCleanup(src.Close)
			im := newMonitor(name)
			refs := make([]string, 0, over)
			containers := make([]container, 0, over)
			for i := range over {
				ref := fmt.Sprintf("%s/acme/gone%d:v1", src.Host(), i)
				refs = append(refs, ref)
				containers = append(containers, running(fmt.Sprintf("c%d", i), ref))
				im.Status.UnavailableImages = append(im.Status.UnavailableImages, kuikv1alpha1.UnavailableImage{
					Ref: ref, Reason: kuikv1alpha1.CheckManifestNotFound, Pods: 1,
					Since: metav1.NewTime(clock.Now().Add(-time.Duration(i) * time.Minute)),
				})
			}
			Expect(k8sClient.Status().Update(ctx, im)).To(Succeed())
			runningPod(selectedNamespace(), containers)
			reconcileIt()

			status := monitorStatusOf(name)
			kept := make([]string, 0, len(status.UnavailableImages))
			for _, e := range status.UnavailableImages {
				kept = append(kept, e.Ref)
			}
			Expect(kept).To(Equal([]string{refs[5], refs[4], refs[3], refs[2], refs[1]}))
			Expect(status.Truncated).To(Equal(map[string]int32{unavailableList: 1}))
			Expect(status.Images.Origin.Unavailable).To(Equal(int32(over)))
			Expect(pressure()).NotTo(BeNil())
			Expect(pressure().Reason).To(Equal(kuikv1alpha1.ReasonListTruncated))
		})

		It("holds truncated, ListTruncated and the unavailable count a restart read back until its rings checked every image again", func() {
			src, _ := failing(over)
			restart()
			reconcileIt()
			status := monitorStatusOf(name)
			Expect(status.Truncated).To(Equal(map[string]int32{unavailableList: 1}))
			Expect(status.Images.Origin.Unavailable).To(Equal(int32(over)))
			Expect(pressure()).NotTo(BeNil())
			Expect(pressure().Reason).To(Equal(kuikv1alpha1.ReasonListTruncated))

			// The image the list left out answers again: the ring finds it on its lap.
			src.Push("acme/gone5:v1", registrytest.Image())
			Eventually(func() int32 {
				if clock.HasWaiters() {
					clock.Step(time.Minute)
				}
				reconcileIt()
				return originOf(name).Available
			}).Should(Equal(int32(1)))
			Expect(monitorStatusOf(name).Truncated).To(BeEmpty())
			Expect(originOf(name).Unavailable).To(Equal(int32(capacity)))
		})

		It("releases the counts a restart read back once every image it can check got a verdict, an unparseable reference aside", func() {
			src, _ := failing(over)
			// A reference no ring can check: an uppercase repository does not parse.
			runningPod(selectedNamespace(), []container{running(appContainer, "acme/BadName:v1")})
			reconcileIt()

			restart()
			reconcileIt()
			Expect(monitorStatusOf(name).Truncated).To(Equal(map[string]int32{unavailableList: 1}))

			// The image the list left out answers again: once the ring has read it, every image
			// it can check has a verdict.
			src.Push("acme/gone5:v1", registrytest.Image())
			Eventually(func() int32 {
				if clock.HasWaiters() {
					clock.Step(time.Minute)
				}
				reconcileIt()
				return originOf(name).Available
			}).Should(Equal(int32(1)))
			Expect(monitorStatusOf(name).Truncated).To(BeEmpty())
			Expect(originOf(name).Unavailable).To(Equal(int32(capacity)))
		})

		It("never counts more images with a verdict than it tracks after a restart", func() {
			// Six images fail on a registry checked every minute, then one on a registry checked
			// every hour: the newest failures, acme/gone5 and the slow one, are left out.
			src := registrytest.New()
			DeferCleanup(src.Close)
			slow := registrytest.New()
			DeferCleanup(slow.Close)
			checkPacing.Registries.Hosts = map[string]config.RegistryPacing{slow.Host(): {Check: &config.Window{
				Interval: &config.Duration{Duration: time.Hour}, Timeout: &config.Duration{},
			}}}
			restart()
			newMonitor(name)
			containers := make([]container, 0, over+1)
			containers = append(containers, running("slow", slow.Host()+"/acme/gone:v1"))
			for i := range over {
				containers = append(containers, running(fmt.Sprintf("c%d", i), fmt.Sprintf("%s/acme/gone%d:v1", src.Host(), i)))
			}
			runningPod(selectedNamespace(), containers)
			reconcileIt()
			step := func(d time.Duration, unavailable int32) {
				GinkgoHelper()
				Eventually(clock.HasWaiters).Should(BeTrue())
				clock.Step(d)
				Eventually(func() int32 {
					reconcileIt()
					return originOf(name).Unavailable
				}).Should(Equal(unavailable))
			}
			for i := range over {
				step(time.Minute, int32(i+1))
			}
			step(time.Hour, over+1)
			Expect(monitorStatusOf(name).Truncated).To(Equal(map[string]int32{unavailableList: 2}))

			// A new process: acme/gone5 answers again before the slow registry is read.
			restart()
			reconcileIt()
			src.Push("acme/gone5:v1", registrytest.Image())
			Eventually(func() int32 {
				if clock.HasWaiters() {
					clock.Step(time.Minute)
				}
				reconcileIt()
				return originOf(name).Available
			}).Should(Equal(int32(1)))
			origin := originOf(name)
			Expect(origin.Available + origin.Unavailable).To(BeNumerically("<=", origin.Tracked))
		})

		It("never caps retainedImages", func() {
			newMonitor(name)
			ns := selectedNamespace()
			runningPod(ns, []container{running(appContainer, thanosImage)})
			containers := make([]container, 0, over)
			for i := range over {
				containers = append(containers, running(fmt.Sprintf("c%d", i), fmt.Sprintf("quay.io/acme/retained%d:v1", i)))
			}
			pod := runningPod(ns, containers)
			reconcileIt()
			deletePod(pod)
			reconcileIt()
			Expect(monitorStatusOf(name).RetainedImages).To(HaveLen(over))
			Expect(monitorStatusOf(name).Truncated).To(BeEmpty())
		})

		It("keeps a series for every anomaly beyond the status cap", func() {
			src, refs := failing(over)
			Expect(monitorStatusOf(name).UnavailableImages).To(HaveLen(capacity))
			for _, ref := range refs {
				_, ok := gauge(registry, "kuik_image_origin_unavailable", map[string]string{
					labelKind: kindImageMonitorLabel, labelName: name, labelImage: ref, labelRegistry: src.Host(),
				})
				Expect(ok).To(BeTrue(), ref)
			}
		})

		It("exports the kuik_status_list_* series of its capped lists", func() {
			failing(over)
			labels := map[string]string{labelKind: kindImageMonitorLabel, labelName: name, "list": unavailableList}
			entries, ok := gauge(registry, "kuik_status_list_entries", labels)
			Expect(ok).To(BeTrue())
			Expect(entries).To(Equal(float64(capacity)))
			limit, ok := gauge(registry, "kuik_status_list_capacity", labels)
			Expect(ok).To(BeTrue())
			Expect(limit).To(Equal(float64(capacity)))
			Expect(counter(registry, "kuik_status_list_dropped_total", labels)).To(Equal(1.0))
		})
	})
})

// withPodName names the pod.
func withPodName(name string) podOption {
	return func(p *corev1.Pod) { p.Name = name }
}

// withPullSecrets gives the pod the imagePullSecrets names.
func withPullSecrets(names ...string) podOption {
	return func(p *corev1.Pod) {
		for _, n := range names {
			p.Spec.ImagePullSecrets = append(p.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: n})
		}
	}
}

// registryUser is the user the protected registries of the credential specs expect.
const registryUser = "kuik"

// pullSecret creates a docker-registry Secret of namespace holding registryUser and password
// for host, and returns its name.
func pullSecret(namespace, host, password string) string {
	name := unique("pull")
	encoded := base64.StdEncoding.EncodeToString([]byte(registryUser + ":" + password))
	dockerConfig := fmt.Sprintf(`{"auths":{%q:{"username":%q,"password":%q,"auth":%q}}}`, host, registryUser, password, encoded)
	Expect(k8sClient.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerConfig)},
	})).To(Succeed())
	return name
}
