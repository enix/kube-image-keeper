package kuik

import (
	"context"
	"encoding/json"
	"fmt"
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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

const (
	installNamespace = "kuik-system"
	testLabel        = "kuik.enix.io/test"
	labelKind        = "kind"
	labelName        = "name"
	appContainer     = "app"
	thanosImage      = "quay.io/thanos/thanos:v0.42.2"
	ghcrThanosImage  = "ghcr.io/thanos-io/thanos:v0.42.2"
	reloaderImage    = "quay.io/prometheus-operator/prometheus-config-reloader:v0.80.0"
)

var testCount atomic.Int64

// unique returns a name no other spec uses.
func unique(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, testCount.Add(1))
}

// recordedEvent is one event the recorder received.
type recordedEvent struct {
	regarding runtime.Object
	eventType string
	reason    string
	note      string
}

// eventRecorder keeps the events it receives.
type eventRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (r *eventRecorder) Eventf(regarding, _ runtime.Object, eventType, reason, _, note string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{regarding: regarding, eventType: eventType, reason: reason, note: fmt.Sprintf(note, args...)})
}

func (r *eventRecorder) withReason(reason string) []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var list []recordedEvent
	for _, e := range r.events {
		if e.reason == reason {
			list = append(list, e)
		}
	}
	return list
}

// gauge returns the value of the series metric of registry carrying at least labels, and
// whether it exists.
func gauge(registry *prometheus.Registry, metric string, labels map[string]string) (float64, bool) {
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, f := range families {
		if f.GetName() != metric {
			continue
		}
	series:
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue series
				}
			}
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

// createNamespace creates a namespace carrying labels, or leaves it as it is when it exists.
func createNamespace(name string, labels map[string]string) {
	err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}})
	if !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

// newAlternative creates an ImageAlternative selecting the namespaces labelled with its own
// name, so that the pods of other specs never count.
func newAlternative(name string, mutate ...func(*kuikv1alpha1.ImageAlternative)) *kuikv1alpha1.ImageAlternative {
	ia := &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kuikv1alpha1.ImageAlternativeSpec{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{testLabel: name}},
			Alternatives: []kuikv1alpha1.Alternative{
				{Repository: "quay.io/thanos/thanos"},
				{Repository: "ghcr.io/thanos-io/thanos"},
			},
		},
	}
	for _, m := range mutate {
		m(ia)
	}
	Expect(k8sClient.Create(ctx, ia)).To(Succeed())
	return ia
}

// container is one container of a pod under construction and the record the webhook left.
type container struct {
	name, image string
	rewrite     *podrecord.Rewrite
	offering    []string
}

// rewrittenBy is a thanos container ia rewrote to its ghcr.io copy under OnFailure, still
// running it.
func rewrittenBy(ia string) container {
	return container{name: "thanos", image: ghcrThanosImage, rewrite: &podrecord.Rewrite{
		By: routing.KindImageAlternative + "/" + ia, Origin: thanosImage, RewrittenTo: ghcrThanosImage, Policy: string(kuikv1alpha1.RewritePolicyOnFailure),
	}}
}

// exhaustedBy is a container no candidate served, offered by ia.
func exhaustedBy(ia, name, origin string) container {
	return container{name: name, image: origin, offering: []string{routing.KindImageAlternative + "/" + ia}}
}

// plain is an app container running thanos that no annotation names.
func plain() container {
	return container{name: appContainer, image: thanosImage}
}

