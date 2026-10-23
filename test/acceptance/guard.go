package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// Every object the acceptance test creates carries ownerLabel. One outside the test namespaces is
// also named kuik-test*: cleanup deletes what carries both, so a label put by hand on a real
// object is not enough to get it deleted.
const (
	ownerLabel = "kuik.enix.io/acceptance-test"
	// scopeLabel is what every test CR selects namespaces on; only testNamespace carries it.
	scopeLabel = "kuik.enix.io/test"
	// labelTrue is the value of ownerLabel and scopeLabel.
	labelTrue = "true"
	// namespaceNameLabel is the label Kubernetes sets on every namespace to its name: a test CR
	// selects on it too, so another namespace that carries scopeLabel is never selected.
	namespaceNameLabel = "kubernetes.io/metadata.name"

	testNamespace      = "kuik-test"
	unlabeledNamespace = "kuik-test-unlabeled"

	// podSecurityLabel enforces a Pod Security profile on a namespace; the test namespaces carry
	// the restricted one, so the API server refuses a test pod that could reach its node.
	podSecurityLabel      = "pod-security.kubernetes.io/enforce"
	podSecurityRestricted = "restricted"

	// deletionWait bounds the wait for every test object cleanup deleted to be gone: the pods of
	// the namespaces terminate, kuik releases the finalizer of an ImageMirror.
	deletionWait = 180 * time.Second
)

var (
	testNamespaces = []string{testNamespace, unlabeledNamespace}
	testName       = regexp.MustCompile(`^kuik-test(-[a-z0-9.-]+)?$`)
)

// guard refuses an object cleanup could not find, and a kuik CR that could select a pod
// outside the test: the CRs are cluster-scoped and the webhook sees every namespace. What it
// lets through: the 2 test namespaces; a Pod, Secret or ConfigMap inside them; a Secret named
// kuik-test* in the kuik namespace (a source Secret the CRs refer to); a kuik CR named
// kuik-test* and scoped twice.
func (e *env) guard(obj client.Object) error {
	var namespaceSelector, podSelector *metav1.LabelSelector
	switch o := obj.(type) {
	case *corev1.Namespace:
		if !slices.Contains(testNamespaces, o.Name) {
			return fmt.Errorf("%s: the only namespaces are %s", describe(obj), strings.Join(testNamespaces, " and "))
		}
		return nil
	case *corev1.Secret:
		if o.Namespace == e.kuikNamespace && testName.MatchString(o.Name) {
			return nil
		}
		return inTestNamespace(obj)
	case *corev1.Pod, *corev1.ConfigMap:
		return inTestNamespace(obj)
	case *kuikv1alpha1.ImageAlternative:
		namespaceSelector, podSelector = o.Spec.NamespaceSelector, o.Spec.PodSelector
	case *kuikv1alpha1.ImageMirror:
		namespaceSelector, podSelector = o.Spec.NamespaceSelector, o.Spec.PodSelector
	case *kuikv1alpha1.ImageMonitor:
		namespaceSelector, podSelector = o.Spec.NamespaceSelector, o.Spec.PodSelector
	default:
		return fmt.Errorf("%s: kind not handled: add it to guard and to owned", describe(obj))
	}
	if !testName.MatchString(obj.GetName()) {
		return fmt.Errorf("%s: the name must match %s", describe(obj), testName)
	}
	if namespaceSelector == nil || namespaceSelector.MatchLabels[scopeLabel] != labelTrue ||
		namespaceSelector.MatchLabels[namespaceNameLabel] != testNamespace {
		return fmt.Errorf("%s: namespaceSelector must match %s=true and %s=%s",
			describe(obj), scopeLabel, namespaceNameLabel, testNamespace)
	}
	if podSelector == nil || (len(podSelector.MatchLabels) == 0 && len(podSelector.MatchExpressions) == 0) {
		return fmt.Errorf("%s: podSelector must not be empty", describe(obj))
	}
	return nil
}

