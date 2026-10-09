//go:build smoke

package smoke

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	ctx, cfg := context.Background(), testenv.EnvConf()
	if err := refuseLeftovers(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := checkPullAuth(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Test resources left in place for the diagnosis; run task smoke-cleanup when done.")
			return
		}
		if err := cleanup(ctx, cfg); err != nil {
			t.Error(err)
		}
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
			ensureNamespaces(ctx, t, cfg)
			create(ctx, t, cfg, testPod(testNamespace, "no-cr", nginx, nil))
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
			ensureNamespaces(ctx, t, cfg)
			before := rewritesTotal(ctx, t, alternative.Name)
			create(ctx, t, cfg, alternative)
			create(ctx, t, cfg, testPod(testNamespace, "fallback", unresolvable, routed))
			create(ctx, t, cfg, testPod(testNamespace, "origin-up", nginx, routed))
			create(ctx, t, cfg, testPod(unlabeledNamespace, "out-of-scope", unresolvable, routed))
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
		pod := getPod(ctx, t, cfg, testNamespace, "fallback")
		waitPodReady(t, cfg, pod)
		pod = getPod(ctx, t, cfg, testNamespace, "fallback")
		expectImage(t, pod, nginx)
		if rewrites := pod.Annotations[podrecord.AnnotationRewrites]; !strings.Contains(rewrites, "ImageAlternative/"+name) {
			t.Errorf("annotation %s = %q, expected ImageAlternative/%s", podrecord.AnnotationRewrites, rewrites, name)
		}
		return ctx
	}
}

func expectOneRewrite(name string) features.Func {
	return func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
		before, ok := ctx.Value(rewritesBeforeKey{}).(float64)
		if !ok {
			t.Fatal("the rewrite count before the test is missing")
		}
		if got := rewritesTotal(ctx, t, name) - before; got != 1 {
			t.Errorf("kuik_routing_rewrites_total grew by %v, expected 1", got)
		}
		return ctx
	}
}

func expectOutOfScope(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	waitFor(t, 90*time.Second, "out-of-scope failing its pull", func(ctx context.Context) (bool, error) {
		pod := getPod(ctx, t, cfg, unlabeledNamespace, "out-of-scope")
		for _, status := range pod.Status.ContainerStatuses {
			if w := status.State.Waiting; w != nil && slices.Contains([]string{"ErrImagePull", "ImagePullBackOff"}, w.Reason) {
				return true, nil
			}
		}
		return false, nil
	})
	pod := getPod(ctx, t, cfg, unlabeledNamespace, "out-of-scope")
	expectImage(t, pod, unresolvable)
	expectUntouched(t, pod)
	return ctx
}

func expectFallbackStatus(name string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		var live kuikv1alpha1.ImageAlternative
		waitFor(t, 60*time.Second, "FallbackActive True", func(ctx context.Context) (bool, error) {
			if err := cfg.Client().Resources().Get(ctx, name, "", &live); err != nil {
				return false, err
			}
			return meta.IsStatusConditionTrue(live.Status.Conditions, "FallbackActive"), nil
		})
		expectReadyReason(t, &live, "IsReady")
		if !slices.ContainsFunc(live.Status.ActiveFallbacks, func(f kuikv1alpha1.ActiveFallback) bool {
			return f.RewrittenTo == nginx
		}) {
			t.Errorf("activeFallbacks %+v does not list %s", live.Status.ActiveFallbacks, nginx)
		}
		waitFor(t, 60*time.Second, "ImageFallback event on pod fallback", func(ctx context.Context) (bool, error) {
			return podEvent(ctx, testNamespace, "fallback", "ImageFallback")
		})
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
			ensureNamespaces(ctx, t, cfg)
			create(ctx, t, cfg, mirror)
			create(ctx, t, cfg, testPod(testNamespace, "mirror", nginx, map[string]string{routingLabel: "mirror"}))
			return ctx
		}).
		Assess("leaves the pod on its origin, without a rewrite", expectMirrorUnused).
		Feature()
}

func expectMirrorUnused(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	pod := getPod(ctx, t, cfg, testNamespace, "mirror")
	waitPodReady(t, cfg, pod)
	pod = getPod(ctx, t, cfg, testNamespace, "mirror")
	expectImage(t, pod, nginx)
	if rewrites, ok := pod.Annotations[podrecord.AnnotationRewrites]; ok {
		t.Errorf("pod mirror rewritten: %s", rewrites)
	}
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
			ensureNamespaces(ctx, t, cfg)
			create(ctx, t, cfg, placeholder)
			create(ctx, t, cfg, withAuth)
			return ctx
		}).
		Assess("test 8: gets its pull Secret in the selected namespaces only, before any pod",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				secret := waitSecret(t, cfg, testNamespace, injected)
				if secret.Type != corev1.SecretTypeDockerConfigJson {
					t.Errorf("%s has type %s", injected, secret.Type)
				}
				err := cfg.Client().Resources().Get(ctx, injected, unlabeledNamespace, &corev1.Secret{})
				if !apierrors.IsNotFound(err) {
					t.Errorf("%s in %s: %v, expected not found", injected, unlabeledNamespace, err)
				}
				waitFor(t, 30*time.Second, "Ready IsReady on "+withAuth.Name, readyReason(cfg, withAuth.Name, "IsReady"))
				return ctx
			}).
		Assess("test 10: loses its pull Secret once deleted", expectPullSecretDeleted(withAuth, injected)).
		Feature()
}

