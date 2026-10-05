package secretsyncer

import (
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/auth/pullsecret"
)

const (
	always    = kuikv1alpha1.RewritePolicyAlways
	onFailure = kuikv1alpha1.RewritePolicyOnFailure

	quayGroup = "quay.io/acme"
	ghcrGroup = "ghcr.io/acme"

	// robot is the user every source credential holds unless a spec tells them apart.
	robot = "robot"
)

var _ = Describe("Secret syncer", func() {
	var (
		scope  string
		source string
	)

	BeforeEach(func() {
		scope = unique("scope")
		source = unique("source")
	})

	Context("materialising a pull secret", func() {
		It("creates the Secret in a namespace an Always resource selects, before any pod exists", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
		})

		It("creates the Secret once a pod rewritten by an OnFailure resource exists in the namespace", func() {
			createSource(source, robot, "ghcr.io")
			ns := createNamespace(scope)
			cr := createAlternative(onFailure, scope, group(quayGroup, nil), group(ghcrGroup, injectedAuth(source)))
			start(startOptions{})
			Consistently(users(ns, cr)).WithTimeout(2 * time.Second).Should(BeNil())

			createRewrittenPod(ns, cr, "ghcr.io/acme/foo:1")

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{ghcrGroup: robot}))
		})

		It("takes over the managed fields of a Secret already present under the reserved name", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: auth.InjectedSecretName("ImageAlternative", cr.Name)},
				Type:       corev1.SecretTypeDockerConfigJson,
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: dockerConfigData("squatter", "quay.io")},
			}, client.FieldOwner("someone-else"))).To(Succeed())
			start(startOptions{})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
			Expect(injected(ns, cr).Labels).To(HaveKeyWithValue(pullsecret.LabelManagedBy, pullsecret.ManagedBy))
		})

		It("never reads a Secret outside the install namespace", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
			syncerClient, err := client.New(syncerCfg, client.Options{Scheme: scheme.Scheme})
			Expect(err).NotTo(HaveOccurred())
			for _, verb := range []string{"get", "list", "watch"} {
				review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{
					ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: ns, Verb: verb, Resource: "secrets"},
				}}
				Expect(syncerClient.Create(ctx, review)).To(Succeed())
				Expect(review.Status.Allowed).To(BeFalse(), verb)
			}
		})
	})

	Context("keeping it valid", func() {
		It("re-applies every Secret built from a source Secret when that source is created or changes", func() {
			ns := createNamespace(scope)
			other := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(BeEmpty())

			createSource(source, "first", "quay.io")
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: "first"}))
			Eventually(users(other, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: "first"}))

			updateSource(source, "second", "quay.io")
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: "second"}))
			Eventually(users(other, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: "second"}))
		})

		It("recomputes the Secrets of a resource when its entries change", func() {
			createSource(source, robot, "quay.io", "ghcr.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))

			updateAlternative(cr, func(cr *kuikv1alpha1.ImageAlternative) {
				cr.Spec.Alternatives[1].Auth = injectedAuth(source)
			})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot, ghcrGroup: robot}))
		})

		It("re-applies a pair once the missing source of an entry added to it is created", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))

			// The added entry's source is missing: the Secret is left as it is.
			missing := unique("missing")
			updateAlternative(cr, func(cr *kuikv1alpha1.ImageAlternative) {
				cr.Spec.Alternatives[1].Auth = injectedAuth(missing)
			})
			Eventually(failedEvents(cr)).WithTimeout(timeout).Should(HaveLen(1))

			createSource(missing, "late", "ghcr.io")

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot, ghcrGroup: "late"}))
		})

		It("retries a pair whose source could not be read, rather than writing it without that credential", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			var failed atomic.Bool
			start(startOptions{wrap: func(c client.Client) client.Client {
				return failingGet{Client: c, fail: func(key client.ObjectKey, obj client.Object) error {
					if _, ok := obj.(*corev1.Secret); ok && key.Name == source && failed.CompareAndSwap(false, true) {
						return apierrors.NewServiceUnavailable("transient")
					}
					return nil
				}}
			}})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
			Expect(failed.Load()).To(BeTrue(), "the first read of the source failed")
			Expect(failedEvents(cr)()).To(BeEmpty())
		})

		It("provisions a namespace entering the scope of an Always resource", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace("")
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})
			Consistently(users(ns, cr)).WithTimeout(2 * time.Second).Should(BeNil())

			setScope(ns, scope)

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
		})

		It("restores a managed field edited by someone else on the periodic re-apply, whatever the cached hash", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{syncPeriod: 2 * time.Second})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))

			edited := injected(ns, cr)
			edited.Data[corev1.DockerConfigJsonKey] = []byte(`{"auths":{}}`)
			Expect(k8sClient.Update(ctx, edited, client.FieldOwner("someone-else"))).To(Succeed())
			Expect(users(ns, cr)()).To(BeEmpty())

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
		})

		It("applies every pair at startup, propagating a source changed while it was stopped", func() {
			createSource(source, "before", "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			first := start(startOptions{})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: "before"}))
			first.stop()

			updateSource(source, "after", "quay.io")
			Consistently(users(ns, cr)).WithTimeout(time.Second).Should(Equal(map[string]string{quayGroup: "before"}))
			start(startOptions{})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: "after"}))
		})
	})

	Context("skipping work", func() {
		It("applies once for a burst of pod events on one pair", func() {
			paths := []string{"one.io/acme", "two.io/acme", "three.io/acme", "four.io/acme"}
			hosts := []string{"one.io", "two.io", "three.io", "four.io"}
			createSource(source, robot, hosts...)
			ns := createNamespace(scope)
			entries := make([]kuikv1alpha1.Alternative, 0, 1+len(paths))
			entries = append(entries, group(quayGroup, nil))
			for _, p := range paths {
				entries = append(entries, group(p, injectedAuth(source)))
			}
			cr := createAlternative(onFailure, scope, entries...)
			syncer := start(startOptions{debounce: 3 * time.Second})

			for _, p := range paths {
				createRewrittenPod(ns, cr, p+"/foo:1")
			}

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(HaveLen(len(paths)))
			Consistently(syncer.allApplies).WithTimeout(2 * time.Second).Should(Equal(1.0))
		})

		It("does not apply on an event that leaves the pair's desired Secret unchanged", func() {
			createSource(source, robot, "ghcr.io")
			ns := createNamespace(scope)
			cr := createAlternative(onFailure, scope, group(quayGroup, nil), group(ghcrGroup, injectedAuth(source)))
			syncer := start(startOptions{})
			createRewrittenPod(ns, cr, "ghcr.io/acme/foo:1")
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{ghcrGroup: robot}))
			Eventually(syncer.allApplies).WithTimeout(timeout).Should(Equal(1.0))

			createRewrittenPod(ns, cr, "ghcr.io/acme/bar:1")

			Consistently(syncer.allApplies).WithTimeout(3 * time.Second).Should(Equal(1.0))
		})
	})

	Context("releasing a credential", func() {
		var now *clock

		BeforeEach(func() {
			now = &clock{now: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}
		})

		It("keeps an entry for the grace period after the last pod using it goes away", func() {
			createSource(source, robot, "quay.io", "ghcr.io")
			ns := createNamespace(scope)
			cr := createAlternative(onFailure, scope, group("docker.io/acme", nil),
				group(quayGroup, injectedAuth(source)), group(ghcrGroup, injectedAuth(source)))
			start(startOptions{clock: now})
			createRewrittenPod(ns, cr, "quay.io/acme/foo:1")
			leaving := createRewrittenPod(ns, cr, "ghcr.io/acme/foo:1")
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(HaveLen(2))

			Expect(k8sClient.Delete(ctx, leaving)).To(Succeed())
			Consistently(users(ns, cr)).WithTimeout(2 * time.Second).Should(HaveLen(2))

			now.Advance(pullsecret.GraceDuration)
			// Any event of the pair reconciles it again.
			createRewrittenPod(ns, cr, "quay.io/acme/bar:1")

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
		})

		It("empties the Secret rather than deleting it when no entry is left", func() {
			createSource(source, robot, "ghcr.io")
			ns := createNamespace(scope)
			cr := createAlternative(onFailure, scope, group(quayGroup, nil), group(ghcrGroup, injectedAuth(source)))
			start(startOptions{clock: now})
			pod := createRewrittenPod(ns, cr, "ghcr.io/acme/foo:1")
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(HaveLen(1))

			now.Advance(pullsecret.GraceDuration)
			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())

			Eventually(func() string {
				secret := injected(ns, cr)
				if secret == nil {
					return "deleted"
				}
				return string(secret.Data[corev1.DockerConfigJsonKey])
			}).WithTimeout(timeout).Should(Equal(`{"auths":{}}`))
		})

		It("empties the Secret of a namespace leaving the scope of an Always resource", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(HaveLen(1))

			setScope(ns, "")

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{}))
		})

		It("empties the Secrets of a resource that stops injecting or moves from Always to OnFailure", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			start(startOptions{})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(HaveLen(1))

			updateAlternative(cr, func(cr *kuikv1alpha1.ImageAlternative) {
				cr.Spec.RewritePolicy = onFailure
			})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{}))
		})

		It("forgets the pairs of a deleted namespace", func() {
			createSource(source, robot, "quay.io", "ghcr.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
			syncer := start(startOptions{})
			Eventually(users(ns, cr)).WithTimeout(timeout).Should(HaveLen(1))
			applied := syncer.allApplies()

			// envtest runs no namespace controller: the namespace stays terminating.
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})).To(Succeed())
			updateAlternative(cr, func(cr *kuikv1alpha1.ImageAlternative) {
				cr.Spec.Alternatives[1].Auth = injectedAuth(source)
			})

			Consistently(syncer.allApplies).WithTimeout(3 * time.Second).Should(Equal(applied))
		})
	})

	Context("reporting", func() {
		It("emits PullSecretInjectionFailed on the resource and writes the other entries", func() {
			createSource(source, robot, "quay.io")
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, injectedAuth(unique("missing"))))
			start(startOptions{})

			Eventually(users(ns, cr)).WithTimeout(timeout).Should(Equal(map[string]string{quayGroup: robot}))
			Eventually(failedEvents(cr)).WithTimeout(timeout).Should(ConsistOf(SatisfyAll(
				HaveField("Type", corev1.EventTypeWarning),
				HaveField("Note", ContainSubstring(pullsecret.ReasonSecretNotFound)),
				HaveField("Note", ContainSubstring(ns)),
			)))
		})

		It("emits it once while the same failure lasts, across re-applies", func() {
			ns := createNamespace(scope)
			cr := createAlternative(always, scope, group(quayGroup, injectedAuth(unique("missing"))), group(ghcrGroup, nil))
			syncer := start(startOptions{syncPeriod: 2 * time.Second})
			Eventually(failedEvents(cr)).WithTimeout(timeout).Should(HaveLen(1))

			Eventually(syncer.allApplies).WithTimeout(timeout).Should(BeNumerically(">=", 3))
			Expect(users(ns, cr)()).To(BeEmpty())
			Expect(failedEvents(cr)()).To(ConsistOf(HaveField("Series", BeNil())))
		})

		DescribeTable("counts each apply by its outcome in kuik_secret_applies_total",
			func(setup func(ns string, cr *kuikv1alpha1.ImageAlternative), opts startOptions, result string) {
				createSource(source, robot, "quay.io")
				ns := createNamespace(scope)
				cr := createAlternative(always, scope, group(quayGroup, injectedAuth(source)), group(ghcrGroup, nil))
				setup(ns, cr)
				syncer := start(opts)

				Eventually(func() float64 { return syncer.applies(result) }).WithTimeout(timeout).Should(BeNumerically(">=", 1))
			},
			Entry("Applied when the Secret changed",
				func(string, *kuikv1alpha1.ImageAlternative) {}, startOptions{}, "Applied"),
			Entry("Noop when the server stored nothing new",
				func(string, *kuikv1alpha1.ImageAlternative) {}, startOptions{syncPeriod: 2 * time.Second}, "Noop"),
			Entry("Failed when the API server rejected the apply",
				func(ns string, cr *kuikv1alpha1.ImageAlternative) {
					// The type of a Secret is immutable: an apply changing it is refused.
					Expect(k8sClient.Create(ctx, &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: auth.InjectedSecretName("ImageAlternative", cr.Name)},
						Type:       corev1.SecretTypeOpaque,
					})).To(Succeed())
				}, startOptions{}, "Failed"),
		)
	})
})
