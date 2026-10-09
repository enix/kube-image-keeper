//go:build smoke

package smoke

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

// The pod label each test CR selects on, on top of the namespace label scopeLabel.
const routingLabel = "kuik-test/routing"

type rewritesBeforeKey struct{}

// TestRouting creates its resources in the test namespaces, every kuik CR scoped twice
// (namespace label and pod label), and deletes them once every test passed. The feature
// names carry the test numbers of the report: 1 is TestInstall.
func TestRouting(t *testing.T) {
	g := NewWithT(t)
	ctx, cfg := context.Background(), testenv.EnvConf()
	g.Expect(refuseLeftovers(ctx, cfg)).To(Succeed())
	g.Expect(checkPullAuth(ctx, cfg)).To(Succeed())
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Test resources left in place for the diagnosis; run task smoke-cleanup when done.")
			return
		}
		g.Expect(cleanup(ctx, cfg)).To(Succeed())
	})
	testenv.Test(t, noCR(), fallback(), imageMirror(), pullSecret(), missingSecret(), privatePull())
}

// checkPullAuth refuses a --pull-auth the smoke test could delete: a Secret it labelled
// itself, cleanup would take it for a test resource.
func checkPullAuth(ctx context.Context, cfg *envconf.Config) error {
	if (*privateRepo == "") != (*pullAuth == "") {
		return fmt.Errorf("--private-repo and --pull-auth go together")
	}
	if *pullAuth == "" {
		return nil
	}
	var secret corev1.Secret
	if err := cfg.Client().Resources().Get(ctx, *pullAuth, *kuikNamespace, &secret); err != nil {
		return fmt.Errorf("--pull-auth %s in %s: %w", *pullAuth, *kuikNamespace, err)
	}
	if secret.Labels[ownerLabel] == labelTrue {
		return fmt.Errorf("--pull-auth %s carries %s: cleanup would delete it", *pullAuth, ownerLabel)
	}
	return nil
}

// scoped is the spec of a test ImageAlternative: it selects only the pods of the test
// namespace labelled routingLabel=value.
func scoped(
	value string, policy kuikv1alpha1.RewritePolicy, alternatives ...kuikv1alpha1.Alternative,
) kuikv1alpha1.ImageAlternativeSpec {
	return kuikv1alpha1.ImageAlternativeSpec{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{scopeLabel: labelTrue}},
		PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{routingLabel: value}},
		RewritePolicy:     policy,
		Alternatives:      alternatives,
	}
}

// Test 2.
func noCR() features.Feature {
	return features.New("test 2: a pod no kuik CR selects").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			g := NewWithT(t)
			ensureNamespaces(ctx, g, cfg)
			create(ctx, g, cfg, testPod(testNamespace, "no-cr", nginx, nil))
			return ctx
		}).
		Assess("keeps its image and gets no kuik annotation", expectPodUntouched(testNamespace, "no-cr", nginx)).
		Feature()
}

// Tests 3, 4, 5 and 7: one ImageAlternative whose origin never resolves, a pod it reroutes,
// a pod whose origin answers, a pod outside its namespaces, and the status it reports.
func fallback() features.Feature {
	alternative := &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: "kuik-test-nginx"},
		Spec: scoped("alternative", kuikv1alpha1.RewritePolicyOnFailure,
			kuikv1alpha1.Alternative{Repository: "kuik-test.invalid/nginx/nginx-unprivileged"},
			kuikv1alpha1.Alternative{Repository: "quay.io/nginx/nginx-unprivileged"}),
	}
	routed := map[string]string{routingLabel: "alternative"}
	return features.New("tests 3, 4, 5 and 7: an ImageAlternative whose origin never resolves").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			g := NewWithT(t)
			ensureNamespaces(ctx, g, cfg)
			before := rewritesTotal(ctx, g, alternative.Name)
			create(ctx, g, cfg, alternative)
			create(ctx, g, cfg, testPod(testNamespace, "fallback", unresolvable, routed))
			create(ctx, g, cfg, testPod(testNamespace, "origin-up", nginx, routed))
			create(ctx, g, cfg, testPod(unlabeledNamespace, "out-of-scope", unresolvable, routed))
			return context.WithValue(ctx, rewritesBeforeKey{}, before)
		}).
		Assess("test 3: rewrites the selected pod to the alternative and records it", expectFallback(alternative.Name)).
		Assess("test 3: counts exactly one rewrite over the webhook replicas", expectOneRewrite(alternative.Name)).
		Assess("test 4: leaves a selected pod whose origin answers alone",
			expectPodUntouched(testNamespace, "origin-up", nginx)).
		Assess("test 5: leaves a pod outside the selected namespaces alone", expectOutOfScope).
		Assess("test 7: reports the fallback in its status and as a pod event", expectFallbackStatus(alternative.Name)).
		Feature()
}