func expectPullSecretDeleted(alternative *kuikv1alpha1.ImageAlternative, injected string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		if err := cfg.Client().Resources().Delete(ctx, alternative); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 60*time.Second, injected+" deleted", func(ctx context.Context) (bool, error) {
			err := cfg.Client().Resources().Get(ctx, injected, testNamespace, &corev1.Secret{})
			return apierrors.IsNotFound(err), client.IgnoreNotFound(err)
		})
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
			ensureNamespaces(ctx, t, cfg)
			create(ctx, t, cfg, missing)
			return ctx
		}).
		Assess("reports it in Ready and as a PullSecretInjectionFailed event", expectSecretNotFound(missing.Name)).
		Feature()
}

func expectSecretNotFound(name string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		waitFor(t, 60*time.Second, "Ready SecretNotFound on "+name, readyReason(cfg, name, "SecretNotFound"))
		waitFor(t, 60*time.Second, "PullSecretInjectionFailed event on "+name, func(ctx context.Context) (bool, error) {
			return resourceEvent(ctx, name, "PullSecretInjectionFailed")
		})
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
	ensureNamespaces(ctx, t, cfg)
	// Built here, after the flags are parsed.
	create(ctx, t, cfg, &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: "kuik-test-private"},
		Spec: scoped("private", kuikv1alpha1.RewritePolicyAlways,
			kuikv1alpha1.Alternative{Repository: "kuik-test-private.invalid/nginx/nginx-unprivileged"},
			withSecret(*privateRepo, *pullAuth)),
	})
	waitSecret(t, cfg, testNamespace, injected)
	pod := testPod(testNamespace, "private", "kuik-test-private.invalid/nginx/nginx-unprivileged:1.31.6-alpine",
		map[string]string{routingLabel: "private"})
	// A cached image would skip the pull this test is about.
	pod.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways
	create(ctx, t, cfg, pod)
	waitPodReady(t, cfg, pod)
	pod = getPod(ctx, t, cfg, testNamespace, "private")
	expectImage(t, pod, *privateRepo+":1.31.6-alpine")
	holds := func(r corev1.LocalObjectReference) bool { return r.Name == injected }
	if !slices.ContainsFunc(pod.Spec.ImagePullSecrets, holds) {
		t.Errorf("imagePullSecrets %+v does not hold %s", pod.Spec.ImagePullSecrets, injected)
	}
	waitFor(t, 30*time.Second, "Pulled event on pod private", func(ctx context.Context) (bool, error) {
		return podEvent(ctx, testNamespace, "private", "Pulled")
	})
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
		pod := getPod(ctx, t, cfg, namespace, name)
		waitPodReady(t, cfg, pod)
		pod = getPod(ctx, t, cfg, namespace, name)
		expectImage(t, pod, image)
		expectUntouched(t, pod)
		return ctx
	}
}

func expectImage(t *testing.T, pod *corev1.Pod, image string) {
	t.Helper()
	if got := pod.Spec.Containers[0].Image; got != image {
		t.Errorf("pod %s runs %s, expected %s%s", pod.Name, got, image, webhookLogs(context.Background(), pod.Name))
	}
}

func expectUntouched(t *testing.T, pod *corev1.Pod) {
	t.Helper()
	for _, key := range []string{podrecord.AnnotationRewrites, podrecord.AnnotationNoAlternatives} {
		if v, ok := pod.Annotations[key]; ok {
			t.Errorf("pod %s carries %s=%s", pod.Name, key, v)
		}
	}
}

func expectReadyReason(t *testing.T, alternative *kuikv1alpha1.ImageAlternative, reason string) {
	t.Helper()
	if ready := meta.FindStatusCondition(alternative.Status.Conditions, "Ready"); ready == nil || ready.Reason != reason {
		t.Errorf("%s: Ready condition %+v, expected reason %s", alternative.Name, ready, reason)
	}
}

// readyReason polls the Ready condition of an ImageAlternative until it has reason.
func readyReason(cfg *envconf.Config, name, reason string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		var live kuikv1alpha1.ImageAlternative
		if err := cfg.Client().Resources().Get(ctx, name, "", &live); err != nil {
			return false, err
		}
		ready := meta.FindStatusCondition(live.Status.Conditions, "Ready")
		return ready != nil && ready.Reason == reason, nil
	}
}

func waitSecret(t *testing.T, cfg *envconf.Config, namespace, name string) *corev1.Secret {
	t.Helper()
	var secret corev1.Secret
	waitFor(t, 60*time.Second, name+" in "+namespace, func(ctx context.Context) (bool, error) {
		err := cfg.Client().Resources().Get(ctx, name, namespace, &secret)
		return err == nil, client.IgnoreNotFound(err)
	})
	return &secret
}
