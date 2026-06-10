package kuik

import (
	"context"
	"fmt"
	"slices"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Regression tests for https://github.com/enix/kube-image-keeper/issues/567.
//
// Scenario: two registries, A (upstream) and B (mirror). After an image is
// mirrored to B and becomes unavailable on A, the webhook rewrites new pods'
// container.Image from A/... to B/.... Those pods still carry the original
// A/... reference in the kuik.enix.io/original-images annotation.
//
// The mirror reconcilers must still treat the original A/... image as "in
// use" for as long as such a rewritten pod exists; otherwise updateUnusedSince
// marks the image unused and the cleanup loop eventually deletes it from B,
// breaking new pulls.
var _ = Describe("Issue #567: rewritten pods keep the original image in use", func() {
	const (
		originalImage  = "a.example.com/test/foo:v1"
		rewrittenImage = "b.example.com/test/foo:v1"
		mirrorPrefix   = "b.example.com"
	)

	ctx := context.Background()

	newRewrittenPod := func(name string) corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Annotations: map[string]string{
					OriginalImagesAnnotation: `{"app":"` + originalImage + `"}`,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: rewrittenImage},
				},
			},
		}
	}

	Context("podsInUseImages (the decoupled in-use signal)", func() {
		It("returns the original image for a rewritten pod, so existing mirror entries stay alive", func() {
			pods := []corev1.Pod{newRewrittenPod("pod-2")}
			got := podsInUseImages(ctx, pods)

			// Both names must appear; updateUnusedSince keys off the original
			// reference stored in the annotation to avoid marking the mirrored
			// image as unused while the rewritten pod still needs it.
			Expect(got).To(HaveKey(originalImage))
			Expect(got).To(HaveKey(rewrittenImage))
		})
	})

	Context("mergePreviousAndCurrentMatchingImages", func() {
		It("leaves unusedSince nil for the original image while a rewritten pod still references it", func() {
			obj := &kuikv1alpha1.ImageSetMirror{
				Spec: kuikv1alpha1.ImageSetMirrorSpec{
					ImageSetMirrorBase: kuikv1alpha1.ImageSetMirrorBase{
						ImageFilter: kuikv1alpha1.ImageFilterDefinition{
							Include: []string{`a\.example\.com/.*`},
						},
						Mirrors: kuikv1alpha1.Mirrors{
							{Registry: "b.example.com"},
						},
					},
				},
				// Prior reconciliation (when pod-1 was still around, unrewritten)
				// already recorded the original image in the status.
				Status: kuikv1alpha1.ImageSetMirrorStatus{
					MatchingImages: []kuikv1alpha1.MatchingImage{
						{
							Image: originalImage,
							Mirrors: []kuikv1alpha1.MirrorStatus{
								{Image: rewrittenImage},
							},
						},
					},
				},
			}

			// pod-1 (unrewritten) is gone; only pod-2 (rewritten to B) remains.
			pods := []corev1.Pod{newRewrittenPod("pod-2")}
			mirrorPrefixes := map[string][]string{"": {mirrorPrefix}}

			_, err := mergePreviousAndCurrentMatchingImages(ctx, pods, obj, mirrorPrefixes, obj.Spec.ImageFilter.MustBuild())
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Status.MatchingImages).To(HaveLen(1))
			Expect(obj.Status.MatchingImages[0].Image).To(Equal(originalImage))
			Expect(obj.Status.MatchingImages[0].UnusedSince).To(BeNil(),
				"unusedSince must stay nil while a rewritten pod still needs the mirrored image")
		})

		It("does not add the original image to matchingImages on a fresh CISM (mirroring is driven by the current image only)", func() {
			// The two concerns are decoupled: rewritten pods must keep an
			// EXISTING mirror entry alive (covered above), but must NOT create
			// new mirror entries for the original reference — only the current
			// container image drives what gets mirrored (d26a099).
			obj := &kuikv1alpha1.ImageSetMirror{
				Spec: kuikv1alpha1.ImageSetMirrorSpec{
					ImageSetMirrorBase: kuikv1alpha1.ImageSetMirrorBase{
						ImageFilter: kuikv1alpha1.ImageFilterDefinition{
							Include: []string{`a\.example\.com/.*`},
						},
						Mirrors: kuikv1alpha1.Mirrors{
							{Registry: "b.example.com"},
						},
					},
				},
			}
			pods := []corev1.Pod{newRewrittenPod("pod-2")}
			mirrorPrefixes := map[string][]string{"": {mirrorPrefix}}

			_, err := mergePreviousAndCurrentMatchingImages(ctx, pods, obj, mirrorPrefixes, obj.Spec.ImageFilter.MustBuild())
			Expect(err).NotTo(HaveOccurred())
			Expect(obj.Status.MatchingImages).To(BeEmpty())
		})
	})
})

