package clusterid

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// namespace is kuik's install namespace in every spec.
	namespace = "kuik-system"
	// storedID is a valid clusterID a ConfigMap already holds.
	storedID = "cluster-a"
)

// calls counts the requests a fake client served, by verb.
type calls struct {
	get, create, write atomic.Int32
}

// fakeClient serves the given objects and counts the requests it serves.
func fakeClient(objects ...client.Object) (client.Client, *calls) {
	return forbiddingClient("", objects...)
}

// forbiddingClient is a fakeClient answering Forbidden to every request of the verb forbidden,
// get or create.
func forbiddingClient(forbidden string, objects ...client.Object) (client.Client, *calls) {
	counted := &calls{}
	refused := func(verb, name string) error {
		if verb != forbidden {
			return nil
		}
		return apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, name, errors.New("RBAC refused"))
	}
	c := fake.NewClientBuilder().WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			counted.get.Add(1)
			if err := refused("get", key.Name); err != nil {
				return err
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			counted.create.Add(1)
			if err := refused("create", obj.GetName()); err != nil {
				return err
			}
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			counted.write.Add(1)
			return c.Update(ctx, obj, opts...)
		},
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			counted.write.Add(1)
			return c.Patch(ctx, obj, patch, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			counted.write.Add(1)
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()
	return c, counted
}

// configMap is the ConfigMap kuik keeps the cluster ID in, with the given data.
func configMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ConfigMapName, Namespace: namespace},
		Data:       data,
	}
}

// stored reads the ConfigMap back from c.
func stored(c client.Client) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: ConfigMapName}, cm)
	return cm, err
}

// startAwait runs an Awaiter on c until the spec ends, polling every few milliseconds.
func startAwait(c client.Reader) *Awaiter {
	a, _ := startLoggedAwait(c)
	return a
}

// startLoggedAwait is startAwait, recording the errors the Awaiter logs.
func startLoggedAwait(c client.Reader) (*Awaiter, *errorLog) {
	a := Await(c, namespace)
	a.minDelay, a.maxDelay = 5*time.Millisecond, 10*time.Millisecond
	errs := &errorLog{}
	ctx, cancel := context.WithCancel(logf.IntoContext(context.Background(), logr.New(errs)))
	DeferCleanup(cancel)
	go func() {
		defer GinkgoRecover()
		_ = a.Start(ctx)
	}()
	return a, errs
}

// errorLog is a logr.LogSink recording the errors logged with Error, dropping the rest.
type errorLog struct {
	mu   sync.Mutex
	errs []error
}

func (l *errorLog) Init(logr.RuntimeInfo)          {}
func (l *errorLog) Enabled(int) bool               { return true }
func (l *errorLog) Info(int, string, ...any)       {}
func (l *errorLog) WithValues(...any) logr.LogSink { return l }
func (l *errorLog) WithName(string) logr.LogSink   { return l }
func (l *errorLog) Error(err error, _ string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

// logged returns the errors logged so far.
func (l *errorLog) logged() []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.errs)
}

var _ = Describe("Generate", func() {
	It("returns an adjective and a surname of the Docker word lists joined by -, then a 4-character random suffix", func() {
		for range 20 {
			parts := strings.Split(Generate(), "-")
			Expect(parts).To(HaveLen(3))
			Expect(left).To(ContainElement(parts[0]))
			Expect(right).To(ContainElement(parts[1]))
			Expect(parts[2]).To(MatchRegexp(`^[0-9a-f]{4}$`))
		}
	})

	It("returns a value Validate accepts", func() {
		for range 20 {
			id := Generate()
			Expect(id).NotTo(BeEmpty())
			Expect(Validate(id)).To(Succeed())
		}
	})

	It("draws again when it picks boring-wozniak, as Docker does", func() {
		boring, wozniak := slices.Index(left[:], "boring"), slices.Index(right[:], "wozniak")
		focused, turing := slices.Index(left[:], "focused"), slices.Index(right[:], "turing")
		Expect([]int{boring, wozniak, focused, turing}).NotTo(ContainElement(-1))
		// Each word is drawn from 2 big-endian bytes, the suffix from 2 more.
		random := bytes.NewReader([]byte{
			0, byte(boring), 0, byte(wozniak),
			0, byte(focused), 0, byte(turing),
			0x7f, 0x3a,
		})
		Expect(generate(random)).To(Equal("focused-turing-7f3a"))
	})

	It("draws a word again when the 2 bytes fall past the last whole multiple of the list length", func() {
		focused, turing := slices.Index(left[:], "focused"), slices.Index(right[:], "turing")
		Expect([]int{focused, turing}).NotTo(ContainElement(-1))
		random := bytes.NewReader([]byte{
			0xff, 0xff,
			0, byte(focused), 0, byte(turing),
			0x7f, 0x3a,
		})
		Expect(generate(random)).To(Equal("focused-turing-7f3a"))
	})
})

