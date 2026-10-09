//go:build smoke

package smoke

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// Every object the smoke test creates carries ownerLabel. One outside the test namespaces is
// also named kuik-test*: cleanup deletes what carries both, so a label put by hand on a real
// object is not enough to get it deleted.
const (
	ownerLabel = "kuik.enix.io/smoke-test"
	// scopeLabel is what every test CR selects namespaces on; only testNamespace carries it.
	scopeLabel = "kuik.enix.io/test"
	// labelTrue is the value of ownerLabel and scopeLabel.
	labelTrue = "true"

	testNamespace      = "kuik-test"
	unlabeledNamespace = "kuik-test-unlabeled"

	// namespaceDeletion bounds the wait for a test namespace to be gone: its pods terminate.
	namespaceDeletion = 180 * time.Second
)

var (
	testNamespaces = []string{testNamespace, unlabeledNamespace}
	testName       = regexp.MustCompile(`^kuik-test(-[a-z0-9.-]+)?$`)
)

// create creates obj after the checks that keep a shared cluster safe, and fails the test
// on any of them. It sets ownerLabel itself.
func create(ctx context.Context, g Gomega, cfg *envconf.Config, obj client.Object) {
	g.Expect(guard(obj)).To(Succeed())
	obj.SetLabels(labels.Merge(obj.GetLabels(), map[string]string{ownerLabel: labelTrue}))

	r := cfg.Client().Resources()
	live, ok := obj.DeepCopyObject().(client.Object)
	g.Expect(ok).To(BeTrue(), "copy %s", describe(obj))
	err := r.Get(ctx, obj.GetName(), obj.GetNamespace(), live)
	if err == nil {
		g.Expect(live.GetLabels()).To(HaveKeyWithValue(ownerLabel, labelTrue),
			"%s exists and was not created by the smoke test: refusing to touch it", describe(obj))
		g.Expect(err).To(HaveOccurred(), "%s is left from an earlier run: run task smoke-cleanup", describe(obj))
	}
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "look up %s: %v", describe(obj), err)
	g.Expect(r.Create(ctx, obj)).To(Succeed(), "create %s", describe(obj))
}

// guard refuses an object cleanup could not find, and a kuik CR that could select a pod
// outside the test: the CRs are cluster-scoped and the webhook sees every namespace.
func guard(obj client.Object) error {
	if !slices.Contains(testNamespaces, obj.GetNamespace()) && !testName.MatchString(obj.GetName()) {
		return fmt.Errorf("%s: outside the test namespaces, the name must match %s", describe(obj), testName)
	}
	var namespaceSelector, podSelector *metav1.LabelSelector
	switch o := obj.(type) {
	case *kuikv1alpha1.ImageAlternative:
		namespaceSelector, podSelector = o.Spec.NamespaceSelector, o.Spec.PodSelector
	case *kuikv1alpha1.ImageMirror:
		namespaceSelector, podSelector = o.Spec.NamespaceSelector, o.Spec.PodSelector
	case *kuikv1alpha1.ImageMonitor:
		return fmt.Errorf("%s: ImageMonitor is not handled yet: add its scoping check here", describe(obj))
	default:
		return nil
	}
	if namespaceSelector == nil || namespaceSelector.MatchLabels[scopeLabel] != labelTrue {
		return fmt.Errorf("%s: namespaceSelector must match %s=true", describe(obj), scopeLabel)
	}
	if podSelector == nil || (len(podSelector.MatchLabels) == 0 && len(podSelector.MatchExpressions) == 0) {
		return fmt.Errorf("%s: podSelector must not be empty", describe(obj))
	}
	return nil
}

// ensureNamespaces creates the test namespaces, once per run: TestInstall never creates them.
func ensureNamespaces(ctx context.Context, g Gomega, cfg *envconf.Config) {
	for _, name := range testNamespaces {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if name == testNamespace {
			ns.Labels = map[string]string{scopeLabel: labelTrue}
		}
		var live corev1.Namespace
		if err := cfg.Client().Resources().Get(ctx, name, "", &live); err == nil && live.Labels[ownerLabel] == labelTrue {
			continue
		}
		create(ctx, g, cfg, ns)
	}
}