// These tests guard the behavior fixed in commit d26a099: when the webhook
// has rewritten a pod's image (original A -> current B), the mirror
// reconcilers must match their filters against B (the current image actually
// in the spec), not A (the original image stored in the
// kuik.enix.io/original-images annotation). The "still in use" signal is
// decoupled and covered separately (see issue #567 regression tests).
var _ = Describe("Mirror controllers match on rewritten, not original image", func() {
	const (
		originalImage  = "docker.io/library/nginx:1.25"
		rewrittenImage = "rewritten.example.com/library/nginx:1.25"
	)

	ctx := context.Background()

	newRewrittenPod := func() corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rewritten-pod",
				Namespace: "default",
				Annotations: map[string]string{
					OriginalImagesAnnotation: `{"app":"` + originalImage + `"}`,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: rewrittenImage},
				},
			},
		}
	}

	Context("normalizedImageNamesFromPod (used by the mirror reconcilers)", func() {
		It("returns only the rewritten container image", func() {
			pod := newRewrittenPod()
			got := slices.Collect(normalizedImageNamesFromPod(&pod))
			Expect(got).To(ConsistOf(rewrittenImage))
			Expect(got).NotTo(ContainElement(originalImage))
		})
	})

	Context("normalizedImageNamesFromAnnotatedPod (used by the availability reconciler and the in-use signal)", func() {
		It("returns both the current and the original image", func() {
			pod := newRewrittenPod()
			got := slices.Collect(normalizedImageNamesFromAnnotatedPod(ctx, &pod))
			Expect(got).To(ConsistOf(rewrittenImage, originalImage))
		})
	})

	Context("podsByNormalizedMatchingImages (mirror matching logic)", func() {
		It("matches the pod on the rewritten image when the filter includes it", func() {
			f := kuikv1alpha1.ImageFilterDefinition{
				Include: []string{`rewritten\.example\.com/.*`},
			}.MustBuild()

			pods := []corev1.Pod{newRewrittenPod()}
			got := podsByNormalizedMatchingImages(ctx, f, nil, pods)
			Expect(got).To(HaveKey(rewrittenImage))
			Expect(got).NotTo(HaveKey(originalImage))
		})

		It("does not match the pod when only the original (pre-rewrite) image matches the filter", func() {
			f := kuikv1alpha1.ImageFilterDefinition{
				Include: []string{`docker\.io/library/nginx:.*`},
			}.MustBuild()

			pods := []corev1.Pod{newRewrittenPod()}
			got := podsByNormalizedMatchingImages(ctx, f, nil, pods)
			Expect(got).To(BeEmpty())
		})
	})
})