var _ = DescribeTable("Validate",
	func(id string, valid bool) {
		if valid {
			Expect(Validate(id)).To(Succeed())
		} else {
			Expect(Validate(id)).NotTo(Succeed())
		}
	},
	Entry("accepts a generated-style value", "focused-turing-7f3a", true),
	Entry("accepts uppercase letters and .", "Cluster.A-1", true),
	Entry("accepts a value of exactly 56 characters", strings.Repeat("a", 56), true),
	Entry("rejects an empty value", "", false),
	Entry("rejects a value carrying _, the tag suffix separator", "cluster_a", false),
	Entry("rejects a value starting with -", "-cluster-a", false),
	Entry("rejects a value starting with .", ".cluster", false),
	Entry("rejects a value longer than 56 characters", strings.Repeat("a", 57), false),
)

var _ = Describe("Ensure", func() {
	Context("when the ConfigMap is missing", func() {
		It("creates kuik-cluster-id in the namespace with a generated clusterID, without an owner reference, and returns it", func() {
			c, _ := fakeClient()
			id, err := Ensure(context.Background(), c, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(Validate(id)).To(Succeed())

			cm, err := stored(c)
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data).To(Equal(map[string]string{Key: id}))
			Expect(cm.OwnerReferences).To(BeEmpty())
		})
	})

	Context("when the ConfigMap exists", func() {
		It("returns its stored clusterID and leaves the ConfigMap unchanged", func() {
			c, counted := fakeClient(configMap(map[string]string{Key: storedID}))
			before, err := stored(c)
			Expect(err).NotTo(HaveOccurred())

			Expect(Ensure(context.Background(), c, namespace)).To(Equal(storedID))

			after, err := stored(c)
			Expect(err).NotTo(HaveOccurred())
			Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
			Expect(after.Data).To(Equal(map[string]string{Key: storedID}))
			Expect(counted.write.Load()).To(BeZero())
		})
	})

	It("returns a Create error other than AlreadyExists without reading the ConfigMap", func() {
		c, counted := forbiddingClient("create")
		_, err := Ensure(context.Background(), c, namespace)
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
		Expect(counted.get.Load()).To(BeZero())
	})

	DescribeTable("fails without rewriting the ConfigMap when its stored clusterID",
		func(data map[string]string) {
			c, counted := fakeClient(configMap(data))
			before, err := stored(c)
			Expect(err).NotTo(HaveOccurred())

			_, err = Ensure(context.Background(), c, namespace)
			Expect(err).To(MatchError(ContainSubstring(namespace + "/" + ConfigMapName)))
			if value, found := data[Key]; found {
				Expect(err).To(MatchError(ContainSubstring(value)))
			}

			after, err := stored(c)
			Expect(err).NotTo(HaveOccurred())
			Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
			Expect(after.Data).To(Equal(before.Data))
			Expect(counted.write.Load()).To(BeZero())
		},
		Entry("is missing", map[string]string{}),
		Entry("is outside ^[a-zA-Z0-9][a-zA-Z0-9.-]*$", map[string]string{"clusterID": "cluster_a"}),
		Entry("is longer than 56 characters", map[string]string{"clusterID": strings.Repeat("a", 57)}),
	)
})

var _ = Describe("Await", func() {
	It("reports not ready until the ConfigMap exists, then ready with its clusterID", func() {
		c, counted := fakeClient()
		a := startAwait(c)

		Eventually(counted.get.Load).Should(BeNumerically(">=", 2))
		Expect(a.Check(nil)).NotTo(Succeed())
		_, read := a.ID()
		Expect(read).To(BeFalse())

		Expect(c.Create(context.Background(), configMap(map[string]string{Key: storedID}))).To(Succeed())
		Eventually(func() error { return a.Check(nil) }).Should(Succeed())
		id, read := a.ID()
		Expect(read).To(BeTrue())
		Expect(id).To(Equal(storedID))
	})

	It("never creates the ConfigMap", func() {
		c, counted := fakeClient()
		startAwait(c)

		Eventually(counted.get.Load).Should(BeNumerically(">=", 3))
		Expect(counted.create.Load()).To(BeZero())
		Expect(counted.write.Load()).To(BeZero())
		_, err := stored(c)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("stays not ready when the stored clusterID is invalid", func() {
		c, counted := fakeClient(configMap(map[string]string{Key: "cluster_a"}))
		a := startAwait(c)

		Eventually(counted.get.Load).Should(BeNumerically(">=", 3))
		Expect(a.Check(nil)).NotTo(Succeed())
		_, read := a.ID()
		Expect(read).To(BeFalse())
	})

	It("stays not ready when the read is forbidden", func() {
		c, counted := forbiddingClient("get", configMap(map[string]string{Key: storedID}))
		a, errs := startLoggedAwait(c)

		Eventually(counted.get.Load).Should(BeNumerically(">=", 3))
		Expect(a.Check(nil)).NotTo(Succeed())
		_, read := a.ID()
		Expect(read).To(BeFalse())

		By("logging the refusal once as an error, for an operator to fix the RBAC")
		Expect(errs.logged()).To(HaveExactElements(Satisfy(apierrors.IsForbidden)))
	})
})
