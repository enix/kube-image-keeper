package secretsyncer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

const (
	// scopeLabel is the namespace label a spec's resources select, its value unique per spec.
	scopeLabel = "kuik.enix.io/test-scope"
	// container names the one container of a rewritten pod.
	container = "app"
	// debounce is the debounce of the syncers the specs start.
	debounce = 200 * time.Millisecond
	// timeout bounds every wait on the syncer.
	timeout = 15 * time.Second
)

var sequence atomic.Int64

// unique returns a name no other spec uses.
func unique(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, sequence.Add(1))
}

// clock is the time the syncers read the grace period with.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// running is one syncer started by a spec.
type running struct {
	registry *prometheus.Registry
	stop     func()
}

type startOptions struct {
	syncPeriod time.Duration
	debounce   time.Duration
	clock      *clock
}

// start runs a syncer as syncerUser in a manager of its own, stopped at the end of the spec.
func start(opts startOptions) *running {
	cacheOptions := CacheOptions(installNamespace)
	if opts.syncPeriod != 0 {
		cacheOptions.SyncPeriod = &opts.syncPeriod
	}
	mgr, err := ctrl.NewManager(syncerCfg, ctrl.Options{
		Scheme:                 scheme.Scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: new(true)},
		Cache:                  cacheOptions,
	})
	Expect(err).NotTo(HaveOccurred())

	registry := prometheus.NewRegistry()
	syncerOptions := Options{
		Namespace:  installNamespace,
		Recorder:   mgr.GetEventRecorder("kuik-secret-syncer"),
		Registerer: registry,
		Debounce:   debounce,
	}
	if opts.debounce != 0 {
		syncerOptions.Debounce = opts.debounce
	}
	if opts.clock != nil {
		syncerOptions.Now = opts.clock.Now
	}
	s, err := New(mgr.GetClient(), syncerOptions)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.SetupWithManager(mgr)).To(Succeed())

	managerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		Expect(mgr.Start(managerCtx)).To(Succeed())
	}()
	r := &running{registry: registry, stop: func() { stop(); <-done }}
	DeferCleanup(r.stop)
	return r
}

// applies is the value of kuik_secret_applies_total{result}.
func (r *running) applies(result string) float64 {
	families, err := r.registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != "kuik_secret_applies_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "result" && label.GetValue() == result {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// allApplies is the sum of kuik_secret_applies_total over its results.
func (r *running) allApplies() float64 {
	return r.applies("Applied") + r.applies("Noop") + r.applies("Failed")
}

// createNamespace creates a namespace carrying scope as its scopeLabel, none when empty.
func createNamespace(scope string) string {
	name := unique("ns")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if scope != "" {
		ns.Labels = map[string]string{scopeLabel: scope}
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return name
}

// setScope sets or, when empty, removes the scopeLabel of a namespace.
func setScope(namespace, scope string) {
	var ns corev1.Namespace
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, &ns)).To(Succeed())
	if scope == "" {
		delete(ns.Labels, scopeLabel)
	} else {
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels[scopeLabel] = scope
	}
	Expect(k8sClient.Update(ctx, &ns)).To(Succeed())
}

// dockerConfigData is a docker config holding user for every key.
func dockerConfigData(user string, keys ...string) []byte {
	auths := map[string]map[string]string{}
	for _, key := range keys {
		auths[key] = map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(user + ":secret"))}
	}
	data, err := json.Marshal(map[string]any{"auths": auths})
	Expect(err).NotTo(HaveOccurred())
	return data
}

// createSource creates a docker-registry Secret in the install namespace, holding user for
// every key.
func createSource(name, user string, keys ...string) {
	Expect(k8sClient.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: installNamespace, Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: dockerConfigData(user, keys...)},
	})).To(Succeed())
}

// updateSource makes a source Secret hold user for every key.
func updateSource(name, user string, keys ...string) {
	var secret corev1.Secret
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: installNamespace, Name: name}, &secret)).To(Succeed())
	secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: dockerConfigData(user, keys...)}
	Expect(k8sClient.Update(ctx, &secret)).To(Succeed())
}

func injectedAuth(name string) *kuikv1alpha1.Auth {
	return &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}
}

// createAlternative creates an ImageAlternative selecting the namespaces of scope.
func createAlternative(policy kuikv1alpha1.RewritePolicy, scope string, entries ...kuikv1alpha1.Alternative) *kuikv1alpha1.ImageAlternative {
	cr := &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: unique("alternative")},
		Spec: kuikv1alpha1.ImageAlternativeSpec{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{scopeLabel: scope}},
			RewritePolicy:     policy,
			Alternatives:      entries,
		},
	}
	Expect(k8sClient.Create(ctx, cr)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cr))).To(Succeed()) })
	return cr
}

// updateAlternative applies change to the stored ImageAlternative.
func updateAlternative(cr *kuikv1alpha1.ImageAlternative, change func(*kuikv1alpha1.ImageAlternative)) {
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name}, cr)).To(Succeed())
	change(cr)
	Expect(k8sClient.Update(ctx, cr)).To(Succeed())
}

func group(path string, a *kuikv1alpha1.Auth) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{RepositoryGroup: path, Auth: a}
}

// createRewrittenPod creates a running pod whose container runs rewrittenTo, recorded as
// placed by cr.
func createRewrittenPod(namespace string, cr *kuikv1alpha1.ImageAlternative, rewrittenTo string) *corev1.Pod {
	value, err := json.Marshal(map[string]podrecord.Rewrite{container: {
		By: "ImageAlternative/" + cr.Name, Origin: "quay.io/acme/foo:1", RewrittenTo: rewrittenTo, Policy: "OnFailure",
	}})
	Expect(err).NotTo(HaveOccurred())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        unique("pod"),
			Annotations: map[string]string{podrecord.AnnotationRewrites: string(value)},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: container, Image: rewrittenTo}}},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	// envtest runs no kubelet: the phase a live pod has is set by hand.
	pod.Status.Phase = corev1.PodRunning
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
	return pod
}

// injected reads the Secret the syncer applies for cr in namespace, nil when absent.
func injected(namespace string, cr *kuikv1alpha1.ImageAlternative) *corev1.Secret {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: namespace, Name: auth.InjectedSecretName("ImageAlternative", cr.Name)}
	if err := k8sClient.Get(ctx, key, &secret); err != nil {
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), err.Error())
		return nil
	}
	return &secret
}

// users is the username each auths key of the injected Secret holds, nil when the Secret is
// absent.
func users(namespace string, cr *kuikv1alpha1.ImageAlternative) func() map[string]string {
	return func() map[string]string {
		secret := injected(namespace, cr)
		if secret == nil {
			return nil
		}
		var config struct {
			Auths map[string]struct {
				Username string `json:"username"`
			} `json:"auths"`
		}
		Expect(json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config)).To(Succeed())
		found := map[string]string{}
		for key, entry := range config.Auths {
			found[key] = entry.Username
		}
		return found
	}
}

// failedEvents are the PullSecretInjectionFailed events emitted on cr.
func failedEvents(cr *kuikv1alpha1.ImageAlternative) func() []eventsv1.Event {
	return func() []eventsv1.Event {
		var list eventsv1.EventList
		Expect(k8sClient.List(ctx, &list)).To(Succeed())
		var found []eventsv1.Event
		for _, e := range list.Items {
			if e.Reason == EventPullSecretInjectionFailed && e.Regarding.Kind == "ImageAlternative" && e.Regarding.Name == cr.Name {
				found = append(found, e)
			}
		}
		return found
	}
}