// createPod creates a pod in namespace with the annotations its containers need.
func createPod(namespace, name string, containers ...container) *corev1.Pod {
	rewrites := map[string]podrecord.Rewrite{}
	noAlternatives := map[string][]string{}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Annotations: map[string]string{}}}
	for _, c := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: c.name, Image: c.image})
		if c.rewrite != nil {
			rewrites[c.name] = *c.rewrite
		}
		if c.offering != nil {
			noAlternatives[c.name] = c.offering
		}
	}
	if len(rewrites) > 0 {
		value, err := json.Marshal(rewrites)
		Expect(err).NotTo(HaveOccurred())
		pod.Annotations[podrecord.AnnotationRewrites] = string(value)
	}
	if len(noAlternatives) > 0 {
		value, err := json.Marshal(noAlternatives)
		Expect(err).NotTo(HaveOccurred())
		pod.Annotations[podrecord.AnnotationNoAlternatives] = string(value)
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

func setPhase(pod *corev1.Pod, phase corev1.PodPhase) {
	pod.Status.Phase = phase
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

func createSecret(namespace, name string, secretType corev1.SecretType) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Type: secretType}
	if secretType == corev1.SecretTypeDockerConfigJson {
		secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

// withSecretRef makes the second entry of ia read with the Secret name.
func withSecretRef(name string) func(*kuikv1alpha1.ImageAlternative) {
	return func(ia *kuikv1alpha1.ImageAlternative) {
		ia.Spec.Alternatives[1].Auth = &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}
	}
}

func statusOf(name string) kuikv1alpha1.ImageAlternativeStatus {
	var ia kuikv1alpha1.ImageAlternative
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &ia)).To(Succeed())
	return ia.Status
}

func readyOf(name string) *metav1.Condition {
	return meta.FindStatusCondition(statusOf(name).Conditions, kuikv1alpha1.ConditionReady)
}

func podsTracked(name string) int32 {
	s := statusOf(name)
	if s.Pods == nil {
		return -1
	}
	return s.Pods.Tracked
}