func expectFallback(name string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		g := NewWithT(t)
		pod := getPod(ctx, g, cfg, testNamespace, "fallback")
		waitPodReady(ctx, g, cfg, pod)
		pod = getPod(ctx, g, cfg, testNamespace, "fallback")
		expectImage(ctx, g, pod, nginx)
		g.Expect(pod.Annotations).To(
			HaveKeyWithValue(podrecord.AnnotationRewrites, ContainSubstring("ImageAlternative/"+name)))
		return ctx
	}
}

func expectOneRewrite(name string) features.Func {
	return func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
		g := NewWithT(t)
		before, ok := ctx.Value(rewritesBeforeKey{}).(float64)
		g.Expect(ok).To(BeTrue(), "the rewrite count before the test is missing")
		g.Expect(rewritesTotal(ctx, g, name)-before).To(BeEquivalentTo(1), "growth of kuik_routing_rewrites_total")
		return ctx
	}
}

func expectOutOfScope(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	g := NewWithT(t)
	waitFor(g, 90*time.Second, func(g Gomega) {
		pod := getPod(ctx, g, cfg, unlabeledNamespace, "out-of-scope")
		g.Expect(pod.Status.ContainerStatuses).To(ContainElement(HaveField("State.Waiting",
			HaveValue(HaveField("Reason", BeElementOf("ErrImagePull", "ImagePullBackOff"))))))
	}, "out-of-scope failing its pull")
	pod := getPod(ctx, g, cfg, unlabeledNamespace, "out-of-scope")
	expectImage(ctx, g, pod, unresolvable)
	expectUntouched(g, pod)
	return ctx
}

func expectFallbackStatus(name string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		g := NewWithT(t)
		var live kuikv1alpha1.ImageAlternative
		waitFor(g, 60*time.Second, func(g Gomega) {
			g.Expect(cfg.Client().Resources().Get(ctx, name, "", &live)).To(Succeed())
			g.Expect(meta.IsStatusConditionTrue(live.Status.Conditions, "FallbackActive")).To(BeTrue())
		}, "FallbackActive True on %s", name)
		g.Expect(readyReason(&live)).To(Equal("IsReady"), "reason of the Ready condition of %s", name)
		g.Expect(live.Status.ActiveFallbacks).To(ContainElement(HaveField("RewrittenTo", nginx)))
		waitFor(g, 60*time.Second, func(g Gomega) {
			g.Expect(podEvent(ctx, testNamespace, "fallback", "ImageFallback")).To(BeTrue())
		}, "ImageFallback event on pod fallback")
		return ctx
	}
}

// Test 6: an ImageMirror whose destination never answers, so nothing is ever served from it.
func imageMirror() features.Feature {
	mirror := &kuikv1alpha1.ImageMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "kuik-test-mirror"},
		Spec: kuikv1alpha1.ImageMirrorSpec{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{scopeLabel: labelTrue}},
			PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{routingLabel: "mirror"}},
			RewritePolicy:     kuikv1alpha1.RewritePolicyAlways,
			Destination:       kuikv1alpha1.MirrorDestination{Path: "registry.invalid/kuik-test"},
		},
	}
	return features.New("test 6: an ImageMirror whose destination never answers").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			g := NewWithT(t)
			ensureNamespaces(ctx, g, cfg)
			create(ctx, g, cfg, mirror)
			create(ctx, g, cfg, testPod(testNamespace, "mirror", nginx, map[string]string{routingLabel: "mirror"}))
			return ctx
		}).
		Assess("leaves the pod on its origin, without a rewrite", expectMirrorUnused).
		Feature()
}