// An invalid imageFilter regex must skip the reconcile gracefully (no error, no
// requeue, no mutation) instead of panicking. The CRD's CEL validation already
// rejects such regexes at admission, so this is exercised through a fake client
// that bypasses that validation.
var _ = Describe("Mirror reconcile skips on an invalid image filter", func() {
	ctx := context.Background()
	badFilter := kuikv1alpha1.ImageFilterDefinition{Include: []string{"["}}

	newFakeReconciler := func(obj client.Object) (reconcile.Reconciler, client.Client) {
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(obj).
			Build()
		base := ImageSetMirrorBaseReconciler{Client: c, Scheme: scheme.Scheme, Recorder: events.NewFakeRecorder(10)}
		if _, ok := obj.(*kuikv1alpha1.ClusterImageSetMirror); ok {
			return &ClusterImageSetMirrorReconciler{base}, c
		}
		return &ImageSetMirrorReconciler{base}, c
	}

	DescribeTable("does not panic and leaves the object untouched",
		func(obj client.Object) {
			r, c := newFakeReconciler(obj)
			key := client.ObjectKeyFromObject(obj)

			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(ctrl.Result{}), "an invalid filter must not requeue")

			Expect(c.Get(ctx, key, obj)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(obj, imageSetMirrorFinalizer)).To(BeFalse(),
				"reconcile must skip before any mutation when the filter is invalid")
		},
		Entry("ImageSetMirror", &kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "ism-bad-filter", Namespace: "default"},
			Spec:       kuikv1alpha1.ImageSetMirrorSpec{ImageSetMirrorBase: kuikv1alpha1.ImageSetMirrorBase{ImageFilter: badFilter}},
		}),
		Entry("ClusterImageSetMirror", &kuikv1alpha1.ClusterImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "cism-bad-filter"},
			Spec:       kuikv1alpha1.ClusterImageSetMirrorSpec{ImageSetMirrorBase: kuikv1alpha1.ImageSetMirrorBase{ImageFilter: badFilter}},
		}),
	)
})

// The API server rejects setting both spec.filter and the deprecated
// spec.imageFilter on the same resource (the documented mutual exclusion),
// exercised through the real envtest client which enforces the CEL rule.
var _ = Describe("CRD admission validation", func() {
	ctx := context.Background()

	It("rejects setting both spec.filter and the deprecated spec.imageFilter", func() {
		ism := &kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "ism-both-filters", Namespace: "default"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				ImageSetMirrorBase: kuikv1alpha1.ImageSetMirrorBase{
					ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: []string{"nginx.*"}},
				},
				Filter: kuikv1alpha1.Filter{Include: []kuikv1alpha1.FilterItem{{Label: "app=foo"}}},
			},
		}
		Expect(k8sClient.Create(ctx, ism)).To(MatchError(ContainSubstring("mutually exclusive")))
	})
})

// conflictOnFirstUpdateClient wraps a client.Client and returns a conflict
// error on the first Update call, then delegates to the real client.
type conflictOnFirstUpdateClient struct {
	client.Client
	conflicted bool
}

func (c *conflictOnFirstUpdateClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if !c.conflicted {
		c.conflicted = true
		return errors.NewConflict(
			schema.GroupResource{Group: "kuik.enix.io", Resource: "imagesetmirrors"},
			obj.GetName(),
			fmt.Errorf("simulated conflict"),
		)
	}
	return c.Client.Update(ctx, obj, opts...)
}

// mirrorSpecOpts is the subset of spec knobs the shared scenarios need to set,
// independent of whether the resource is an ImageSetMirror or a
// ClusterImageSetMirror.
type mirrorSpecOpts struct {
	// imageFilter drives the legacy (deprecated) imageFilter mode; mutually
	// exclusive with filter*.
	imageFilter []string
	// filterInclude / filterExclude drive the unified spec.filter mode (items
	// are the namespaced shape; the cluster build wraps them with the namespace
	// axis).
	filterInclude []kuikv1alpha1.FilterItem
	filterExclude []kuikv1alpha1.FilterItem
	mirrors       kuikv1alpha1.Mirrors
	finalizer     bool
}

// clusterFilterItems wraps namespaced filter items into their cluster shape.
func clusterFilterItems(items []kuikv1alpha1.FilterItem) []kuikv1alpha1.ClusterFilterItem {
	if items == nil {
		return nil
	}
	out := make([]kuikv1alpha1.ClusterFilterItem, len(items))
	for i, it := range items {
		out[i] = kuikv1alpha1.ClusterFilterItem{FilterItem: it}
	}
	return out
}