// checkWrite refuses what a test object would bring onto the cluster beyond its scope, before it
// is created or updated: a credential that is not a test Secret or the user's --pull-auth, a cloud
// provider identity, a mirror destination outside the test paths, a pod that could reach a node or
// evict a real pod, a test namespace without the restricted Pod Security profile. delete does not
// call it: removing a test object brings nothing new.
func (e *env) checkWrite(obj client.Object) error {
	switch o := obj.(type) {
	case *corev1.Namespace:
		if o.Labels[podSecurityLabel] != podSecurityRestricted {
			return fmt.Errorf("%s: must carry %s=%s", describe(obj), podSecurityLabel, podSecurityRestricted)
		}
	case *corev1.Pod:
		return checkPod(o)
	case *kuikv1alpha1.ImageAlternative:
		for _, alternative := range o.Spec.Alternatives {
			if err := e.checkAuth(obj, alternative.Auth); err != nil {
				return err
			}
		}
	case *kuikv1alpha1.ImageMirror:
		if err := e.checkDestination(obj, o.Spec.Destination.Path); err != nil {
			return err
		}
		for _, role := range []*kuikv1alpha1.DestinationCredentials{o.Spec.Destination.Manage, o.Spec.Destination.Pull} {
			if role == nil {
				continue
			}
			if err := e.checkAuth(obj, &role.Auth); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkAuth lets a test CR read only a test Secret or the user's --pull-auth: any other Secret of
// the kuik namespace may be a real credential, with write access to a real registry.
func (e *env) checkAuth(obj client.Object, auth *kuikv1alpha1.Auth) error {
	switch {
	case auth == nil:
		return nil
	case auth.Provider != nil:
		return fmt.Errorf("%s: a test CR uses no cloud provider identity", describe(obj))
	case auth.SecretRef != nil && !testName.MatchString(auth.SecretRef.Name) &&
		(e.pullAuth == "" || auth.SecretRef.Name != e.pullAuth):
		return fmt.Errorf("%s: secretRef %s is neither a %s Secret nor --pull-auth",
			describe(obj), auth.SecretRef.Name, testName)
	}
	return nil
}

// checkDestination keeps a test mirror off every real registry path: kuik deletes the tags of
// this cluster it finds under a mirror's destination, so a test mirror sharing a real one's path
// would delete real tags. The host must be under .invalid (never resolves, RFC 2606), or the path
// under --mirror-prefix, a path the user dedicates to the test.
func (e *env) checkDestination(obj client.Object, path string) error {
	path = strings.TrimSuffix(path, "/")
	host, _, _ := strings.Cut(path, "/")
	underPrefix := e.mirrorPrefix != "" && underPath(registryPath(path), registryPath(e.mirrorPrefix))
	if strings.HasSuffix(host, ".invalid") || underPrefix {
		return nil
	}
	return fmt.Errorf("%s: destination.path %s must be on a .invalid host or under --mirror-prefix", describe(obj), path)
}

// underPath tells whether path is prefix or below it, segment by segment.
func underPath(path, prefix string) bool {
	path, prefix = strings.TrimSuffix(path, "/"), strings.TrimSuffix(prefix, "/")
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// checkPod refuses a pod that could weigh on the real workloads: one that preempts them, skips the
// scheduler or reaches its node by the most direct means. The restricted Pod Security profile the
// test namespaces enforce covers the rest (privilege escalation, seccomp, procMount).
func checkPod(pod *corev1.Pod) error {
	spec := pod.Spec
	switch {
	case spec.PriorityClassName != "" || spec.Priority != nil:
		return fmt.Errorf("%s: a test pod sets no priority", describe(pod))
	case spec.NodeName != "":
		return fmt.Errorf("%s: a test pod sets no nodeName", describe(pod))
	case spec.HostNetwork || spec.HostPID || spec.HostIPC:
		return fmt.Errorf("%s: a test pod shares no host namespace", describe(pod))
	}
	for _, volume := range spec.Volumes {
		if volume.HostPath != nil {
			return fmt.Errorf("%s: a test pod mounts no hostPath", describe(pod))
		}
	}
	containers := append(append(slices.Clone(spec.InitContainers), spec.Containers...), ephemeralAsContainers(spec)...)
	for _, c := range containers {
		if sc := c.SecurityContext; sc != nil {
			if sc.Privileged != nil && *sc.Privileged {
				return fmt.Errorf("%s: container %s is privileged", describe(pod), c.Name)
			}
			if sc.Capabilities != nil && len(sc.Capabilities.Add) > 0 {
				return fmt.Errorf("%s: container %s adds capabilities", describe(pod), c.Name)
			}
		}
		for _, port := range c.Ports {
			if port.HostPort != 0 {
				return fmt.Errorf("%s: container %s binds a host port", describe(pod), c.Name)
			}
		}
	}
	return nil
}

func ephemeralAsContainers(spec corev1.PodSpec) []corev1.Container {
	out := make([]corev1.Container, 0, len(spec.EphemeralContainers))
	for _, c := range spec.EphemeralContainers {
		out = append(out, corev1.Container(c.EphemeralContainerCommon))
	}
	return out
}

// checkMirrorOverlap refuses a test mirror whose destination shares a path with a mirror that is
// not a test object, whatever the rules above let through.
func (e *env) checkMirrorOverlap(ctx context.Context, mirror *kuikv1alpha1.ImageMirror) error {
	var mirrors kuikv1alpha1.ImageMirrorList
	if err := e.client.List(ctx, &mirrors); err != nil {
		return fmt.Errorf("list the ImageMirrors: %w", err)
	}
	for _, other := range mirrors.Items {
		if other.Labels[ownerLabel] == labelTrue && testName.MatchString(other.Name) {
			continue
		}
		mine, theirs := registryPath(mirror.Spec.Destination.Path), registryPath(other.Spec.Destination.Path)
		if underPath(mine, theirs) || underPath(theirs, mine) {
			return fmt.Errorf("%s: destination.path %s overlaps ImageMirror/%s (%s)", describe(mirror), mine, other.Name, theirs)
		}
	}
	return nil
}

// registryPath writes a registry path the way the registry sees it, so two spellings of one
// path compare equal: the host lowercased, without the default HTTPS port, Docker Hub's aliases
// as docker.io.
func registryPath(path string) string {
	host, rest, _ := strings.Cut(strings.TrimSuffix(path, "/"), "/")
	host = strings.TrimSuffix(strings.ToLower(host), ":443")
	switch host {
	case "index.docker.io", "registry-1.docker.io":
		host = "docker.io"
	}
	if rest == "" {
		return host
	}
	return host + "/" + rest
}

func inTestNamespace(obj client.Object) error {
	if !slices.Contains(testNamespaces, obj.GetNamespace()) {
		return fmt.Errorf("%s: must live in %s", describe(obj), strings.Join(testNamespaces, " or "))
	}
	return nil
}

// apply creates obj, or updates it when the acceptance test created it, after guard. It sets
// ownerLabel itself and refuses an existing object without it. With createOnly, an existing
// object is refused too: setup starts from nothing.
func (e *env) apply(ctx context.Context, obj client.Object, createOnly bool) error {
	if err := e.guard(obj); err != nil {
		return err
	}
	if err := e.checkWrite(obj); err != nil {
		return err
	}
	if mirror, ok := obj.(*kuikv1alpha1.ImageMirror); ok {
		if err := e.checkMirrorOverlap(ctx, mirror); err != nil {
			return err
		}
	}
	if err := e.checkLiveNamespace(ctx, obj.GetNamespace()); err != nil {
		return err
	}
	obj.SetLabels(labels.Merge(obj.GetLabels(), map[string]string{ownerLabel: labelTrue}))
	live, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("copy %s", describe(obj))
	}
	err := e.client.Get(ctx, client.ObjectKeyFromObject(obj), live)
	switch {
	case apierrors.IsNotFound(err):
		if err := e.client.Create(ctx, obj); err != nil {
			return fmt.Errorf("create %s: %w", describe(obj), err)
		}
		fmt.Printf("created %s\n", describe(obj))
		return nil
	case err != nil:
		return fmt.Errorf("look up %s: %w", describe(obj), err)
	case live.GetLabels()[ownerLabel] != labelTrue:
		return fmt.Errorf("%s exists and was not created by the acceptance test: refusing to touch it", describe(obj))
	case createOnly:
		return fmt.Errorf("%s is left from an earlier run: run task acceptance-cleanup", describe(obj))
	case live.GetDeletionTimestamp() != nil:
		return fmt.Errorf("%s is being deleted: wait for it to go, then apply", describe(obj))
	}
	// The file knows nothing of what others set: kuik's finalizer on an ImageMirror holds its
	// tags until no pod runs them, and dropping it would skip their deletion.
	obj.SetFinalizers(live.GetFinalizers())
	obj.SetAnnotations(labels.Merge(live.GetAnnotations(), obj.GetAnnotations()))
	obj.SetOwnerReferences(live.GetOwnerReferences())
	obj.SetResourceVersion(live.GetResourceVersion())
	if err := e.client.Update(ctx, obj); err != nil {
		return fmt.Errorf("update %s (a pod spec is mostly immutable: delete it, then apply): %w", describe(obj), err)
	}
	fmt.Printf("updated %s\n", describe(obj))
	return nil
}

// remove deletes obj when it belongs to the acceptance test and passes guard: it carries
// ownerLabel, or lives in a test namespace the acceptance test created (a pull Secret kuik
// injected there, say).
func (e *env) remove(ctx context.Context, obj client.Object) error {
	if err := e.client.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return fmt.Errorf("look up %s: %w", describe(obj), err)
	}
	if obj.GetLabels()[ownerLabel] != labelTrue {
		owned, err := e.inOwnedNamespace(ctx, obj.GetNamespace())
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("%s was not created by the acceptance test: refusing to delete it", describe(obj))
		}
	}
	if err := e.guard(obj); err != nil {
		return err
	}
	if err := e.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %s: %w", describe(obj), err)
	}
	fmt.Printf("deleted %s\n", describe(obj))
	return nil
}