func expectMirrorUnused(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	g := NewWithT(t)
	pod := getPod(ctx, g, cfg, testNamespace, "mirror")
	waitPodReady(ctx, g, cfg, pod)
	pod = getPod(ctx, g, cfg, testNamespace, "mirror")
	expectImage(ctx, g, pod, nginx)
	g.Expect(pod.Annotations).NotTo(HaveKey(podrecord.AnnotationRewrites))
	return ctx
}

// Tests 8 and 10: an Always ImageAlternative whose entry declares a placeholder Secret.
func pullSecret() features.Feature {
	const injected = "kuik-inject-imagealternative-kuik-test-pull-secret"
	placeholder := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kuik-test-quay-creds", Namespace: *kuikNamespace},
		Type:       corev1.SecretTypeDockerConfigJson,
		// Placeholder, not a credential: no pod uses it, so it never reaches a pull.
		StringData: map[string]string{
			corev1.DockerConfigJsonKey: `{"auths":{"quay.io":{"username":"kuik-test","password":"placeholder"}}}`,
		},
	}
	withAuth := &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: "kuik-test-pull-secret"},
		Spec: scoped("pull-secret", kuikv1alpha1.RewritePolicyAlways,
			kuikv1alpha1.Alternative{Repository: "kuik-test-secret.invalid/nginx/nginx-unprivileged"},
			withSecret("quay.io/nginx/nginx-unprivileged", placeholder.Name)),
	}
	return features.New("tests 8 and 10: an Always ImageAlternative with credentials").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			g := NewWithT(t)
			ensureNamespaces(ctx, g, cfg)
			create(ctx, g, cfg, placeholder)
			create(ctx, g, cfg, withAuth)
			return ctx
		}).
		Assess("test 8: gets its pull Secret in the selected namespaces only, before any pod",
			expectPullSecretProvisioned(withAuth.Name, injected)).
		Assess("test 10: loses its pull Secret once deleted", expectPullSecretDeleted(withAuth, injected)).
		Feature()
}

func expectPullSecretProvisioned(name, injected string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		g := NewWithT(t)
		secret := waitSecret(ctx, g, cfg, testNamespace, injected)
		g.Expect(secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
		err := cfg.Client().Resources().Get(ctx, injected, unlabeledNamespace, &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"%s in %s: %v, expected not found", injected, unlabeledNamespace, err)
		waitReadyReason(ctx, g, cfg, name, "IsReady", 30*time.Second)
		return ctx
	}
}

func expectPullSecretDeleted(alternative *kuikv1alpha1.ImageAlternative, injected string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		g := NewWithT(t)
		g.Expect(cfg.Client().Resources().Delete(ctx, alternative)).To(Succeed())
		waitFor(g, 60*time.Second, func(g Gomega) {
			err := cfg.Client().Resources().Get(ctx, injected, testNamespace, &corev1.Secret{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%s: %v", injected, err)
		}, "%s deleted from %s", injected, testNamespace)
		return ctx
	}
}

// Test 9: an ImageAlternative whose source Secret never exists.
func missingSecret() features.Feature {
	missing := &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: "kuik-test-missing-secret"},
		Spec: scoped("missing-secret", kuikv1alpha1.RewritePolicyAlways,
			kuikv1alpha1.Alternative{Repository: "kuik-test-missing.invalid/nginx/nginx-unprivileged"},
			withSecret("quay.io/nginx/nginx-unprivileged", "kuik-test-absent-creds")),
	}
	return features.New("test 9: an ImageAlternative whose source Secret is missing").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			g := NewWithT(t)
			ensureNamespaces(ctx, g, cfg)
			create(ctx, g, cfg, missing)
			return ctx
		}).
		Assess("reports it in Ready and as a PullSecretInjectionFailed event", expectSecretNotFound(missing.Name)).
		Feature()
}

func expectSecretNotFound(name string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		g := NewWithT(t)
		waitReadyReason(ctx, g, cfg, name, "SecretNotFound", 60*time.Second)
		waitFor(g, 60*time.Second, func(g Gomega) {
			g.Expect(resourceEvent(ctx, name, "PullSecretInjectionFailed")).To(BeTrue())
		}, "PullSecretInjectionFailed event on %s", name)
		return ctx
	}
}