// mirrorKind abstracts ImageSetMirror vs ClusterImageSetMirror so the shared
// reconcile scenarios run against both. The namespaced kind only observes pods
// in its own namespace, so its resource is created in workloadNS; the cluster
// kind is cluster-scoped (no namespace) but still observes pods in workloadNS.
type mirrorKind struct {
	name          string
	slug          string
	build         func(name, workloadNS string, opts mirrorSpecOpts) client.Object
	fresh         func() client.Object
	newReconciler func(c client.Client) reconcile.Reconciler
}

func ismSpec(opts mirrorSpecOpts) kuikv1alpha1.ImageSetMirrorBase {
	return kuikv1alpha1.ImageSetMirrorBase{
		ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: opts.imageFilter},
		Mirrors:     opts.mirrors,
	}
}

func mirrorFinalizers(opts mirrorSpecOpts) []string {
	if opts.finalizer {
		return []string{imageSetMirrorFinalizer}
	}
	return nil
}

var mirrorKinds = []mirrorKind{
	{
		name: "ImageSetMirror",
		slug: "ism",
		build: func(name, workloadNS string, opts mirrorSpecOpts) client.Object {
			return &kuikv1alpha1.ImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: workloadNS, Finalizers: mirrorFinalizers(opts)},
				Spec: kuikv1alpha1.ImageSetMirrorSpec{
					ImageSetMirrorBase: ismSpec(opts),
					Filter:             kuikv1alpha1.Filter{Include: opts.filterInclude, Exclude: opts.filterExclude},
				},
			}
		},
		fresh: func() client.Object { return &kuikv1alpha1.ImageSetMirror{} },
		newReconciler: func(c client.Client) reconcile.Reconciler {
			return &ImageSetMirrorReconciler{ImageSetMirrorBaseReconciler{Client: c, Scheme: c.Scheme(), Recorder: events.NewFakeRecorder(10)}}
		},
	},
	{
		name: "ClusterImageSetMirror",
		slug: "cism",
		build: func(name, workloadNS string, opts mirrorSpecOpts) client.Object {
			return &kuikv1alpha1.ClusterImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{Name: name, Finalizers: mirrorFinalizers(opts)},
				Spec: kuikv1alpha1.ClusterImageSetMirrorSpec{
					ImageSetMirrorBase: ismSpec(opts),
					Filter: kuikv1alpha1.ClusterFilter{
						Include: clusterFilterItems(opts.filterInclude),
						Exclude: clusterFilterItems(opts.filterExclude),
					},
				},
			}
		},
		fresh: func() client.Object { return &kuikv1alpha1.ClusterImageSetMirror{} },
		newReconciler: func(c client.Client) reconcile.Reconciler {
			return &ClusterImageSetMirrorReconciler{ImageSetMirrorBaseReconciler{Client: c, Scheme: c.Scheme(), Recorder: events.NewFakeRecorder(10)}}
		},
	},
}