// checkLiveNamespace refuses to write into a test namespace the acceptance test did not create, or
// that does not enforce the restricted Pod Security profile: a kuik-test namespace left by hand
// would take test pods without either. Objects outside the test namespaces pass: guard already
// bounds them. A fixture creates its namespaces before what goes in them, in file order.
func (e *env) checkLiveNamespace(ctx context.Context, namespace string) error {
	if !slices.Contains(testNamespaces, namespace) {
		return nil
	}
	var ns corev1.Namespace
	if err := e.client.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		return fmt.Errorf("namespace %s: %w (apply fixtures/00-namespaces.yaml first)", namespace, err)
	}
	if ns.Labels[ownerLabel] != labelTrue || ns.Labels[podSecurityLabel] != podSecurityRestricted {
		return fmt.Errorf("namespace %s was not created by the acceptance test or does not enforce %s=%s",
			namespace, podSecurityLabel, podSecurityRestricted)
	}
	return nil
}

// checkKuikNamespace refuses a --kuik-namespace that holds no pod of the release: the test Secrets
// written there, and looked for there by cleanup, must sit next to kuik, not in a namespace a typo
// named.
func (e *env) checkKuikNamespace(ctx context.Context) error {
	pods, err := e.kuikPods(ctx, "")
	if err != nil {
		return fmt.Errorf("look for the kuik pods in %s: %w", e.kuikNamespace, err)
	}
	if len(pods) == 0 {
		return fmt.Errorf("no pod of release %s in %s: check --kuik-namespace and --release", e.release, e.kuikNamespace)
	}
	return nil
}