// owned lists what the smoke test created: the kuik CRs and the Secrets of the kuik namespace
// carrying ownerLabel and a kuik-test name, then the labelled test namespaces. A failed list
// is an error, never "nothing left".
func owned(ctx context.Context, cfg *envconf.Config) ([]client.Object, error) {
	r := cfg.Client().Resources()
	selector := resources.WithLabelSelector(ownerLabel + "=true")
	var found []client.Object

	var alternatives kuikv1alpha1.ImageAlternativeList
	var mirrors kuikv1alpha1.ImageMirrorList
	var monitors kuikv1alpha1.ImageMonitorList
	var secrets corev1.SecretList
	for _, list := range []client.ObjectList{&alternatives, &mirrors, &monitors} {
		if err := r.List(ctx, list, selector); err != nil {
			return nil, err
		}
	}
	if err := r.WithNamespace(*kuikNamespace).List(ctx, &secrets, selector); err != nil {
		return nil, err
	}
	for i := range alternatives.Items {
		found = append(found, &alternatives.Items[i])
	}
	for i := range mirrors.Items {
		found = append(found, &mirrors.Items[i])
	}
	for i := range monitors.Items {
		found = append(found, &monitors.Items[i])
	}
	for i := range secrets.Items {
		found = append(found, &secrets.Items[i])
	}
	found = slices.DeleteFunc(found, func(o client.Object) bool { return !testName.MatchString(o.GetName()) })

	for _, name := range testNamespaces {
		var ns corev1.Namespace
		switch err := r.Get(ctx, name, "", &ns); {
		case apierrors.IsNotFound(err):
		case err != nil:
			return nil, err
		case ns.Labels[ownerLabel] == labelTrue:
			found = append(found, &ns)
		}
	}
	return found, nil
}

// refuseLeftovers fails when resources of an earlier run are left: a run must start from a
// cluster where nothing of the smoke test exists, or its checks could read stale objects.
func refuseLeftovers(ctx context.Context, cfg *envconf.Config) error {
	left, err := owned(ctx, cfg)
	if err != nil {
		return fmt.Errorf("cannot look for the resources of an earlier run: %w", err)
	}
	if len(left) > 0 {
		return fmt.Errorf("resources of an earlier run are left, run task smoke-cleanup: %s", describeAll(left))
	}
	return nil
}

// cleanup deletes what owned lists, the CRs before the namespaces, waits for the namespaces
// to be gone and checks nothing is left.
func cleanup(ctx context.Context, cfg *envconf.Config) error {
	left, err := owned(ctx, cfg)
	if err != nil {
		return err
	}
	r := cfg.Client().Resources()
	var errs []error
	for _, obj := range left {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("cannot delete %s: %w", describe(obj), err))
		}
	}
	for _, obj := range left {
		if _, ok := obj.(*corev1.Namespace); !ok {
			continue
		}
		gone := conditions.New(r).ResourceDeleted(obj)
		if err := wait.For(gone, wait.WithTimeout(namespaceDeletion), wait.WithInterval(3*time.Second)); err != nil {
			errs = append(errs, fmt.Errorf("%s is still there after %s: %w", describe(obj), namespaceDeletion, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if left, err = owned(ctx, cfg); err != nil {
		return fmt.Errorf("cannot list the test resources after cleanup: %w", err)
	}
	if len(left) > 0 {
		return fmt.Errorf("left after cleanup: %s", describeAll(left))
	}
	return nil
}

func describe(obj client.Object) string {
	kind := strings.TrimPrefix(fmt.Sprintf("%T", obj), "*")
	if obj.GetNamespace() == "" {
		return kind + "/" + obj.GetName()
	}
	return kind + "/" + obj.GetNamespace() + "/" + obj.GetName()
}

func describeAll(objs []client.Object) string {
	names := make([]string, len(objs))
	for i, obj := range objs {
		names[i] = describe(obj)
	}
	return strings.Join(names, ", ")
}