var _ = Describe("Mirror reconcile (shared across kinds)", func() {
	const (
		nginxImage  = "docker.io/library/nginx:latest"
		nginxFilter = `docker\.io/library/nginx:.*`
		mirrorImage = "mirror.example.com/cache/library/nginx:latest"
	)
	cacheMirror := kuikv1alpha1.Mirrors{{Registry: "mirror.example.com", Path: "cache"}}

	for _, k := range mirrorKinds {
		Describe(k.name, func() {
			ctx := context.Background()
			workloadNS := k.slug + "-shared"

			ensureNamespace := func() {
				err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: workloadNS}})
				if err != nil && !errors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
			}

			create := func(name string, opts mirrorSpecOpts) types.NamespacedName {
				obj := k.build(name, workloadNS, opts)
				Expect(k8sClient.Create(ctx, obj)).To(Succeed())
				key := client.ObjectKeyFromObject(obj)
				DeferCleanup(func() {
					got := k.fresh()
					if err := k8sClient.Get(ctx, key, got); err == nil {
						got.SetFinalizers(nil)
						_ = k8sClient.Update(ctx, got)
						_ = k8sClient.Delete(ctx, got)
					}
				})
				return key
			}

			seed := func(key types.NamespacedName, images []kuikv1alpha1.MatchingImage) {
				got := k.fresh()
				Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
				got.(MirrorObject).MirrorStatus().MatchingImages = images
				Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())
			}

			statusOf := func(key types.NamespacedName) *kuikv1alpha1.ImageSetMirrorStatus {
				got := k.fresh()
				Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
				return got.(MirrorObject).MirrorStatus()
			}

			createPod := func(name, image string, labels, annotations map[string]string) {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: workloadNS, Labels: labels, Annotations: annotations},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
				}
				Expect(k8sClient.Create(ctx, pod)).To(Succeed())
				DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod) })
			}

			doReconcile := func(key types.NamespacedName) {
				_, err := k.newReconciler(k8sClient).Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())
			}

			BeforeEach(ensureNamespace)

			It("reconciles an empty resource without error", func() {
				key := create(k.slug+"-smoke", mirrorSpecOpts{})
				doReconcile(key)
			})

			Context("unusedSince bookkeeping", func() {
				seeded := func() types.NamespacedName {
					key := create(k.slug+"-unused", mirrorSpecOpts{imageFilter: []string{nginxFilter}, mirrors: cacheMirror})
					seed(key, []kuikv1alpha1.MatchingImage{{
						Image:   nginxImage,
						Mirrors: []kuikv1alpha1.MirrorStatus{{Image: mirrorImage}},
					}})
					return key
				}

				It("sets unusedSince when no pod uses the image", func() {
					key := seeded()
					beforeReconcile := time.Now().Truncate(time.Second)

					doReconcile(key)

					status := statusOf(key)
					Expect(status.MatchingImages).To(HaveLen(1))
					Expect(status.MatchingImages[0].UnusedSince).NotTo(BeNil())
					Expect(status.MatchingImages[0].UnusedSince.Time).To(BeTemporally(">=", beforeReconcile))
				})

				It("does not overwrite unusedSince on subsequent reconciliations", func() {
					key := seeded()

					doReconcile(key)
					first := statusOf(key).MatchingImages[0].UnusedSince
					Expect(first).NotTo(BeNil())

					doReconcile(key)
					second := statusOf(key).MatchingImages[0].UnusedSince
					Expect(second).NotTo(BeNil())
					Expect(second.Time).To(BeTemporally("==", first.Time))
				})
			})

			// Regression coverage for commit d26a099: the reconciler must match its
			// ImageFilter against the pod's current (rewritten) image, not the
			// original image stashed in the kuik.enix.io/original-images annotation.
			Context("when a pod's image has been rewritten by the webhook", func() {
				const (
					originalImage  = "docker.io/library/nginx:1.25"
					rewrittenImage = "rewritten.example.com/library/nginx:1.25"
					expectedMirror = "mirror.example.com/cache/library/nginx:1.25"
				)

				It("matches the rewritten image when the filter includes the rewritten registry", func() {
					createPod(k.slug+"-rewritten", rewrittenImage, nil, map[string]string{
						OriginalImagesAnnotation: `{"app":"` + originalImage + `"}`,
					})
					key := create(k.slug+"-rewrite-match", mirrorSpecOpts{
						imageFilter: []string{`rewritten\.example\.com/.*`},
						mirrors:     cacheMirror,
						finalizer:   true,
					})

					By("Pre-seeding status so the mirror loop skips actual image copies")
					mirroredAt := metav1.NewTime(time.Now())
					seed(key, []kuikv1alpha1.MatchingImage{{
						Image:   rewrittenImage,
						Mirrors: []kuikv1alpha1.MirrorStatus{{Image: expectedMirror, MirroredAt: &mirroredAt}},
					}})

					doReconcile(key)

					status := statusOf(key)
					Expect(status.MatchingImages).To(HaveLen(1))
					Expect(status.MatchingImages[0].Image).To(Equal(rewrittenImage))
					Expect(status.MatchingImages[0].UnusedSince).To(BeNil())
				})

				It("does not match the pod when only the original (pre-rewrite) image matches the filter", func() {
					createPod(k.slug+"-rewritten-orig", rewrittenImage, nil, map[string]string{
						OriginalImagesAnnotation: `{"app":"` + originalImage + `"}`,
					})
					key := create(k.slug+"-rewrite-nomatch", mirrorSpecOpts{
						imageFilter: []string{`docker\.io/library/nginx:.*`},
						mirrors:     cacheMirror,
						finalizer:   true,
					})

					doReconcile(key)

					status := statusOf(key)
					Expect(status.MatchingImages).To(BeEmpty())
				})
			})

			// Regression coverage for https://github.com/enix/kube-image-keeper/issues/567.
			// A rewritten pod references only the mirror (B) copy while the status still
			// tracks the original (A) image; that entry must not be marked unused.
			It("keeps the original image in use when a pod references only the mirror copy (issue #567)", func() {
				const (
					originalImage  = "a.example.com/test/foo:v1"
					rewrittenImage = "b.example.com/test/foo:v1"
				)
				createPod(k.slug+"-567", rewrittenImage, nil, map[string]string{
					OriginalImagesAnnotation: `{"app":"` + originalImage + `"}`,
				})
				key := create(k.slug+"-567", mirrorSpecOpts{
					imageFilter: []string{`a\.example\.com/.*`},
					mirrors:     kuikv1alpha1.Mirrors{{Registry: "b.example.com"}},
					finalizer:   true,
				})

				By("Pre-seeding status as it would be after an earlier pod mirrored the image")
				mirroredAt := metav1.NewTime(time.Now())
				seed(key, []kuikv1alpha1.MatchingImage{{
					Image:   originalImage,
					Mirrors: []kuikv1alpha1.MirrorStatus{{Image: rewrittenImage, MirroredAt: &mirroredAt}},
				}})

				doReconcile(key)

				status := statusOf(key)
				var matching *kuikv1alpha1.MatchingImage
				for i := range status.MatchingImages {
					if status.MatchingImages[i].Image == originalImage {
						matching = &status.MatchingImages[i]
						break
					}
				}
				Expect(matching).NotTo(BeNil(), "original image must remain in status.matchingImages")
				Expect(matching.UnusedSince).To(BeNil(),
					"unusedSince must stay nil while a pod still pulls from the mirror (issue #567)")
			})

			Context("pod filters", func() {
				// seedWithFilter folds the nginx image dimension into spec.filter
				// alongside the given pod include/exclude items (imageFilter and
				// filter are mutually exclusive, so the image selector must live in
				// the unified filter).
				seedWithFilter := func(name string, include, exclude []kuikv1alpha1.FilterItem) types.NamespacedName {
					include = append([]kuikv1alpha1.FilterItem{{Image: nginxFilter}}, include...)
					key := create(name, mirrorSpecOpts{
						filterInclude: include,
						filterExclude: exclude,
						mirrors:       cacheMirror,
						finalizer:     true,
					})
					mirroredAt := metav1.NewTime(time.Now())
					seed(key, []kuikv1alpha1.MatchingImage{{
						Image:   nginxImage,
						Mirrors: []kuikv1alpha1.MirrorStatus{{Image: mirrorImage, MirroredAt: &mirroredAt}},
					}})
					return key
				}

				It("drops pods whose labels match an exclude selector", func() {
					key := seedWithFilter(k.slug+"-pf-exclude", nil, []kuikv1alpha1.FilterItem{{Label: "cnpg.io/podRole=instance"}})
					createPod(k.slug+"-pf-excluded", nginxImage, map[string]string{"cnpg.io/podRole": "instance"}, nil)

					doReconcile(key)

					Expect(statusOf(key).MatchingImages[0].UnusedSince).NotTo(BeNil(),
						"excluded-label pod must not keep the image in-use")
				})

				It("keeps pods whose labels don't match an exclude selector", func() {
					key := seedWithFilter(k.slug+"-pf-keep", nil, []kuikv1alpha1.FilterItem{{Label: "cnpg.io/podRole=instance"}})
					createPod(k.slug+"-pf-kept", nginxImage, map[string]string{"app": "foo"}, nil)

					doReconcile(key)

					Expect(statusOf(key).MatchingImages[0].UnusedSince).To(BeNil(),
						"non-matching pod must keep the image in-use")
				})

				It("narrows to pods that match an include label selector", func() {
					key := seedWithFilter(k.slug+"-pf-include", []kuikv1alpha1.FilterItem{{Label: "app=mirror-me"}}, nil)
					createPod(k.slug+"-pf-out", nginxImage, map[string]string{"app": "skip-me"}, nil)

					doReconcile(key)

					Expect(statusOf(key).MatchingImages[0].UnusedSince).NotTo(BeNil(),
						"non-included pod must not keep the image in-use")
				})

				It("supports annotation presence-only includes", func() {
					key := seedWithFilter(k.slug+"-pf-anno", []kuikv1alpha1.FilterItem{{Annotation: "my.company.com/custom-annotation"}}, nil)
					createPod(k.slug+"-pf-no-anno", nginxImage, map[string]string{"app": "foo"}, nil)

					doReconcile(key)

					Expect(statusOf(key).MatchingImages[0].UnusedSince).NotTo(BeNil(),
						"pod missing the required annotation must not keep the image in-use")
				})
			})

			Context("finalizer conflict retries", func() {
				It("retries and succeeds when a conflict occurs while adding the finalizer", func() {
					key := create(k.slug+"-fz-add", mirrorSpecOpts{})

					wrapped := &conflictOnFirstUpdateClient{Client: k8sClient}
					_, err := k.newReconciler(wrapped).Reconcile(ctx, reconcile.Request{NamespacedName: key})
					Expect(err).NotTo(HaveOccurred())

					got := k.fresh()
					Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
					Expect(controllerutil.ContainsFinalizer(got, imageSetMirrorFinalizer)).To(BeTrue())
				})

				It("retries and succeeds when a conflict occurs while removing the finalizer", func() {
					key := create(k.slug+"-fz-remove", mirrorSpecOpts{finalizer: true})

					got := k.fresh()
					Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
					Expect(k8sClient.Delete(ctx, got)).To(Succeed())

					wrapped := &conflictOnFirstUpdateClient{Client: k8sClient}
					_, err := k.newReconciler(wrapped).Reconcile(ctx, reconcile.Request{NamespacedName: key})
					Expect(err).NotTo(HaveOccurred())

					Eventually(func() bool {
						err := k8sClient.Get(ctx, key, k.fresh())
						return errors.IsNotFound(err)
					}, 5*time.Second, time.Millisecond*500).Should(BeTrue())
				})

				It("removes the finalizer on deletion even when the filter is invalid", func() {
					// An unparseable label selector passes CEL admission but fails to
					// compile at runtime. A finalized object that ends up in this state
					// must still be deletable: reconcile handles deletion before
					// compiling the filter, otherwise the finalizer would never be
					// removed and the object would stay stuck in Terminating forever.
					key := create(k.slug+"-fz-bad-filter", mirrorSpecOpts{
						filterInclude: []kuikv1alpha1.FilterItem{{Label: "==="}},
						finalizer:     true,
					})

					got := k.fresh()
					Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
					Expect(k8sClient.Delete(ctx, got)).To(Succeed())

					doReconcile(key)

					Eventually(func() bool {
						err := k8sClient.Get(ctx, key, k.fresh())
						return errors.IsNotFound(err)
					}, 5*time.Second, time.Millisecond*500).Should(BeTrue(),
						"deletion must not depend on a valid filter")
				})
			})
		})
	}
})