// inOwnedNamespace tells whether namespace is a test namespace carrying ownerLabel.
func (e *env) inOwnedNamespace(ctx context.Context, namespace string) (bool, error) {
	if !slices.Contains(testNamespaces, namespace) {
		return false, nil
	}
	var ns corev1.Namespace
	if err := e.client.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		return false, fmt.Errorf("look up namespace %s: %w", namespace, err)
	}
	return ns.Labels[ownerLabel] == labelTrue, nil
}

// owned lists what the acceptance test created outside the test namespaces (the kuik CRs and
// the Secrets of the kuik namespace carrying ownerLabel and a kuik-test name), then the
// labelled test namespaces, which hold the rest. A failed list is an error, never "nothing left".
func (e *env) owned(ctx context.Context) ([]client.Object, error) {
	selector := client.MatchingLabels{ownerLabel: labelTrue}
	var found []client.Object

	var alternatives kuikv1alpha1.ImageAlternativeList
	var mirrors kuikv1alpha1.ImageMirrorList
	var monitors kuikv1alpha1.ImageMonitorList
	var secrets corev1.SecretList
	for _, list := range []client.ObjectList{&alternatives, &mirrors, &monitors} {
		if err := e.client.List(ctx, list, selector); err != nil {
			return nil, err
		}
	}
	if err := e.client.List(ctx, &secrets, selector, client.InNamespace(e.kuikNamespace)); err != nil {
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
		switch err := e.client.Get(ctx, client.ObjectKey{Name: name}, &ns); {
		case apierrors.IsNotFound(err):
		case err != nil:
			return nil, err
		case ns.Labels[ownerLabel] == labelTrue:
			found = append(found, &ns)
		}
	}
	return found, nil
}

