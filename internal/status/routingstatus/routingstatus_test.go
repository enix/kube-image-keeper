package routingstatus

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/capped"
)

var _ = Describe("Routing status", func() {
	var (
		registry *prometheus.Registry
		series   metrics
		events   *recorder
		tracker  *Tracker
	)

	BeforeEach(func() {
		registry = prometheus.NewRegistry()
		series = metrics{registry: registry}
		events = &recorder{}
		var err error
		tracker, err = NewTracker(events, registry)
		Expect(err).NotTo(HaveOccurred())
		tracker.Elected(elected)
	})

	report := func(list []*corev1.Pod) kuikv1alpha1.RoutingStatus {
		return tracker.Report(Input{Resource: self, Pods: list, Now: now})
	}
	reportAfter := func(previous kuikv1alpha1.RoutingStatus, list []*corev1.Pod) kuikv1alpha1.RoutingStatus {
		return tracker.Report(Input{Resource: self, Pods: list, Previous: previous, Now: now})
	}

	Describe("the pods and containers gauges", func() {
		It("counts every live pod it is given in pods.tracked", func() {
			status := report(pods(
				pod("a", untouched(containerApp)),
				pod("b", rewritten(containerApp, self, thanos, ghcrThanos, onFailure)),
				pod("c", exhausted(containerApp, reloader, other)),
			))
			Expect(status.Pods.Tracked).To(Equal(int32(3)))
		})

		It("leaves out a static pod, which the webhook never routes", func() {
			static := pod("etcd", untouched(containerApp))
			static.Annotations["kubernetes.io/config.mirror"] = "a1b2c3"
			status := report(pods(static, pod("a", untouched(containerApp))))
			Expect(status.Pods.Tracked).To(Equal(int32(1)))
			Expect(status.Containers.Tracked).To(Equal(int32(1)))
		})

		It("counts in pods.rewritten the pods carrying a standing rewrite by this resource", func() {
			status := report(pods(
				pod("a", rewritten(containerApp, self, thanos, ghcrThanos, onFailure)),
				pod("b", untouched(containerApp)),
			))
			Expect(status.Pods.Rewritten).To(Equal(int32(1)))
		})

		It("counts a pod with several rewritten containers once in pods.rewritten and once per container in containers.rewritten", func() {
			status := report(pods(pod("a",
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				rewritten("proxy", self, proxy, ghcrProxy, always),
			)))
			Expect(status.Pods.Rewritten).To(Equal(int32(1)))
			Expect(status.Containers.Rewritten).To(Equal(int32(2)))
		})

		It("partitions the containers into the five states, containers.tracked being their sum", func() {
			status := report(pods(pod("a",
				untouched("plain"),
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
				stale("sidecar", self, thanosNext),
				exhausted("reloader", reloader, self),
			)))
			Expect(*status.Containers).To(Equal(kuikv1alpha1.ContainerCounts{
				Tracked: 5, Untouched: 1, Rewritten: 1, Conceded: 1, Stale: 1, NoAlternatives: 1,
			}))
		})

		It("counts a container another resource rewrote, conceded or let go stale as untouched", func() {
			status := report(pods(pod("a",
				rewritten("thanos", other, thanos, ghcrThanos, onFailure),
				conceded("proxy", other, proxy, ghcrProxy, internalPxy),
				stale("sidecar", other, thanosNext),
			)))
			Expect(*status.Containers).To(Equal(kuikv1alpha1.ContainerCounts{Tracked: 3, Untouched: 3}))
		})

		It("leaves out of pods.rewritten a pod whose only rewrite by this resource went stale", func() {
			status := report(pods(pod("a", stale(containerApp, self, thanosNext))))
			Expect(status.Pods.Rewritten).To(BeZero())
		})
	})

	Describe("activeFallbacks", func() {
		It("lists the OnFailure rewrites of this resource per origin and rewrittenTo, with the pods carrying them", func() {
			status := report(pods(
				pod("a", rewritten(containerApp, self, thanos, ghcrThanos, onFailure)),
				pod("b", rewritten(containerApp, self, thanos, ghcrThanos, onFailure)),
				pod("c", rewritten(containerApp, self, proxy, ghcrProxy, onFailure)),
			))
			Expect(status.ActiveFallbacks).To(ConsistOf(
				kuikv1alpha1.ActiveFallback{Image: thanos, RewrittenTo: ghcrThanos, Pods: 2, Since: now},
				kuikv1alpha1.ActiveFallback{Image: proxy, RewrittenTo: ghcrProxy, Pods: 1, Since: now},
			))
		})

		It("leaves out an Always rewrite", func() {
			status := report(pods(pod("a", rewritten(containerApp, self, thanos, ghcrThanos, always))))
			Expect(status.ActiveFallbacks).To(BeEmpty())
		})

		It("leaves out a rewrite that went stale", func() {
			status := report(pods(pod("a", stale(containerApp, self, thanosNext))))
			Expect(status.ActiveFallbacks).To(BeEmpty())
		})
	})

	Describe("noAlternatives", func() {
		It("lists per origin the containers no candidate served that this resource offered one for, with their pods", func() {
			status := report(pods(
				pod("a", exhausted(containerApp, reloader, self, other)),
				pod("b", exhausted(containerApp, reloader, other, self)),
				pod("c", exhausted(containerApp, thanos, other)),
			))
			Expect(status.NoAlternatives).To(ConsistOf(
				kuikv1alpha1.NoAlternative{Image: reloader, Pods: 2, Since: now},
			))
		})
	})

	Describe("concededRewrites and staleRewrites", func() {
		It("lists the conceded rewrites of this resource per origin, rewrittenTo and replacedBy", func() {
			status := report(pods(
				pod("a", conceded(containerApp, self, proxy, ghcrProxy, internalPxy)),
				pod("b", conceded(containerApp, self, proxy, ghcrProxy, internalPxy)),
				pod("c", conceded(containerApp, other, thanos, ghcrThanos, thanosNext)),
			))
			Expect(status.ConcededRewrites).To(ConsistOf(
				kuikv1alpha1.ReplacedRewrite{Image: proxy, RewrittenTo: ghcrProxy, ReplacedBy: internalPxy, Pods: 2, Since: now},
			))
		})

		It("lists the stale rewrites of this resource per origin, rewrittenTo and replacedBy", func() {
			status := report(pods(
				pod("a", stale(containerApp, self, thanosNext)),
				pod("b", stale(containerApp, self, ecrThanos)),
			))
			Expect(status.StaleRewrites).To(ConsistOf(
				kuikv1alpha1.ReplacedRewrite{Image: thanos, RewrittenTo: ghcrThanos, ReplacedBy: thanosNext, Pods: 1, Since: now},
				kuikv1alpha1.ReplacedRewrite{Image: thanos, RewrittenTo: ghcrThanos, ReplacedBy: ecrThanos, Pods: 1, Since: now},
			))
		})

		It("reads image from the record's origin, never from the live container", func() {
			status := report(pods(
				pod("a", conceded("proxy", self, proxy, ghcrProxy, internalPxy), stale("thanos", self, thanosNext)),
			))
			Expect(status.ConcededRewrites[0].Image).To(Equal(proxy))
			Expect(status.StaleRewrites[0].Image).To(Equal(thanos))
		})

		It("reads replacedBy from the live container", func() {
			status := report(pods(pod("a", conceded(containerApp, self, proxy, ghcrProxy, "internal.tld/oauth2-proxy:v7.7.1"))))
			Expect(status.ConcededRewrites[0].ReplacedBy).To(Equal(internalPxy))
		})
	})

	Describe("since, on every list", func() {
		earlier := metav1.NewTime(elected.Add(-24 * time.Hour))
		allLists := func() []*corev1.Pod {
			return pods(pod("a",
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
				stale("sidecar", self, thanosNext),
				exhausted("reloader", reloader, self),
			))
		}

		It("stamps a new entry with now", func() {
			status := report(allLists())
			Expect(status.ActiveFallbacks[0].Since).To(Equal(now))
			Expect(status.NoAlternatives[0].Since).To(Equal(now))
			Expect(status.ConcededRewrites[0].Since).To(Equal(now))
			Expect(status.StaleRewrites[0].Since).To(Equal(now))
		})

		It("carries since forward from the previous status for an entry still present", func() {
			previous := report(allLists())
			previous.ActiveFallbacks[0].Since = earlier
			previous.NoAlternatives[0].Since = earlier
			previous.ConcededRewrites[0].Since = earlier
			previous.StaleRewrites[0].Since = earlier
			status := reportAfter(previous, allLists())
			Expect(status.ActiveFallbacks[0].Since).To(Equal(earlier))
			Expect(status.NoAlternatives[0].Since).To(Equal(earlier))
			Expect(status.ConcededRewrites[0].Since).To(Equal(earlier))
			Expect(status.StaleRewrites[0].Since).To(Equal(earlier))
		})

		It("drops an entry no pod accounts for any more", func() {
			previous := report(allLists())
			status := reportAfter(previous, pods(pod("a", untouched(containerApp))))
			Expect(status.ActiveFallbacks).To(BeEmpty())
			Expect(status.NoAlternatives).To(BeEmpty())
			Expect(status.ConcededRewrites).To(BeEmpty())
			Expect(status.StaleRewrites).To(BeEmpty())
		})

		It("counts a pod once in an entry it carries several containers of", func() {
			status := report(pods(pod("a",
				rewritten("one", self, thanos, ghcrThanos, onFailure),
				rewritten("two", self, thanos, ghcrThanos, onFailure),
				exhausted("three", reloader, self),
				exhausted("four", reloader, self),
			)))
			Expect(status.ActiveFallbacks[0].Pods).To(Equal(int32(1)))
			Expect(status.NoAlternatives[0].Pods).To(Equal(int32(1)))
		})
	})

	Describe("SetConditions", func() {
		fallbacks := func(n int) kuikv1alpha1.RoutingStatus {
			var status kuikv1alpha1.RoutingStatus
			for i := range n {
				status.ActiveFallbacks = append(status.ActiveFallbacks, kuikv1alpha1.ActiveFallback{
					Image: fmt.Sprintf("quay.io/acme/app%d:1", i), RewrittenTo: fmt.Sprintf("ghcr.io/acme/app%d:1", i), Pods: 2, Since: now,
				})
				status.NoAlternatives = append(status.NoAlternatives, kuikv1alpha1.NoAlternative{
					Image: fmt.Sprintf("quay.io/acme/gone%d:1", i), Pods: 3, Since: now,
				})
			}
			return status
		}

		It("sets FallbackActive True with reason OriginUnavailable while activeFallbacks is not empty", func() {
			var conditions []metav1.Condition
			SetConditions(&conditions, self.Kind, fallbacks(1), 1)
			c := meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionFallbackActive)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionTrue))
			Expect(c.Reason).To(Equal(kuikv1alpha1.ReasonOriginUnavailable))
		})

		It("sets AlternativesExhausted True with reason AllCandidatesFailed while noAlternatives is not empty", func() {
			var conditions []metav1.Condition
			SetConditions(&conditions, self.Kind, fallbacks(1), 1)
			c := meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionAlternativesExhausted)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionTrue))
			Expect(c.Reason).To(Equal(kuikv1alpha1.ReasonAllCandidatesFailed))
		})

		It("counts the images and pods of the whole list in the message, before any cap", func() {
			var conditions []metav1.Condition
			n := capped.Capacity + 1
			SetConditions(&conditions, self.Kind, fallbacks(n), 1)
			fallback := meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionFallbackActive)
			Expect(fallback.Message).To(And(ContainSubstring(fmt.Sprintf("%d images", n)), ContainSubstring(fmt.Sprintf("%d pods", 2*n))))
			exhaustedCondition := meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionAlternativesExhausted)
			Expect(exhaustedCondition.Message).To(And(ContainSubstring(fmt.Sprintf("%d images", n)), ContainSubstring(fmt.Sprintf("%d pods", 3*n))))
		})

		It("words FallbackActive after the mirror on an ImageMirror and after the fallback on an ImageAlternative", func() {
			var alternative, mirror []metav1.Condition
			SetConditions(&alternative, routing.KindImageAlternative, fallbacks(1), 1)
			SetConditions(&mirror, routing.KindImageMirror, fallbacks(1), 1)
			Expect(meta.FindStatusCondition(alternative, kuikv1alpha1.ConditionFallbackActive).Message).To(Equal("1 image routed to fallback (2 pods)"))
			Expect(meta.FindStatusCondition(mirror, kuikv1alpha1.ConditionFallbackActive).Message).To(Equal("1 image routed to the mirror (2 pods)"))
		})

		It("removes both conditions once their list is empty", func() {
			var conditions []metav1.Condition
			SetConditions(&conditions, self.Kind, fallbacks(1), 1)
			SetConditions(&conditions, self.Kind, kuikv1alpha1.RoutingStatus{}, 1)
			Expect(meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionFallbackActive)).To(BeNil())
			Expect(meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionAlternativesExhausted)).To(BeNil())
		})
	})

	Describe("Cap", func() {
		It("caps the four lists under their status field names", func() {
			var status kuikv1alpha1.RoutingStatus
			const to = "ghcr.io/x:1"
			for i := range capped.Capacity + 1 {
				image := fmt.Sprintf("quay.io/acme/app%d:1", i)
				status.ActiveFallbacks = append(status.ActiveFallbacks, kuikv1alpha1.ActiveFallback{Image: image, RewrittenTo: to, Pods: 1, Since: now})
				status.NoAlternatives = append(status.NoAlternatives, kuikv1alpha1.NoAlternative{Image: image, Pods: 1, Since: now})
				status.ConcededRewrites = append(status.ConcededRewrites, kuikv1alpha1.ReplacedRewrite{Image: image, RewrittenTo: to, ReplacedBy: "internal.tld/x:1", Pods: 1, Since: now})
				status.StaleRewrites = append(status.StaleRewrites, kuikv1alpha1.ReplacedRewrite{Image: image, RewrittenTo: to, ReplacedBy: "quay.io/x:2", Pods: 1, Since: now})
			}
			limiter, err := capped.NewLimiter(prometheus.NewRegistry())
			Expect(err).NotTo(HaveOccurred())
			pass := limiter.Begin(self.Kind, self.Name)
			Cap(pass, &status)
			var conditions []metav1.Condition
			Expect(pass.End(&conditions, 1)).To(Equal(map[string]int32{
				"activeFallbacks": 1, "noAlternatives": 1, "concededRewrites": 1, "staleRewrites": 1,
			}))
			Expect(status.ActiveFallbacks).To(HaveLen(capped.Capacity))
			Expect(status.NoAlternatives).To(HaveLen(capped.Capacity))
			Expect(status.ConcededRewrites).To(HaveLen(capped.Capacity))
			Expect(status.StaleRewrites).To(HaveLen(capped.Capacity))
		})
	})

	Describe("pod events", func() {
		It("emits a Normal ImageFallback for an OnFailure rewrite, naming the origin, the retained reference and the resource", func() {
			report(pods(pod("a", rewritten(containerApp, self, thanos, ghcrThanos, onFailure))))
			list := events.withReason(EventImageFallback)
			Expect(list).To(HaveLen(1))
			Expect(regarding(list)).To(Equal([]string{"a"}))
			Expect(list[0].eventType).To(Equal(corev1.EventTypeNormal))
			Expect(list[0].note).To(And(ContainSubstring(thanos), ContainSubstring(ghcrThanos), ContainSubstring(self.String())))
		})

		It("emits nothing for an Always rewrite", func() {
			report(pods(pod("a", rewritten(containerApp, self, thanos, ghcrThanos, always))))
			Expect(events.events).To(BeEmpty())
		})

		It("emits a Warning NoAlternativeAvailable from the first resource listed, naming every resource that offered one", func() {
			report(pods(pod("a", exhausted(containerApp, reloader, self, other))))
			list := events.withReason(EventNoAlternativeAvailable)
			Expect(list).To(HaveLen(1))
			Expect(list[0].eventType).To(Equal(corev1.EventTypeWarning))
			Expect(list[0].note).To(And(ContainSubstring(reloader), ContainSubstring(self.String()), ContainSubstring(other.String())))
		})

		It("emits no NoAlternativeAvailable from a resource listed after the first", func() {
			report(pods(pod("a", exhausted(containerApp, reloader, other, self))))
			Expect(events.withReason(EventNoAlternativeAvailable)).To(BeEmpty())
		})

		It("emits a Warning RewriteConceded naming the container, the origin, the reference kuik placed, the resource and the image that won", func() {
			report(pods(pod("a", conceded("oauth-proxy", self, proxy, ghcrProxy, internalPxy))))
			list := events.withReason(EventRewriteConceded)
			Expect(list).To(HaveLen(1))
			Expect(list[0].eventType).To(Equal(corev1.EventTypeWarning))
			Expect(list[0].note).To(And(
				ContainSubstring("oauth-proxy"), ContainSubstring(proxy), ContainSubstring(ghcrProxy),
				ContainSubstring(self.String()), ContainSubstring(internalPxy),
			))
		})

		It("emits ImageFallback, NoAlternativeAvailable and RewriteConceded only for pods created after the lease", func() {
			before := pod("before",
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				exhausted("reloader", reloader, self),
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
			)
			before.CreationTimestamp = metav1.NewTime(elected.Add(-1))
			after := pod("after",
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				exhausted("reloader", reloader, self),
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
			)
			report(pods(before, after))
			for _, reason := range []string{EventImageFallback, EventNoAlternativeAvailable, EventRewriteConceded} {
				Expect(regarding(events.withReason(reason))).To(Equal([]string{"after"}), reason)
			}
		})

		It("emits no pod event before the lease is acquired", func() {
			var err error
			tracker, err = NewTracker(events, prometheus.NewRegistry())
			Expect(err).NotTo(HaveOccurred())
			report(pods(pod("a",
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				exhausted("reloader", reloader, self),
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
				stale("sidecar", self, thanosNext),
			)))
			Expect(events.events).To(BeEmpty())
		})

		It("emits each event once per container, not again at the next report", func() {
			list := pods(pod("a",
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				exhausted("reloader", reloader, self),
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
				stale("sidecar", self, thanosNext),
			))
			previous := report(list)
			reportAfter(previous, list)
			Expect(events.events).To(HaveLen(4))
		})

		It("emits ImageFallback, RewriteConceded and RewriteStale only from the resource the record names", func() {
			report(pods(pod("a",
				rewritten("thanos", other, thanos, ghcrThanos, onFailure),
				conceded("proxy", other, proxy, ghcrProxy, internalPxy),
				stale("sidecar", other, thanosNext),
			)))
			Expect(events.events).To(BeEmpty())
		})

		It("emits a Warning RewriteStale on each pod of an entry entering staleRewrites, whenever the pod was created", func() {
			old := pod("old", stale(containerApp, self, thanosNext))
			old.CreationTimestamp = metav1.NewTime(elected.Add(-30 * 24 * time.Hour))
			report(pods(old, pod("new", stale(containerApp, self, thanosNext))))
			list := events.withReason(EventRewriteStale)
			Expect(regarding(list)).To(ConsistOf("old", "new"))
			Expect(list[0].eventType).To(Equal(corev1.EventTypeWarning))
		})

		It("names the container, the origin, the reference kuik placed, the resource and the image the pod runs now in RewriteStale", func() {
			report(pods(pod("a", stale("thanos-sidecar", self, thanosNext))))
			list := events.withReason(EventRewriteStale)
			Expect(list).To(HaveLen(1))
			Expect(list[0].note).To(And(
				ContainSubstring("thanos-sidecar"), ContainSubstring(thanos), ContainSubstring(ghcrThanos),
				ContainSubstring(self.String()), ContainSubstring(thanosNext),
			))
		})

		It("emits RewriteStale for a pod joining an entry already listed", func() {
			first := pod("first", stale(containerApp, self, thanosNext))
			previous := report(pods(first))
			reportAfter(previous, pods(first, pod("second", stale(containerApp, self, thanosNext))))
			Expect(regarding(events.withReason(EventRewriteStale))).To(Equal([]string{"first", "second"}))
		})

		It("re-announces no pod of an entry the previous status already listed, at the first report of a lease", func() {
			list := pods(pod("a", stale(containerApp, self, thanosNext)))
			previous := report(list)

			restarted := &recorder{}
			var err error
			tracker, err = NewTracker(restarted, prometheus.NewRegistry())
			Expect(err).NotTo(HaveOccurred())
			tracker.Elected(now.Time)
			reportAfter(previous, list)
			Expect(restarted.withReason(EventRewriteStale)).To(BeEmpty())
		})
	})

	Describe("the routing series", func() {
		It("exports kuik_routing_containers for each of the five states, zero included", func() {
			report(pods(pod("a", untouched(containerApp))))
			for _, state := range []string{"untouched", "rewritten", "conceded", "stale", "noAlternatives"} {
				v, ok := series.value("kuik_routing_containers", of(map[string]string{"state": state}))
				Expect(ok).To(BeTrue(), state)
				if state == "untouched" {
					Expect(v).To(Equal(1.0))
				} else {
					Expect(v).To(BeZero(), state)
				}
			}
		})

		It("exports kuik_routing_pods_tracked and kuik_routing_pods_rewritten", func() {
			report(pods(
				pod("a", rewritten(containerApp, self, thanos, ghcrThanos, onFailure)),
				pod("b", untouched(containerApp)),
			))
			tracked, _ := series.value("kuik_routing_pods_tracked", of(nil))
			rewrittenPods, _ := series.value("kuik_routing_pods_rewritten", of(nil))
			Expect(tracked).To(Equal(2.0))
			Expect(rewrittenPods).To(Equal(1.0))
		})

		It("exports kuik_fallback_active_pods per origin image, with the pods as value and the host as registry", func() {
			report(pods(
				pod("a", rewritten(containerApp, self, thanos, ghcrThanos, onFailure)),
				pod("b", rewritten(containerApp, self, thanos, ghcrThanos, onFailure)),
			))
			v, ok := series.value("kuik_fallback_active_pods", of(map[string]string{labelImage: thanos, labelRegistry: quay}))
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(2.0))
		})

		It("exports kuik_alternatives_exhausted_pods per origin image", func() {
			report(pods(pod("a", exhausted(containerApp, reloader, other, self))))
			v, ok := series.value("kuik_alternatives_exhausted_pods", of(map[string]string{labelImage: reloader, labelRegistry: quay}))
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
		})

		It("exports kuik_rewrite_conceded_pods per origin image", func() {
			report(pods(pod("a", conceded(containerApp, self, proxy, ghcrProxy, internalPxy))))
			v, ok := series.value("kuik_rewrite_conceded_pods", of(map[string]string{labelImage: proxy, labelRegistry: quay}))
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
		})

		It("exports kuik_rewrite_stale_pods per origin image", func() {
			report(pods(pod("a", stale(containerApp, self, thanosNext))))
			v, ok := series.value("kuik_rewrite_stale_pods", of(map[string]string{labelImage: thanos, labelRegistry: quay}))
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
		})

		It("labels the conceded and stale series with the record's origin, never the live image", func() {
			report(pods(pod("a",
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
				stale("thanos", self, ecrThanos),
			)))
			Expect(series.count("kuik_rewrite_conceded_pods", of(map[string]string{labelImage: internalPxy}))).To(BeZero())
			Expect(series.count("kuik_rewrite_stale_pods", of(map[string]string{labelImage: ecrThanos}))).To(BeZero())
		})

		It("counts distinct pods in a series that several entries of one origin feed", func() {
			report(pods(pod("a",
				rewritten("one", self, thanos, ghcrThanos, onFailure),
				rewritten("two", self, thanos, ecrThanos, onFailure),
			)))
			v, _ := series.value("kuik_fallback_active_pods", of(map[string]string{labelImage: thanos}))
			Expect(v).To(Equal(1.0))
		})

		It("deletes an anomaly series once its anomaly ends", func() {
			previous := report(pods(pod("a", rewritten(containerApp, self, thanos, ghcrThanos, onFailure))))
			reportAfter(previous, pods(pod("a", untouched(containerApp))))
			Expect(series.count("kuik_fallback_active_pods", of(nil))).To(BeZero())
		})

		It("holds every entry, whatever the status cap", func() {
			containers := make([]spec, 0, capped.Capacity+1)
			for i := range capped.Capacity + 1 {
				containers = append(containers, exhausted(fmt.Sprintf("c%d", i), fmt.Sprintf("quay.io/acme/gone%d:1", i), self))
			}
			report(pods(pod("a", containers...)))
			Expect(series.count("kuik_alternatives_exhausted_pods", of(nil))).To(Equal(capped.Capacity + 1))
		})

		It("removes every series of a forgotten resource", func() {
			report(pods(pod("a",
				rewritten("thanos", self, thanos, ghcrThanos, onFailure),
				exhausted("reloader", reloader, self),
				conceded("proxy", self, proxy, ghcrProxy, internalPxy),
				stale("sidecar", self, thanosNext),
			)))
			tracker.Forget(self)
			for _, metric := range []string{
				"kuik_routing_containers", "kuik_routing_pods_tracked", "kuik_routing_pods_rewritten",
				"kuik_fallback_active_pods", "kuik_alternatives_exhausted_pods", "kuik_rewrite_conceded_pods", "kuik_rewrite_stale_pods",
			} {
				Expect(series.count(metric, of(nil))).To(BeZero(), metric)
			}
		})
	})
})