// Test 11: a real pull from the user's private repository, with the credentials kuik copies
// from --pull-auth. Skipped without --private-repo.
func privatePull() features.Feature {
	return features.New("test 11: an ImageAlternative to a private repository").
		Assess("pulls the image with the credentials it injects", expectPrivatePull).
		Feature()
}

func expectPrivatePull(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	const injected = "kuik-inject-imagealternative-kuik-test-private"
	if *privateRepo == "" {
		t.Skip("no --private-repo and --pull-auth")
	}
	g := NewWithT(t)
	ensureNamespaces(ctx, g, cfg)
	// Built here, after the flags are parsed.
	create(ctx, g, cfg, &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: "kuik-test-private"},
		Spec: scoped("private", kuikv1alpha1.RewritePolicyAlways,
			kuikv1alpha1.Alternative{Repository: "kuik-test-private.invalid/nginx/nginx-unprivileged"},
			withSecret(*privateRepo, *pullAuth)),
	})
	waitSecret(ctx, g, cfg, testNamespace, injected)
	pod := testPod(testNamespace, "private", "kuik-test-private.invalid/nginx/nginx-unprivileged:1.31.6-alpine",
		map[string]string{routingLabel: "private"})
	// A cached image would skip the pull this test is about.
	pod.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways
	create(ctx, g, cfg, pod)
	waitPodReady(ctx, g, cfg, pod)
	pod = getPod(ctx, g, cfg, testNamespace, "private")
	expectImage(ctx, g, pod, *privateRepo+":1.31.6-alpine")
	g.Expect(pod.Spec.ImagePullSecrets).To(ContainElement(HaveField("Name", injected)))
	waitFor(g, 30*time.Second, func(g Gomega) {
		g.Expect(podEvent(ctx, testNamespace, "private", "Pulled")).To(BeTrue())
	}, "Pulled event on pod private")
	return ctx
}

func withSecret(repository, secret string) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{
		Repository: repository,
		Auth:       &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: secret}},
	}
}

func expectPodUntouched(namespace, name, image string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		g := NewWithT(t)
		pod := getPod(ctx, g, cfg, namespace, name)
		waitPodReady(ctx, g, cfg, pod)
		pod = getPod(ctx, g, cfg, namespace, name)
		expectImage(ctx, g, pod, image)
		expectUntouched(g, pod)
		return ctx
	}
}

// expectImage checks container 0 of pod; the failure carries the webhook log lines about it.
func expectImage(ctx context.Context, g Gomega, pod *corev1.Pod, image string) {
	g.Expect(pod.Spec.Containers[0].Image).To(Equal(image),
		func() string { return fmt.Sprintf("image of pod %s%s", pod.Name, webhookLogs(ctx, pod.Name)) })
}

func expectUntouched(g Gomega, pod *corev1.Pod) {
	g.Expect(pod.Annotations).NotTo(HaveKey(podrecord.AnnotationRewrites), "pod %s", pod.Name)
	g.Expect(pod.Annotations).NotTo(HaveKey(podrecord.AnnotationNoAlternatives), "pod %s", pod.Name)
}

// readyReason is the reason of the Ready condition of an ImageAlternative, empty without one.
func readyReason(alternative *kuikv1alpha1.ImageAlternative) string {
	if ready := meta.FindStatusCondition(alternative.Status.Conditions, "Ready"); ready != nil {
		return ready.Reason
	}
	return ""
}

func waitReadyReason(ctx context.Context, g Gomega, cfg *envconf.Config, name, reason string, timeout time.Duration) {
	waitFor(g, timeout, func(g Gomega) {
		var live kuikv1alpha1.ImageAlternative
		g.Expect(cfg.Client().Resources().Get(ctx, name, "", &live)).To(Succeed())
		g.Expect(readyReason(&live)).To(Equal(reason))
	}, "Ready %s on %s", reason, name)
}

func waitSecret(ctx context.Context, g Gomega, cfg *envconf.Config, namespace, name string) *corev1.Secret {
	var secret corev1.Secret
	waitFor(g, 60*time.Second, func(g Gomega) {
		g.Expect(cfg.Client().Resources().Get(ctx, name, namespace, &secret)).To(Succeed())
	}, "%s in %s", name, namespace)
	return &secret
}