// refuseLeftovers fails when objects of an earlier run are left: a run must start from a
// cluster where nothing of the acceptance test exists, or what it shows could be stale.
func (e *env) refuseLeftovers(ctx context.Context) error {
	left, err := e.owned(ctx)
	if err != nil {
		return fmt.Errorf("cannot look for the objects of an earlier run: %w", err)
	}
	if len(left) > 0 {
		return fmt.Errorf("objects of an earlier run are left, run task acceptance-cleanup: %s", describeAll(left))
	}
	return nil
}

// runCleanup restores the global config when a test config is in place, then deletes what owned
// lists and waits for it to be gone, the CRs first, then the Secrets and the namespaces, and
// checks nothing is left.
func runCleanup(ctx context.Context, e *env, _ []string) error {
	if err := e.checkKuikNamespace(ctx); err != nil {
		return err
	}
	// A config written but not reloaded everywhere in time still lets the deletions go on: the
	// write is done, the error is returned at the end.
	restored := e.restoreConfig(ctx, 3*time.Minute)
	if errors.Is(restored, errNoConfigMap) {
		// Another release's test config would be left in place: stop before any deletion.
		if err := e.refuseOtherTestConfig(ctx); err != nil {
			return err
		}
		fmt.Printf("%v, so no test config to restore\n", restored)
		restored = nil
	}
	if restored != nil && !errors.Is(restored, errReloadTimeout) {
		return fmt.Errorf("restore the global config: %w", restored)
	}
	if err := e.deleteOwned(ctx); err != nil {
		return err
	}
	return restored
}

// refuseOtherTestConfig fails when a ConfigMap of the kuik namespace other than the one of
// --release holds a test config: the run used another --release, and cleanup must restore it.
func (e *env) refuseOtherTestConfig(ctx context.Context) error {
	var cms corev1.ConfigMapList
	if err := e.client.List(ctx, &cms, client.InNamespace(e.kuikNamespace)); err != nil {
		return fmt.Errorf("look for a test config in %s: %w", e.kuikNamespace, err)
	}
	for _, cm := range cms.Items {
		for _, key := range configAnnotations {
			if _, ok := cm.Annotations[key]; ok {
				return fmt.Errorf("%s holds a test config (%s) but is not %s: run cleanup with the --release of that run",
					describe(&cm), key, e.configMapKey().Name)
			}
		}
	}
	return nil
}

func (e *env) deleteOwned(ctx context.Context) error {
	left, err := e.owned(ctx)
	if err != nil {
		return err
	}
	// The CRs go alone, and are gone before any Secret: a deleted ImageMirror keeps its finalizer
	// until it has deleted its tags, with a manage credential a test Secret may hold.
	crs := slices.DeleteFunc(slices.Clone(left), func(o client.Object) bool { return !isCR(o) })
	rest := slices.DeleteFunc(slices.Clone(left), isCR)
	// A CR still there keeps the Secrets: its finalizer may need them.
	if err := errors.Join(e.deleteAndWait(ctx, crs)...); err != nil {
		return err
	}
	if err := errors.Join(e.deleteAndWait(ctx, rest)...); err != nil {
		return err
	}
	if left, err = e.owned(ctx); err != nil {
		return fmt.Errorf("cannot list the test objects after cleanup: %w", err)
	}
	if len(left) > 0 {
		return fmt.Errorf("left after cleanup: %s", describeAll(left))
	}
	fmt.Println("no test object left")
	return nil
}

func isCR(obj client.Object) bool {
	switch obj.(type) {
	case *kuikv1alpha1.ImageAlternative, *kuikv1alpha1.ImageMirror, *kuikv1alpha1.ImageMonitor:
		return true
	}
	return false
}

// deleteAndWait deletes every object and waits for each one to be gone: kuik holds a deleted
// ImageMirror with its finalizer until it has deleted its tags.
func (e *env) deleteAndWait(ctx context.Context, objs []client.Object) []error {
	var errs []error
	for _, obj := range objs {
		if err := e.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("cannot delete %s: %w", describe(obj), err))
			continue
		}
		fmt.Printf("deleted %s\n", describe(obj))
	}
	deadline := time.Now().Add(deletionWait)
	for _, obj := range objs {
		live, ok := obj.DeepCopyObject().(client.Object)
		if !ok {
			errs = append(errs, fmt.Errorf("copy %s", describe(obj)))
			continue
		}
		for {
			err := e.client.Get(ctx, client.ObjectKeyFromObject(obj), live)
			if apierrors.IsNotFound(err) {
				break
			}
			if time.Now().After(deadline) {
				errs = append(errs, fmt.Errorf("%s is still there after %s", describe(obj), deletionWait))
				break
			}
			time.Sleep(3 * time.Second)
		}
	}
	return errs
}