var _ = Describe("ImageAlternative Controller", func() {
	var (
		registry   *prometheus.Registry
		recorder   *eventRecorder
		reconciler *ImageAlternativeReconciler
		name       string
	)

	BeforeEach(func() {
		createNamespace(installNamespace, nil)
		registry = prometheus.NewRegistry()
		recorder = &eventRecorder{}
		var err error
		reconciler, err = NewImageAlternativeReconciler(k8sClient, k8sClient.Scheme(), ImageAlternativeOptions{
			APIReader:                k8sClient,
			ClusterResourceNamespace: installNamespace,
			Recorder:                 recorder,
			Registerer:               registry,
		})
		Expect(err).NotTo(HaveOccurred())
		reconciler.Elected(time.Now().Add(-time.Minute))
		name = unique("ia")
	})

	reconcileIt := func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		Expect(err).NotTo(HaveOccurred())
	}

	// selectedNamespace creates a namespace the resource under test selects.
	selectedNamespace := func() string {
		ns := unique("ns")
		createNamespace(ns, map[string]string{testLabel: name})
		return ns
	}

	Describe("Ready", func() {
		It("sets Ready True with reason IsReady for a resource whose secretRefs resolve", func() {
			secret := unique("creds")
			createSecret(installNamespace, secret, corev1.SecretTypeDockerConfigJson)
			newAlternative(name, withSecretRef(secret))
			reconcileIt()
			Expect(readyOf(name).Status).To(Equal(metav1.ConditionTrue))
			Expect(readyOf(name).Reason).To(Equal(kuikv1alpha1.ReasonIsReady))
		})

		It("sets Ready False with reason InvalidConfig for a selector that does not parse", func() {
			newAlternative(name, func(ia *kuikv1alpha1.ImageAlternative) {
				ia.Spec.PodSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: appContainer, Operator: "Bogus", Values: []string{"web"}},
				}}
			})
			reconcileIt()
			Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
			Expect(readyOf(name).Reason).To(Equal(kuikv1alpha1.ReasonInvalidConfig))
		})

		It("sets Ready False with reason SecretNotFound for a secretRef naming an absent Secret", func() {
			newAlternative(name, withSecretRef(unique("missing")))
			reconcileIt()
			Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
			Expect(readyOf(name).Reason).To(Equal(kuikv1alpha1.ReasonSecretNotFound))
		})

		It("resolves a secretRef in the install namespace only, a Secret of that name elsewhere being not found", func() {
			secret := unique("elsewhere")
			createSecret("default", secret, corev1.SecretTypeDockerConfigJson)
			newAlternative(name, withSecretRef(secret))
			reconcileIt()
			Expect(readyOf(name).Reason).To(Equal(kuikv1alpha1.ReasonSecretNotFound))
		})

		It("sets Ready False with reason SecretMalformed for a secretRef naming a Secret that is not a dockerconfigjson", func() {
			secret := unique("opaque")
			createSecret(installNamespace, secret, corev1.SecretTypeOpaque)
			newAlternative(name, withSecretRef(secret))
			reconcileIt()
			Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
			Expect(readyOf(name).Reason).To(Equal(kuikv1alpha1.ReasonSecretMalformed))
		})

		It("emits ResourceNotReady on the resource and exports kuik_resource_not_ready while Ready is False", func() {
			newAlternative(name, withSecretRef(unique("missing")))
			reconcileIt()
			events := recorder.withReason("ResourceNotReady")
			Expect(events).To(HaveLen(1))
			Expect(events[0].regarding.(*kuikv1alpha1.ImageAlternative).Name).To(Equal(name))
			v, ok := gauge(registry, "kuik_resource_not_ready", map[string]string{
				labelKind: routing.KindImageAlternative, labelName: name, "reason": kuikv1alpha1.ReasonSecretNotFound,
			})
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
		})
	})

	Describe("the routing side", func() {
		It("reports only the live pods its podSelector and namespaceSelector select", func() {
			newAlternative(name, func(ia *kuikv1alpha1.ImageAlternative) {
				ia.Spec.PodSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "skip", Operator: metav1.LabelSelectorOpDoesNotExist},
				}}
			})
			selected := selectedNamespace()
			other := unique("ns")
			createNamespace(other, nil)

			createPod(selected, "counted", plain())
			skipped := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "skipped", Namespace: selected, Labels: map[string]string{"skip": "true"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: appContainer, Image: thanosImage}}},
			}
			Expect(k8sClient.Create(ctx, skipped)).To(Succeed())
			setPhase(createPod(selected, "done", plain()), corev1.PodSucceeded)
			createPod(other, "elsewhere", plain())

			reconcileIt()
			Expect(podsTracked(name)).To(Equal(int32(1)))
		})

		It("leaves static pods out of the routing side", func() {
			newAlternative(name)
			ns := selectedNamespace()
			static := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: ns, Annotations: map[string]string{"kubernetes.io/config.mirror": "a1b2c3"}},
				// The API server requires a node on a mirror pod, as the kubelet always sets it.
				Spec: corev1.PodSpec{NodeName: "node-1", Containers: []corev1.Container{{Name: "etcd", Image: thanosImage}}},
			}
			Expect(k8sClient.Create(ctx, static)).To(Succeed())
			createPod(ns, appContainer, plain())
			reconcileIt()
			Expect(podsTracked(name)).To(Equal(int32(1)))
		})

		It("writes the pods and containers gauges and the anomaly lists from the pods' annotations", func() {
			newAlternative(name)
			ns := selectedNamespace()
			createPod(ns, "web", rewrittenBy(name), exhaustedBy(name, "reloader", reloaderImage))
			reconcileIt()
			s := statusOf(name)
			Expect(*s.Pods).To(Equal(kuikv1alpha1.PodCounts{Tracked: 1, Rewritten: 1}))
			Expect(*s.Containers).To(Equal(kuikv1alpha1.ContainerCounts{Tracked: 2, Rewritten: 1, NoAlternatives: 1}))
			Expect(s.ActiveFallbacks).To(HaveLen(1))
			Expect(s.ActiveFallbacks[0].Image).To(Equal(thanosImage))
			Expect(s.ActiveFallbacks[0].RewrittenTo).To(Equal(ghcrThanosImage))
			Expect(s.NoAlternatives).To(HaveLen(1))
			Expect(s.NoAlternatives[0].Image).To(Equal(reloaderImage))
		})

		It("sets FallbackActive and AlternativesExhausted from the lists", func() {
			newAlternative(name)
			ns := selectedNamespace()
			createPod(ns, "web", rewrittenBy(name), exhaustedBy(name, "reloader", reloaderImage))
			reconcileIt()
			conditions := statusOf(name).Conditions
			Expect(meta.IsStatusConditionTrue(conditions, kuikv1alpha1.ConditionFallbackActive)).To(BeTrue())
			Expect(meta.IsStatusConditionTrue(conditions, kuikv1alpha1.ConditionAlternativesExhausted)).To(BeTrue())
		})

		It("emits the pod events of the pods it reports on", func() {
			newAlternative(name)
			ns := selectedNamespace()
			createPod(ns, "web", rewrittenBy(name))
			reconcileIt()
			events := recorder.withReason("ImageFallback")
			Expect(events).To(HaveLen(1))
			Expect(events[0].regarding.(*corev1.Pod).Name).To(Equal("web"))
		})

		It("exports the routing series of the resource", func() {
			newAlternative(name)
			ns := selectedNamespace()
			createPod(ns, "web", rewrittenBy(name))
			reconcileIt()
			v, ok := gauge(registry, "kuik_routing_pods_tracked", map[string]string{labelKind: routing.KindImageAlternative, labelName: name})
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
			_, ok = gauge(registry, "kuik_fallback_active_pods", map[string]string{labelKind: routing.KindImageAlternative, labelName: name, "image": thanosImage})
			Expect(ok).To(BeTrue())
		})
	})

	Describe("bounded lists", func() {
		It("caps a list over 500 entries, records it in truncated and sets ListCapacityPressure", func() {
			newAlternative(name)
			ns := selectedNamespace()
			containers := make([]container, 0, 501)
			for i := range 501 {
				containers = append(containers, exhaustedBy(name, fmt.Sprintf("c%d", i), fmt.Sprintf("quay.io/acme/gone%d:1", i)))
			}
			createPod(ns, "many", containers...)
			reconcileIt()
			s := statusOf(name)
			Expect(s.NoAlternatives).To(HaveLen(500))
			Expect(s.Truncated).To(Equal(map[string]int32{"noAlternatives": 1}))
			pressure := meta.FindStatusCondition(s.Conditions, kuikv1alpha1.ConditionListCapacityPressure)
			Expect(pressure).NotTo(BeNil())
			Expect(pressure.Reason).To(Equal(kuikv1alpha1.ReasonListTruncated))
		})
	})

	Describe("writing the status", func() {
		It("skips the write when the status did not change", func() {
			newAlternative(name)
			createPod(selectedNamespace(), "web", plain())
			reconcileIt()
			var before kuikv1alpha1.ImageAlternative
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &before)).To(Succeed())
			Expect(before.Status.Pods).NotTo(BeNil(), "the first reconcile writes the status")
			reconcileIt()
			var after kuikv1alpha1.ImageAlternative
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &after)).To(Succeed())
			Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
		})

		It("removes the series of a deleted resource", func() {
			ia := newAlternative(name)
			createPod(selectedNamespace(), "web", rewrittenBy(name))
			reconcileIt()
			_, exported := gauge(registry, "kuik_fallback_active_pods", map[string]string{labelKind: routing.KindImageAlternative, labelName: name})
			Expect(exported).To(BeTrue(), "the reconcile exports the series")
			Expect(k8sClient.Delete(ctx, ia)).To(Succeed())
			reconcileIt()
			for _, metric := range []string{"kuik_routing_pods_tracked", "kuik_fallback_active_pods", "kuik_status_list_entries"} {
				_, ok := gauge(registry, metric, map[string]string{labelKind: routing.KindImageAlternative, labelName: name})
				Expect(ok).To(BeFalse(), metric)
			}
		})
	})

	Describe("with a manager", func() {
		// start runs the reconciler in a manager of its own, writing statuses interval after a
		// pod or namespace change.
		start := func(interval time.Duration) {
			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme:                 scheme.Scheme,
				Metrics:                metricsserver.Options{BindAddress: "0"},
				HealthProbeBindAddress: "0",
				Controller:             config.Controller{SkipNameValidation: new(true)},
			})
			Expect(err).NotTo(HaveOccurred())
			r, err := NewImageAlternativeReconciler(mgr.GetClient(), mgr.GetScheme(), ImageAlternativeOptions{
				APIReader:                mgr.GetAPIReader(),
				ClusterResourceNamespace: installNamespace,
				Recorder:                 recorder,
				Registerer:               prometheus.NewRegistry(),
			})
			Expect(err).NotTo(HaveOccurred())
			r.StatusInterval = interval
			Expect(r.SetupWithManager(mgr)).To(Succeed())
			managerCtx, cancel := context.WithCancel(ctx)
			DeferCleanup(cancel)
			go func() {
				defer GinkgoRecover()
				Expect(mgr.Start(managerCtx)).To(Succeed())
			}()
		}

		It("reports again within the status interval when a selected pod is created", func() {
			newAlternative(name)
			ns := selectedNamespace()
			start(time.Second)
			Eventually(func() int32 { return podsTracked(name) }).Should(BeZero())
			createPod(ns, "web", plain())
			Eventually(func() int32 { return podsTracked(name) }).WithTimeout(10 * time.Second).Should(Equal(int32(1)))
		})

		It("reports again when a selected pod is deleted or becomes terminal", func() {
			newAlternative(name)
			ns := selectedNamespace()
			gone := createPod(ns, "gone", plain())
			done := createPod(ns, "done", plain())
			start(time.Second)
			Eventually(func() int32 { return podsTracked(name) }).WithTimeout(10 * time.Second).Should(Equal(int32(2)))
			Expect(k8sClient.Delete(ctx, gone)).To(Succeed())
			setPhase(done, corev1.PodSucceeded)
			Eventually(func() int32 { return podsTracked(name) }).WithTimeout(10 * time.Second).Should(BeZero())
		})

		It("reports again when the labels of a namespace change which pods it selects", func() {
			newAlternative(name)
			ns := unique("ns")
			createNamespace(ns, nil)
			createPod(ns, "web", plain())
			start(time.Second)
			Eventually(func() int32 { return podsTracked(name) }).Should(BeZero())
			var namespace corev1.Namespace
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ns}, &namespace)).To(Succeed())
			namespace.Labels = map[string]string{testLabel: name}
			Expect(k8sClient.Update(ctx, &namespace)).To(Succeed())
			Eventually(func() int32 { return podsTracked(name) }).WithTimeout(10 * time.Second).Should(Equal(int32(1)))
		})

		It("reports again at once when its spec changes", func() {
			ia := newAlternative(name, func(ia *kuikv1alpha1.ImageAlternative) {
				ia.Spec.PodSelector = &metav1.LabelSelector{MatchLabels: map[string]string{appContainer: "nothing"}}
			})
			createPod(selectedNamespace(), "web", plain())
			// An interval no spec waits for: only the spec change can trigger the report.
			start(time.Hour)
			Eventually(func() int32 { return podsTracked(name) }).Should(BeZero())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, ia)).To(Succeed())
			ia.Spec.PodSelector = nil
			Expect(k8sClient.Update(ctx, ia)).To(Succeed())
			Eventually(func() int32 { return podsTracked(name) }).WithTimeout(10 * time.Second).Should(Equal(int32(1)))
		})

		It("sets Ready back to IsReady once the missing Secret is created", func() {
			secret := unique("late")
			newAlternative(name, withSecretRef(secret))
			start(time.Hour)
			Eventually(func() string {
				if c := readyOf(name); c != nil {
					return c.Reason
				}
				return ""
			}).Should(Equal(kuikv1alpha1.ReasonSecretNotFound))
			createSecret(installNamespace, secret, corev1.SecretTypeDockerConfigJson)
			Eventually(func() string { return readyOf(name).Reason }).WithTimeout(10 * time.Second).Should(Equal(kuikv1alpha1.ReasonIsReady))
		})
	})
})
