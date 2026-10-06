package v1

import (
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

const (
	onFailure = kuikv1alpha1.RewritePolicyOnFailure
	always    = kuikv1alpha1.RewritePolicyAlways
)

var _ = Describe("Pod webhook", func() {
	var (
		namespace string
		// origin serves the pods' original images, alt the alternative of the library CR.
		origin, alt *registrytest.Registry
		d           *PodDefaulter
		// library routes origin to alt, OnFailure.
		library kuikv1alpha1.ImageAlternative
	)

	BeforeEach(func() {
		ensureClusterResourceNamespace()
		namespace = newNamespace(map[string]string{teamLabel: "a"})
		origin = newRegistry()
		alt = newRegistry()
		d = newDefaulter(testConfig())
		library = alternative("library", onFailure, group(origin), group(alt))
		d.SetResources([]kuikv1alpha1.ImageAlternative{library}, nil)
	})

	Context("gates", func() {
		It("leaves a static pod untouched: no rewrite, no annotation, no pull secret", func() {
			fail(origin)
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), withAuth(group(alt), secretAuth("alt-creds"))),
			}, nil)
			pod := newPod(namespace, ref(origin))
			pod.Annotations = map[string]string{"kubernetes.io/config.mirror": "static"}
			Expect(admit(d, namespace, pod)).To(Equal(pod))
			Expect(manifestHeads(origin) + manifestHeads(alt)).To(BeZero())
		})

		It("leaves a container with imagePullPolicy Never untouched and routes the others", func() {
			fail(origin)
			pod := newPod(namespace, ref(origin), ref(origin))
			pod.Spec.Containers[1].ImagePullPolicy = corev1.PullNever
			admitted := admit(d, namespace, pod)
			Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(alt)))
			Expect(admitted.Spec.Containers[1].Image).To(Equal(ref(origin)))
			Expect(rewrites(admitted, AnnotationRewrites)).NotTo(HaveKey("app-1"))
			Expect(noAlternatives(admitted)).NotTo(HaveKey("app-1"))
		})
	})

	Context("containers", func() {
		It("routes and records initContainers like containers", func() {
			fail(origin)
			pod := newPod(namespace, ref(origin))
			pod.Spec.InitContainers = []corev1.Container{{Name: "init", Image: ref(origin), ImagePullPolicy: corev1.PullIfNotPresent}}
			admitted := admit(d, namespace, pod)
			Expect(admitted.Spec.InitContainers[0].Image).To(Equal(ref(alt)))
			Expect(rewrites(admitted, AnnotationRewrites)).To(HaveKey("init"))
		})

		It("leaves ephemeralContainers untouched", func() {
			fail(origin)
			pod := newPod(namespace, ref(origin))
			pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug", Image: ref(origin)},
			}}
			admitted := admit(d, namespace, pod)
			Expect(admitted.Spec.EphemeralContainers[0].Image).To(Equal(ref(origin)))
			Expect(rewrites(admitted, AnnotationRewrites)).NotTo(HaveKey("debug"))
		})

		It("routes a digest-pinned image, carrying the digest to the candidate", func() {
			fail(origin)
			digest, err := image.Digest()
			Expect(err).NotTo(HaveOccurred())
			pinned := "/library/nginx@" + digest.String()
			admitted := admit(d, namespace, newPod(namespace, origin.Host()+pinned))
			Expect(admitted.Spec.Containers[0].Image).To(Equal(alt.Host() + pinned))
		})
	})

	Context("probing", func() {
		It("leaves the container and the annotations untouched when the original answers first", func() {
			pod := newPod(namespace, ref(origin))
			Expect(admit(d, namespace, pod)).To(Equal(pod))
		})

		It("rewrites the container to the first candidate that answers", func() {
			fail(origin)
			admitted := admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(alt)))
		})

		It("probes a candidate only once every candidate before it has failed", func() {
			var mu sync.Mutex
			var originAnswered, altAsked time.Time
			origin.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodHead {
					return false
				}
				time.Sleep(200 * time.Millisecond)
				mu.Lock()
				originAnswered = time.Now()
				mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
				return true
			})
			alt.Intercept(func(_ http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodHead {
					mu.Lock()
					altAsked = time.Now()
					mu.Unlock()
				}
				return false
			})
			admit(d, namespace, newPod(namespace, ref(origin)))
			mu.Lock()
			defer mu.Unlock()
			Expect(altAsked).To(BeTemporally(">=", originAnswered))
		})

		It("sends no request past the first candidate that answers", func() {
			fail(origin)
			last := newRegistry()
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), group(alt), group(last)),
			}, nil)
			admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(manifestHeads(last)).To(BeZero())
		})

		It("gives up on a candidate that does not answer within availabilityCheck.timeout", func() {
			delay(origin, 5*time.Second)
			start := time.Now()
			admitted := admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
			Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(alt)))
		})

		It("sends no request when no resource offers a candidate", func() {
			d.SetResources(nil, nil)
			admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(manifestHeads(origin)).To(BeZero())
		})

		It("resolves an image several containers share once", func() {
			fail(origin)
			admit(d, namespace, newPod(namespace, ref(origin), ref(origin)))
			Expect(manifestHeads(origin)).To(Equal(1))
		})

		It("resolves apart two containers of one image whose pull policies give different candidate lists", func() {
			destination := newRegistry()
			mirrorRef := mirrored(destination, origin)
			d.SetResources(nil, []kuikv1alpha1.ImageMirror{imageMirror("prod-mirror", always, destination)})
			pod := newPod(namespace, ref(origin), ref(origin))
			pod.Spec.Containers[1].ImagePullPolicy = corev1.PullAlways
			admitted := admit(d, namespace, pod)
			Expect(admitted.Spec.Containers[0].Image).To(Equal(mirrorRef))
			Expect(admitted.Spec.Containers[1].Image).To(Equal(ref(origin)))
		})

		It("probes the distinct images of a pod concurrently", func() {
			origin.Push("library/redis:8", image)
			delay(origin, 400*time.Millisecond)
			start := time.Now()
			admit(d, namespace, newPod(namespace, ref(origin), origin.Host()+"/library/redis:8"))
			Expect(time.Since(start)).To(BeNumerically("<", 750*time.Millisecond))
		})
	})

	Context("credentials", func() {
		var private *registrytest.Registry

		BeforeEach(func() {
			fail(origin)
			private = newRegistry(registrytest.WithBasicAuth(registryUser, registryPassword))
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), group(private)),
			}, nil)
		})

		It("probes with the pod's imagePullSecrets, read in the namespace of the admission request", func() {
			createDockerConfigSecret(namespace, podCreds, private.Host())
			pod := newPod("", ref(origin))
			pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: podCreds}}
			admitted := admit(d, namespace, pod)
			Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(private)))
		})

		It("never probes with a fallbackAuth credential", func() {
			createDockerConfigSecret(clusterResourceNamespace, "fallback-creds", private.Host())
			cfg := testConfig()
			cfg.FallbackAuth = []auth.FallbackAuthEntry{{
				RepositoryGroup: private.Host(),
				SecretRef:       &kuikv1alpha1.SecretReference{Name: "fallback-creds"},
			}}
			d = newDefaulter(cfg)
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), group(private)),
			}, nil)
			admitted := admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(origin)))
		})

		// Interim until providers are implemented (notes/0010): the spec'd webhook reads the
		// Secret the syncer materialises for the provider.
		It("probes a candidate whose auth is a provider with the credentials that follow it", func() {
			createDockerConfigSecret(namespace, podCreds, private.Host())
			provider := &kuikv1alpha1.Auth{Provider: &kuikv1alpha1.Provider{Name: kuikv1alpha1.ProviderAWS}}
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), withAuth(group(private), provider)),
			}, nil)
			pod := newPod(namespace, ref(origin))
			pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: podCreds}}
			Expect(admit(d, namespace, pod).Spec.Containers[0].Image).To(Equal(ref(private)))
		})
	})

	Context("failing open", func() {
		It("probes a candidate whose declared Secret is missing with the credentials that follow, admitting the pod", func() {
			fail(origin)
			private := newRegistry(registrytest.WithBasicAuth(registryUser, registryPassword))
			createDockerConfigSecret(namespace, podCreds, private.Host())
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), withAuth(group(private), secretAuth("absent"))),
			}, nil)
			pod := newPod(namespace, ref(origin))
			pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: podCreds}}
			Expect(admit(d, namespace, pod).Spec.Containers[0].Image).To(Equal(ref(private)))
		})

		It("leaves a container whose image does not parse untouched and routes the others", func() {
			fail(origin)
			admitted := admit(d, namespace, newPod(namespace, ref(origin), "quay.io/Acme/foo:v1"))
			Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(alt)))
			Expect(admitted.Spec.Containers[1].Image).To(Equal("quay.io/Acme/foo:v1"))
		})
	})

	Context("activeCheckCache", func() {
		It("answers a second admission of the same image within the TTL without a request", func() {
			fail(origin)
			admit(d, namespace, newPod(namespace, ref(origin)))
			admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(manifestHeads(origin)).To(Equal(1))
			Expect(manifestHeads(alt)).To(Equal(1))
		})

		It("probes again once the TTL has elapsed", func() {
			cfg := testConfig()
			cfg.Webhook.AvailabilityCheck.ActiveCheckCache.TTL.Duration = 200 * time.Millisecond
			d.SetConfig(cfg)
			fail(origin)
			admit(d, namespace, newPod(namespace, ref(origin)))
			time.Sleep(300 * time.Millisecond)
			admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(manifestHeads(origin)).To(Equal(2))
		})

		It("collapses concurrent admissions of the same image into one request", func() {
			delay(origin, 300*time.Millisecond)
			var wg sync.WaitGroup
			for range 2 {
				wg.Go(func() {
					defer GinkgoRecover()
					admit(d, namespace, newPod(namespace, ref(origin)))
				})
			}
			wg.Wait()
			Expect(manifestHeads(origin)).To(Equal(1))
		})

		It("does not reuse an answer obtained with other credentials", func() {
			fail(origin)
			private := newRegistry(registrytest.WithBasicAuth(registryUser, registryPassword))
			createDockerConfigSecret(namespace, podCreds, private.Host())
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), group(private)),
			}, nil)
			anonymous := admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(anonymous.Spec.Containers[0].Image).To(Equal(ref(origin)))
			pod := newPod(namespace, ref(origin))
			pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: podCreds}}
			Expect(admit(d, namespace, pod).Spec.Containers[0].Image).To(Equal(ref(private)))
		})
	})

	Context("records on a new container", func() {
		It("records a rewrite in kuik.enix.io/rewrites as by, the normalised origin, rewrittenTo and policy", func() {
			fail(origin)
			alt.Push("library/nginx:latest", image)
			// Without a tag, the image is normalised to latest.
			admitted := admit(d, namespace, newPod(namespace, origin.Host()+"/library/nginx"))
			Expect(rewrites(admitted, AnnotationRewrites)).To(Equal(map[string]Rewrite{
				appContainer: {
					By:          "ImageAlternative/library",
					Origin:      origin.Host() + "/library/nginx:latest",
					RewrittenTo: alt.Host() + "/library/nginx:latest",
					Policy:      "OnFailure",
				},
			}))
		})

		DescribeTable("records the policy of the band the retained candidate comes from",
			func(policy string) {
				if policy == string(onFailure) {
					fail(origin)
				}
				d.SetResources([]kuikv1alpha1.ImageAlternative{
					alternative("library", kuikv1alpha1.RewritePolicy(policy), group(origin), group(alt)),
				}, nil)
				admitted := admit(d, namespace, newPod(namespace, ref(origin)))
				Expect(rewrites(admitted, AnnotationRewrites)[appContainer].Policy).To(Equal(policy))
			},
			Entry("Always, from an Always resource ahead of the original", "Always"),
			Entry("OnFailure, from a candidate behind the original", "OnFailure"),
		)

		It("records nothing when the candidates of an Always resource decline and the original answers", func() {
			fail(alt)
			d.SetResources([]kuikv1alpha1.ImageAlternative{alternative("library", always, group(origin), group(alt))}, nil)
			pod := newPod(namespace, ref(origin))
			Expect(admit(d, namespace, pod)).To(Equal(pod))
		})

		It("records every resource that offered a candidate in kuik.enix.io/no-alternatives when none answers, leaving the image", func() {
			fail(origin)
			fail(alt)
			destination := newRegistry()
			fail(destination)
			d.SetResources([]kuikv1alpha1.ImageAlternative{library},
				[]kuikv1alpha1.ImageMirror{imageMirror("prod-mirror", onFailure, destination)})
			admitted := admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(origin)))
			Expect(noAlternatives(admitted)[appContainer]).To(ConsistOf("ImageAlternative/library", "ImageMirror/prod-mirror"))
			Expect(admitted.Annotations).NotTo(HaveKey(AnnotationRewrites))
		})
	})

	Context("reinvocation and replayed spec", func() {
		It("leaves an intact container untouched, without probing", func() {
			fail(origin)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			origin.Reset()
			alt.Reset()
			// A replica with a cold cache: only the record can keep it from probing.
			other := newDefaulter(testConfig())
			other.SetResources([]kuikv1alpha1.ImageAlternative{library}, nil)
			Expect(admit(other, namespace, first)).To(Equal(first))
			Expect(manifestHeads(origin) + manifestHeads(alt)).To(BeZero())
		})

		It("never resolves a container named in no-alternatives a second time", func() {
			fail(origin)
			fail(alt)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			origin.Reset()
			alt.Reset()
			other := newDefaulter(testConfig())
			other.SetResources([]kuikv1alpha1.ImageAlternative{library}, nil)
			Expect(admit(other, namespace, first)).To(Equal(first))
			Expect(manifestHeads(origin) + manifestHeads(alt)).To(BeZero())
		})

		It("concedes a container whose image differs from its rewrittenTo, moving its entry unchanged to kuik.enix.io/conceded-rewrites", func() {
			fail(origin)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			recorded := rewrites(first, AnnotationRewrites)[appContainer]
			first.Spec.Containers[0].Image = injected
			second := admit(d, namespace, first)
			Expect(second.Spec.Containers[0].Image).To(Equal(injected))
			Expect(rewrites(second, AnnotationConcededRewrites)).To(Equal(map[string]Rewrite{appContainer: recorded}))
			Expect(rewrites(second, AnnotationRewrites)).NotTo(HaveKey(appContainer))
		})

		It("concedes even when the new image is another candidate of the same resource", func() {
			fail(origin)
			last := newRegistry()
			d.SetResources([]kuikv1alpha1.ImageAlternative{
				alternative("library", onFailure, group(origin), group(alt), group(last)),
			}, nil)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			first.Spec.Containers[0].Image = ref(last)
			second := admit(d, namespace, first)
			Expect(rewrites(second, AnnotationConcededRewrites)).To(HaveKey(appContainer))
			Expect(second.Spec.Containers[0].Image).To(Equal(ref(last)))
		})

		It("leaves a conceded container alone on a further round, producing no patch", func() {
			fail(origin)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			first.Spec.Containers[0].Image = injected
			second := admit(d, namespace, first)
			Expect(admit(d, namespace, second)).To(Equal(second))
		})

		It("routes a container another webhook added and leaves the containers already served alone", func() {
			fail(origin)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			served := rewrites(first, AnnotationRewrites)[appContainer]
			first.Spec.Containers = append(first.Spec.Containers, corev1.Container{
				Name: "sidecar", Image: ref(origin), ImagePullPolicy: corev1.PullIfNotPresent,
			})
			second := admit(d, namespace, first)
			Expect(second.Spec.Containers[1].Image).To(Equal(ref(alt)))
			Expect(rewrites(second, AnnotationRewrites)).To(HaveKeyWithValue(appContainer, served))
			Expect(rewrites(second, AnnotationRewrites)).To(HaveKey("sidecar"))
		})

		It("drops the entry of a container no longer in the pod", func() {
			fail(origin)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			first.Spec.Containers = []corev1.Container{{Name: "other", Image: injected}}
			second := admit(d, namespace, first)
			Expect(second.Annotations).NotTo(HaveKey(AnnotationRewrites))
		})

		It("names each container in at most one of the three annotations", func() {
			fail(origin)
			origin.Push("library/redis:8", image)
			// app and app-2 are rewritten, app-1 finds no candidate: alt has no redis.
			first := admit(d, namespace, newPod(namespace, ref(origin), origin.Host()+"/library/redis:8", ref(origin)))
			first.Spec.Containers[2].Image = injected
			second := admit(d, namespace, first)

			rewritten := rewrites(second, AnnotationRewrites)
			conceded := rewrites(second, AnnotationConcededRewrites)
			exhausted := noAlternatives(second)
			Expect(rewritten).To(HaveLen(1))
			Expect(exhausted).To(HaveLen(1))
			Expect(conceded).To(HaveLen(1))
			for name := range rewritten {
				Expect(conceded).NotTo(HaveKey(name))
				Expect(exhausted).NotTo(HaveKey(name))
			}
			for name := range conceded {
				Expect(exhausted).NotTo(HaveKey(name))
			}
		})
	})

	Context("injected pull secret", func() {
		BeforeEach(func() {
			fail(origin)
			createDockerConfigSecret(clusterResourceNamespace, "alt-creds", alt.Host())
		})

		// injecting routes origin to alt with a secretRef, injectPullSecret unset.
		injecting := func() kuikv1alpha1.ImageAlternative {
			return alternative("library", onFailure, group(origin), withAuth(group(alt), secretAuth("alt-creds")))
		}

		DescribeTable("appends kuik-inject-<kind>-<name> when the retained candidate's auth injects",
			func(kind string) {
				if kind == "ImageAlternative" {
					d.SetResources([]kuikv1alpha1.ImageAlternative{injecting()}, nil)
				} else {
					destination := newRegistry()
					mirrored(destination, origin)
					createDockerConfigSecret(clusterResourceNamespace, "mirror-creds", destination.Host())
					m := imageMirror("prod-mirror", onFailure, destination)
					m.Spec.Destination.Pull = &kuikv1alpha1.DestinationCredentials{Auth: *secretAuth("mirror-creds")}
					d.SetResources(nil, []kuikv1alpha1.ImageMirror{m})
				}
				admitted := admit(d, namespace, newPod(namespace, ref(origin)))
				name := "library"
				if kind == "ImageMirror" {
					name = "prod-mirror"
				}
				Expect(pullSecrets(admitted)).To(ConsistOf(auth.InjectedSecretName(kind, name)))
			},
			Entry("for an ImageAlternative entry with a secretRef and injectPullSecret unset", "ImageAlternative"),
			Entry("for an ImageMirror destination.pull", "ImageMirror"),
		)

		DescribeTable("appends nothing when the retained candidate's auth does not inject",
			func(form string) {
				a := injectPullSecret(secretAuth("alt-creds"), false)
				if form == "provider" {
					a = &kuikv1alpha1.Auth{Provider: &kuikv1alpha1.Provider{Name: kuikv1alpha1.ProviderAWS}}
				}
				d.SetResources([]kuikv1alpha1.ImageAlternative{
					alternative("library", onFailure, group(origin), withAuth(group(alt), a)),
				}, nil)
				admitted := admit(d, namespace, newPod(namespace, ref(origin)))
				Expect(admitted.Spec.Containers[0].Image).To(Equal(ref(alt)))
				Expect(pullSecrets(admitted)).To(BeEmpty())
			},
			Entry("a secretRef with injectPullSecret false", "secretRef"),
			Entry("a provider with injectPullSecret unset", "provider"),
		)

		It("appends one name per resource, however many containers it serves", func() {
			d.SetResources([]kuikv1alpha1.ImageAlternative{injecting()}, nil)
			admitted := admit(d, namespace, newPod(namespace, ref(origin), ref(origin)))
			Expect(pullSecrets(admitted)).To(Equal([]string{auth.InjectedSecretName("ImageAlternative", "library")}))
		})

		It("removes the injected name of a conceded resource that serves no other container", func() {
			d.SetResources([]kuikv1alpha1.ImageAlternative{injecting()}, nil)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			first.Spec.Containers[0].Image = injected
			Expect(pullSecrets(admit(d, namespace, first))).To(BeEmpty())
		})

		It("keeps the injected name while another container of the conceded resource still needs it", func() {
			d.SetResources([]kuikv1alpha1.ImageAlternative{injecting()}, nil)
			first := admit(d, namespace, newPod(namespace, ref(origin), ref(origin)))
			first.Spec.Containers[1].Image = injected
			Expect(pullSecrets(admit(d, namespace, first))).To(Equal([]string{auth.InjectedSecretName("ImageAlternative", "library")}))
		})

		It("restores the injected name a replayed spec lost", func() {
			d.SetResources([]kuikv1alpha1.ImageAlternative{injecting()}, nil)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			first.Spec.ImagePullSecrets = nil
			Expect(pullSecrets(admit(d, namespace, first))).To(Equal([]string{auth.InjectedSecretName("ImageAlternative", "library")}))
		})

		It("never removes a pull secret the pod declared itself", func() {
			d.SetResources([]kuikv1alpha1.ImageAlternative{injecting()}, nil)
			pod := newPod(namespace, ref(origin))
			pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "own"}}
			first := admit(d, namespace, pod)
			first.Spec.Containers[0].Image = injected
			Expect(pullSecrets(admit(d, namespace, first))).To(Equal([]string{"own"}))
		})
	})

	Context("metrics", func() {
		It("counts a rewrite in kuik_routing_rewrites_total by kind, name and policy", func() {
			fail(origin)
			counter := RewritesTotal.WithLabelValues("ImageAlternative", "library", "OnFailure")
			before := testutil.ToFloat64(counter)
			admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(testutil.ToFloat64(counter)).To(Equal(before + 1))
		})

		It("counts a container no candidate served once per offering resource in kuik_routing_alternatives_exhausted_total", func() {
			fail(origin)
			fail(alt)
			destination := newRegistry()
			fail(destination)
			d.SetResources([]kuikv1alpha1.ImageAlternative{library},
				[]kuikv1alpha1.ImageMirror{imageMirror("prod-mirror", onFailure, destination)})
			alternativeCounter := AlternativesExhaustedTotal.WithLabelValues("ImageAlternative", "library")
			mirrorCounter := AlternativesExhaustedTotal.WithLabelValues("ImageMirror", "prod-mirror")
			alternativeBefore, mirrorBefore := testutil.ToFloat64(alternativeCounter), testutil.ToFloat64(mirrorCounter)
			admit(d, namespace, newPod(namespace, ref(origin)))
			Expect(testutil.ToFloat64(alternativeCounter)).To(Equal(alternativeBefore + 1))
			Expect(testutil.ToFloat64(mirrorCounter)).To(Equal(mirrorBefore + 1))
		})

		It("counts nothing again on a reinvocation", func() {
			fail(origin)
			origin.Push("library/redis:8", image)
			rewritten := RewritesTotal.WithLabelValues("ImageAlternative", "library", "OnFailure")
			exhausted := AlternativesExhaustedTotal.WithLabelValues("ImageAlternative", "library")
			first := admit(d, namespace, newPod(namespace, ref(origin), origin.Host()+"/library/redis:8"))
			rewrittenBefore, exhaustedBefore := testutil.ToFloat64(rewritten), testutil.ToFloat64(exhausted)
			admit(d, namespace, first)
			Expect(testutil.ToFloat64(rewritten)).To(Equal(rewrittenBefore))
			Expect(testutil.ToFloat64(exhausted)).To(Equal(exhaustedBefore))
		})
	})

	Context("logs", func() {

		It("tags every line of an admission with its request UID, namespace and pod name", func() {
			fail(origin)
			pod := newPod(namespace, ref(origin))
			pod.Name = "web"
			_, lines := admitLogged(d, namespace, pod)
			Expect(lines).NotTo(BeEmpty())
			for _, line := range lines {
				Expect(line).To(HaveKeyWithValue("requestID", string(requestUID)))
				Expect(line).To(HaveKeyWithValue("namespace", namespace))
				Expect(line).To(HaveKeyWithValue("pod", "web"))
			}
		})

		It("names the pod by its generateName when it has no name yet", func() {
			fail(origin)
			_, lines := admitLogged(d, namespace, newPod(namespace, ref(origin)))
			Expect(lines).NotTo(BeEmpty())
			for _, line := range lines {
				Expect(line).To(HaveKeyWithValue("generateName", "routed-"))
				Expect(line).NotTo(HaveKey("pod"))
			}
		})

		It("tags the lines about one container with the container name and its image", func() {
			fail(origin)
			origin.Push("library/redis:8", image)
			redis := origin.Host() + "/library/redis:8"
			_, lines := admitLogged(d, namespace, newPod(namespace, ref(origin), redis))
			Expect(decision(lines, appContainer)).To(HaveKeyWithValue("image", ref(origin)))
			Expect(decision(lines, "app-1")).To(HaveKeyWithValue("image", redis))
		})

		It("logs a rewrite under OnFailure at Info, with the resource, the policy, the origin, the candidate and the reason the origin failed", func() {
			fail(origin)
			admitted, lines := admitLogged(d, namespace, newPod(namespace, ref(origin)))
			line := decision(lines, appContainer)
			Expect(line).To(HaveKeyWithValue("msg", msgRewrote))
			Expect(line).To(HaveKeyWithValue("level", "info"))
			Expect(line).To(HaveKeyWithValue("resource", "ImageAlternative/library"))
			Expect(line).To(HaveKeyWithValue("policy", "OnFailure"))
			Expect(line).To(HaveKeyWithValue("origin", rewrites(admitted, AnnotationRewrites)[appContainer].Origin))
			Expect(line).To(HaveKeyWithValue("candidate", ref(alt)))
			Expect(line).To(HaveKeyWithValue("reason", string(kuikv1alpha1.CheckManifestNotFound)))
		})

		It("logs a rewrite under OnFailure with no reason when the origin was demoted and never probed", func() {
			demoted := group(origin)
			demoted.Unavailable = true
			d.SetResources([]kuikv1alpha1.ImageAlternative{alternative("library", onFailure, demoted, group(alt))}, nil)
			_, lines := admitLogged(d, namespace, newPod(namespace, ref(origin)))
			Expect(manifestHeads(origin)).To(BeZero())
			line := decision(lines, appContainer)
			Expect(line).To(HaveKeyWithValue("msg", msgRewrote))
			Expect(line).To(HaveKeyWithValue("policy", "OnFailure"))
			Expect(line).NotTo(HaveKey("reason"))
		})

		It("logs the same rewrite line, reason included, when activeCheckCache answers instead of a probe", func() {
			fail(origin)
			_, first := admitLogged(d, namespace, newPod(namespace, ref(origin)))
			heads := manifestHeads(origin) + manifestHeads(alt)
			_, second := admitLogged(d, namespace, newPod(namespace, ref(origin)))
			Expect(manifestHeads(origin) + manifestHeads(alt)).To(Equal(heads))
			without := func(line map[string]any) map[string]any {
				delete(line, "ts")
				return line
			}
			Expect(without(decision(second, appContainer))).To(Equal(without(decision(first, appContainer))))
			Expect(decision(second, appContainer)).To(HaveKeyWithValue("reason", string(kuikv1alpha1.CheckManifestNotFound)))
		})

		It("logs a rewrite under Always at V(1) only, with no reason", func() {
			d.SetResources([]kuikv1alpha1.ImageAlternative{alternative("library", always, group(origin), group(alt))}, nil)
			_, lines := admitLogged(d, namespace, newPod(namespace, ref(origin)))
			line := decision(lines, appContainer)
			Expect(line).To(HaveKeyWithValue("msg", msgRewrote))
			Expect(line).To(HaveKeyWithValue("level", "debug"))
			Expect(line).To(HaveKeyWithValue("policy", "Always"))
			Expect(line).NotTo(HaveKey("reason"))
		})

		It("logs the original answering at V(1) only, under Always as under OnFailure", func() {
			fail(alt)
			for _, policy := range []kuikv1alpha1.RewritePolicy{always, onFailure} {
				d := newDefaulter(testConfig())
				d.SetResources([]kuikv1alpha1.ImageAlternative{alternative("library", policy, group(origin), group(alt))}, nil)
				_, lines := admitLogged(d, namespace, newPod(namespace, ref(origin)))
				line := decision(lines, appContainer)
				Expect(line).To(HaveKeyWithValue("msg", msgKept), "under %s", policy)
				Expect(line).To(HaveKeyWithValue("level", "debug"), "under %s", policy)
			}
		})

		It("logs a container no candidate served at Info, with every resource that offered one", func() {
			fail(origin)
			fail(alt)
			destination := newRegistry()
			fail(destination)
			d.SetResources([]kuikv1alpha1.ImageAlternative{library},
				[]kuikv1alpha1.ImageMirror{imageMirror("prod-mirror", onFailure, destination)})
			_, lines := admitLogged(d, namespace, newPod(namespace, ref(origin)))
			line := decision(lines, appContainer)
			Expect(line).To(HaveKeyWithValue("msg", msgNoCandidate))
			Expect(line).To(HaveKeyWithValue("level", "info"))
			Expect(line["resources"]).To(ConsistOf("ImageAlternative/library", "ImageMirror/prod-mirror"))
		})

		It("logs a rewrite conceded on a reinvocation at Info, with the resource, the policy, the origin, the reference kuik placed and the image that replaced it", func() {
			fail(origin)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			entry := rewrites(first, AnnotationRewrites)[appContainer]
			first.Spec.Containers[0].Image = injected
			_, lines := admitLogged(d, namespace, first)
			line := decision(lines, appContainer)
			Expect(line).To(HaveKeyWithValue("msg", msgConceded))
			Expect(line).To(HaveKeyWithValue("level", "info"))
			Expect(line).To(HaveKeyWithValue("resource", entry.By))
			Expect(line).To(HaveKeyWithValue("policy", entry.Policy))
			Expect(line).To(HaveKeyWithValue("origin", entry.Origin))
			Expect(line).To(HaveKeyWithValue("candidate", entry.RewrittenTo))
			Expect(line).To(HaveKeyWithValue("image", injected))
		})

		It("logs no decision again for a container already recorded", func() {
			fail(origin)
			// alt has no redis: app-1 lands in no-alternatives, app in rewrites.
			origin.Push("library/redis:8", image)
			first := admit(d, namespace, newPod(namespace, ref(origin), origin.Host()+"/library/redis:8"))
			Expect(rewrites(first, AnnotationRewrites)).To(HaveKey(appContainer))
			Expect(noAlternatives(first)).To(HaveKey("app-1"))
			_, lines := admitLogged(d, namespace, first)
			Expect(decisions(lines)).To(BeEmpty())
		})

		It("logs the decision of a container another webhook added between two rounds", func() {
			fail(origin)
			first := admit(d, namespace, newPod(namespace, ref(origin)))
			first.Spec.Containers = append(first.Spec.Containers, corev1.Container{
				Name: "sidecar", Image: ref(origin), ImagePullPolicy: corev1.PullIfNotPresent,
			})
			_, lines := admitLogged(d, namespace, first)
			Expect(decisions(lines)).To(ConsistOf(HaveKeyWithValue("container", "sidecar")))
		})
	})

	Context("through the API server", func() {
		// create creates a pod through the API server, which calls the served webhook.
		create := func(pod *corev1.Pod) *corev1.Pod {
			GinkgoHelper()
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pod))).To(Succeed()) })
			return pod
		}
		// apply creates a routing resource, deleted when the spec ends.
		apply := func(obj client.Object) {
			GinkgoHelper()
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed()) })
		}
		// routedImage creates a pod of ref(origin) in namespace and returns its image.
		routedImage := func(namespace string) func() string {
			return func() string { return create(newPod(namespace, ref(origin))).Spec.Containers[0].Image }
		}

		It("routes a pod with the resources as last changed", func() {
			fail(origin)
			cr := alternative("served-library", onFailure, group(origin), group(alt))
			apply(&cr)
			Eventually(routedImage(namespace)).Should(Equal(ref(alt)))

			last := newRegistry()
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&cr), &cr)).To(Succeed())
			cr.Spec.Alternatives = []kuikv1alpha1.Alternative{group(origin), group(last)}
			Expect(k8sClient.Update(ctx, &cr)).To(Succeed())
			Eventually(routedImage(namespace)).Should(Equal(ref(last)))
		})

		It("selects resources on the labels of the pod's namespace", func() {
			fail(origin)
			routed := newNamespace(map[string]string{teamLabel: "routed"})
			cr := alternative("served-library", onFailure, group(origin), group(alt))
			cr.Spec.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{teamLabel: "routed"}}
			apply(&cr)
			Eventually(routedImage(routed)).Should(Equal(ref(alt)))
			Expect(routedImage(namespace)()).To(Equal(ref(origin)))
		})

		It("never rewrites a pod on update", func() {
			fail(origin)
			pod := create(newPod(namespace, "other.tld/untouched:v1"))
			cr := alternative("served-library", onFailure, group(origin), group(alt))
			apply(&cr)
			Eventually(routedImage(namespace)).Should(Equal(ref(alt)))

			pod.Spec.Containers[0].Image = ref(origin)
			Expect(k8sClient.Update(ctx, pod)).To(Succeed())
			Expect(pod.Spec.Containers[0].Image).To(Equal(ref(origin)))
		})

		It("applies a reloaded webhook config to the next admission", func() {
			destination := newRegistry()
			mirrorRef := mirrored(destination, origin)
			m := imageMirror("served-mirror", always, destination)
			apply(&m)
			DeferCleanup(func() { served.SetConfig(testConfig()) })
			pullAlways := func() string {
				pod := newPod(namespace, ref(origin))
				pod.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways
				return create(pod).Spec.Containers[0].Image
			}
			// The mirror is demoted behind the original, which answers.
			Consistently(pullAlways, time.Second).Should(Equal(ref(origin)))

			cfg := testConfig()
			cfg.Webhook.DemoteMirrorWithPullPolicyAlways = false
			served.SetConfig(cfg)
			Eventually(pullAlways).Should(Equal(mirrorRef))
		})

		It("writes nothing to the API server besides the patch it returns", func() {
			fail(origin)
			createDockerConfigSecret(clusterResourceNamespace, "alt-creds", alt.Host())
			cr := alternative("served-library", onFailure, group(origin), withAuth(group(alt), secretAuth("alt-creds")))
			apply(&cr)
			Eventually(routedImage(namespace)).Should(Equal(ref(alt)))

			var secrets corev1.SecretList
			Expect(k8sClient.List(ctx, &secrets, client.InNamespace(namespace))).To(Succeed())
			Expect(secrets.Items).To(BeEmpty())
			var events corev1.EventList
			Expect(k8sClient.List(ctx, &events, client.InNamespace(namespace))).To(Succeed())
			Expect(events.Items).To(BeEmpty())
		})
	})
})