// The config command writes 2 objects it did not create, and only these fields of them: the key
// config.yaml and the annotations below of the ConfigMap <fullname>-config in the kuik namespace,
// and spec.suspend of the Flux HelmRelease that ConfigMap's labels name. writeConfig and
// suspendRelease are the only writes of either, and build their patch from nothing else.
const (
	configKey = "config.yaml"
	// originalAnnotation holds the config.yaml of before the first change of a run, put back by
	// --restore; it lives on the ConfigMap so an interrupted run can still restore it.
	originalAnnotation = "kuik.enix.io/acceptance-original-config"
	// originalAbsentAnnotation records that the ConfigMap held no config.yaml before the first
	// change: --restore removes the key rather than writing it empty.
	originalAbsentAnnotation = "kuik.enix.io/acceptance-original-config-absent"
	// suspendedAnnotation records that the program suspended the HelmRelease, so --restore resumes
	// only what it suspended. It is written before the suspension and removed after the resume.
	suspendedAnnotation = "kuik.enix.io/acceptance-suspended-release"
	fluxNameLabel       = "helm.toolkit.fluxcd.io/name"
	fluxNamespaceLabel  = "helm.toolkit.fluxcd.io/namespace"
	// fieldOwner names the program in the managedFields of what it patches: the time of its entry
	// is when the API server took the write.
	fieldOwner = "kuik-acceptance"
)

var helmReleaseGVK = schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"}

// configMapKey is the one ConfigMap config writes: the global config file of the release.
func (e *env) configMapKey() client.ObjectKey {
	return client.ObjectKey{Namespace: e.kuikNamespace, Name: e.fullname() + "-config"}
}

// fluxRelease is the HelmRelease the Flux labels of cm name, if any: the one object besides cm
// config may write.
func fluxRelease(cm *corev1.ConfigMap) (client.ObjectKey, bool) {
	name, namespace := cm.Labels[fluxNameLabel], cm.Labels[fluxNamespaceLabel]
	if name == "" || namespace == "" {
		return client.ObjectKey{}, false
	}
	return client.ObjectKey{Namespace: namespace, Name: name}, true
}

// configAnnotations are the only annotations config writes.
var configAnnotations = []string{originalAnnotation, originalAbsentAnnotation, suspendedAnnotation}

// writeConfig writes data (config.yaml only: a value sets it, nil removes it; a nil map leaves
// data alone) and the annotations given (nil removes one) on cm, in one merge patch holding
// nothing else and the resourceVersion cm was read at: a change made in between fails it rather
// than being overwritten. cm is updated to what the API server returned. It returns when the API
// server took the write.
func (e *env) writeConfig(ctx context.Context, cm *corev1.ConfigMap, data, annotations map[string]*string) (
	time.Time, error) {
	if client.ObjectKeyFromObject(cm) != e.configMapKey() {
		return time.Time{}, fmt.Errorf("%s: config writes only ConfigMap %s", describe(cm), e.configMapKey())
	}
	for key := range data {
		if key != configKey {
			return time.Time{}, fmt.Errorf("%s: config writes no data key %s", describe(cm), key)
		}
	}
	for key := range annotations {
		if !slices.Contains(configAnnotations, key) {
			return time.Time{}, fmt.Errorf("%s: config writes no annotation %s", describe(cm), key)
		}
	}
	metadata := map[string]any{"resourceVersion": cm.ResourceVersion}
	// Each left out when empty: a null annotations or data would remove all of them.
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}
	body := map[string]any{"metadata": metadata}
	if len(data) > 0 {
		body["data"] = data
	}
	patch, err := json.Marshal(body)
	if err != nil {
		return time.Time{}, err
	}
	if err := e.mergePatch(ctx, cm, patch); err != nil {
		return time.Time{}, fmt.Errorf("write %s: %w", describe(cm), err)
	}
	written := time.Now().UTC()
	for _, entry := range cm.ManagedFields {
		if entry.Manager == fieldOwner && entry.Time != nil {
			written = entry.Time.UTC()
		}
	}
	what := "annotations"
	if len(data) > 0 {
		what = configKey
	}
	fmt.Printf("wrote the %s of %s at %s\n", what, describe(cm), written.Format(time.RFC3339))
	return written, nil
}

// suspendRelease sets spec.suspend of the HelmRelease cm's Flux labels name, and nothing else.
func (e *env) suspendRelease(ctx context.Context, cm *corev1.ConfigMap, suspend bool) error {
	key, ok := fluxRelease(cm)
	if !ok {
		return fmt.Errorf("%s carries no Flux labels: no HelmRelease to suspend", describe(cm))
	}
	release := &unstructured.Unstructured{}
	release.SetGroupVersionKind(helmReleaseGVK)
	release.SetNamespace(key.Namespace)
	release.SetName(key.Name)
	patch, err := json.Marshal(map[string]any{"spec": map[string]bool{"suspend": suspend}})
	if err != nil {
		return err
	}
	if err := e.mergePatch(ctx, release, patch); err != nil {
		return fmt.Errorf("set spec.suspend of HelmRelease %s to %t: %w", key, suspend, err)
	}
	fmt.Printf("set spec.suspend of HelmRelease %s to %t\n", key, suspend)
	return nil
}

// mergePatch sends patch as a JSON merge patch, the program named as its field owner.
func (e *env) mergePatch(ctx context.Context, obj client.Object, patch []byte) error {
	return e.client.Patch(ctx, obj, client.RawPatch(types.MergePatchType, patch), client.FieldOwner(fieldOwner))
}

// releaseSuspended reads spec.suspend of the HelmRelease key.
func (e *env) releaseSuspended(ctx context.Context, key client.ObjectKey) (bool, error) {
	release := &unstructured.Unstructured{}
	release.SetGroupVersionKind(helmReleaseGVK)
	if err := e.client.Get(ctx, key, release); err != nil {
		return false, fmt.Errorf("read HelmRelease %s: %w", key, err)
	}
	suspended, _, err := unstructured.NestedBool(release.Object, "spec", "suspend")
	if err != nil {
		return false, fmt.Errorf("HelmRelease %s: spec.suspend: %w", key, err)
	}
	return suspended, nil
}

// clusterIDConfigMap is the ConfigMap of the kuik namespace kuik keeps its cluster ID in.
const clusterIDConfigMap = "kuik-cluster-id"

// canRead refuses, before any read, what get may not show: anything but the test objects as
// guard lets them through, the kuik CRs, the pods of the release in the kuik namespace (get selects
// them by the release labels, so no other pod there is read), its 2 ConfigMaps
// <fullname>-config and kuik-cluster-id, and the admission policy and binding
// <fullname>-secret-syncer. An empty name is a list of kind in namespace: allowed only where every
// object it reads is.
func (e *env) canRead(kind, namespace, name string) error {
	inTest := slices.Contains(testNamespaces, namespace)
	switch kind {
	case kindImageAlternative, kindImageMirror, kindImageMonitor:
		return nil
	case kindNamespace:
		if slices.Contains(testNamespaces, name) {
			return nil
		}
		return fmt.Errorf("namespace %q: get reads only %s", name, strings.Join(testNamespaces, " and "))
	case kindPod:
		if inTest || namespace == e.kuikNamespace {
			return nil
		}
		return fmt.Errorf("pods of %s: get reads only those of %s, and those of release %s in the kuik namespace %s",
			namespace, strings.Join(testNamespaces, ", "), e.release, e.kuikNamespace)
	case kindSecret:
		if inTest || (namespace == e.kuikNamespace && name != "" && testName.MatchString(name)) {
			return nil
		}
		return fmt.Errorf("secret %s/%s: get reads only the Secrets of %s and those named %s in %s, one by one",
			namespace, name, strings.Join(testNamespaces, ", "), testName, e.kuikNamespace)
	case kindConfigMap:
		if inTest || (namespace == e.kuikNamespace && (name == e.configMapKey().Name || name == clusterIDConfigMap)) {
			return nil
		}
		return fmt.Errorf("configmap %s/%s: get reads only the ConfigMaps of %s, and %s and %s in %s",
			namespace, name, strings.Join(testNamespaces, ", "), e.configMapKey().Name, clusterIDConfigMap, e.kuikNamespace)
	case kindPolicy, kindPolicyBinding:
		if name == e.fullname()+"-secret-syncer" {
			return nil
		}
		return fmt.Errorf("%s %q: get reads only %s-secret-syncer, the one the chart installs", kind, name, e.fullname())
	}
	return fmt.Errorf("kind %q: get does not read it", kind)
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
