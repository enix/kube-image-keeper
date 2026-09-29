package auth

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// Fixtures: the image every resolution is for, and the Secret names the specs declare.
const (
	acmeFoo       = "quay.io/acme/foo"
	acmeImage     = acmeFoo + ":v1"
	entryCreds    = "entry-creds"
	podCreds      = "pod-creds"
	fallbackCreds = "fallback-creds"

	// restrictedRole grants the restricted identity Secrets in the cluster resource namespace.
	restrictedRole = "secret-reader"
)

// newNamespace creates a namespace with a generated name and returns that name.
func newNamespace() string {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "auth-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns.Name
}

// newSecret creates a Secret of that type, with the data key the API server requires for it.
func newSecret(namespace, name string, secretType corev1.SecretType) {
	GinkgoHelper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Type:       secretType,
	}
	secret.Data = map[string][]byte{"token": []byte("not a registry credential")}
	if secretType == corev1.SecretTypeDockerConfigJson {
		secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}
	}
	if secretType == corev1.SecretTypeDockercfg {
		secret.Data = map[string][]byte{corev1.DockerConfigKey: []byte(`{}`)}
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

// podWithPullSecrets is a pod in namespace naming those pull secrets. It is never created:
// the resolver reads only its namespace and its spec.
func podWithPullSecrets(namespace string, names ...string) *corev1.Pod {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "app"}}
	for _, name := range names {
		pod.Spec.ImagePullSecrets = append(pod.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: name})
	}
	return pod
}

func secretRefAuth(name string) *kuikv1alpha1.Auth {
	return &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}
}

func groupWithSecret(group, name string) FallbackAuthEntry {
	return FallbackAuthEntry{RepositoryGroup: group, SecretRef: &kuikv1alpha1.SecretReference{Name: name}}
}

func mustParse(s string) imagepath.Reference {
	GinkgoHelper()
	ref, err := imagepath.Parse(s)
	Expect(err).NotTo(HaveOccurred())
	return ref
}

// sources lists the steps the credentials come from, in order.
func sources(creds []Credential) []Source {
	out := make([]Source, 0, len(creds))
	for _, c := range creds {
		out = append(out, c.Source)
	}
	return out
}

// secretNames lists the names of the Secrets a credential selected.
func secretNames(c Credential) []string {
	out := make([]string, 0, len(c.Secrets))
	for _, s := range c.Secrets {
		out = append(out, s.Name)
	}
	return out
}

var _ = Describe("Resolver", func() {
	var clusterNS, podNS string

	BeforeEach(func() {
		clusterNS = newNamespace()
		podNS = newNamespace()
	})

	// resolve runs a resolution for acmeImage with a resolver reading through k8sClient.
	resolve := func(mode Mode, fallback []FallbackAuthEntry, entryAuth *kuikv1alpha1.Auth, pod *corev1.Pod) ([]Credential, error) {
		GinkgoHelper()
		r := NewResolver(k8sClient, clusterNS, mode)
		Expect(r.SetFallbackAuth(fallback)).To(Succeed())
		return r.Resolve(ctx, mustParse(acmeImage), entryAuth, pod)
	}

	// declareEveryStep creates a Secret for each of the first three steps.
	declareEveryStep := func() ([]FallbackAuthEntry, *kuikv1alpha1.Auth, *corev1.Pod) {
		GinkgoHelper()
		newSecret(clusterNS, entryCreds, corev1.SecretTypeDockerConfigJson)
		newSecret(podNS, podCreds, corev1.SecretTypeDockerConfigJson)
		newSecret(clusterNS, fallbackCreds, corev1.SecretTypeDockerConfigJson)
		return []FallbackAuthEntry{groupWithSecret("quay.io/acme", fallbackCreds)},
			secretRefAuth(entryCreds), podWithPullSecrets(podNS, podCreds)
	}

	Context("in reconciler mode", func() {
		It("yields the entry's auth, the pod's pull secrets, the matching fallbackAuth entry and anonymous, in that order", func() {
			fallback, entryAuth, pod := declareEveryStep()
			creds, err := resolve(ModeReconciler, fallback, entryAuth, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal([]Source{SourceEntryAuth, SourcePodPullSecret, SourceFallbackAuth, SourceAnonymous}))
			Expect(secretNames(creds[0])).To(Equal([]string{entryCreds}))
			Expect(secretNames(creds[1])).To(Equal([]string{podCreds}))
			Expect(secretNames(creds[2])).To(Equal([]string{fallbackCreds}))
			Expect(creds[3].Secrets).To(BeEmpty())
		})

		It("yields anonymous alone when nothing is declared and no fallbackAuth entry matches", func() {
			newSecret(clusterNS, fallbackCreds, corev1.SecretTypeDockerConfigJson)
			creds, err := resolve(ModeReconciler, []FallbackAuthEntry{groupWithSecret("quay.io/other", fallbackCreds)},
				nil, podWithPullSecrets(podNS))
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal([]Source{SourceAnonymous}))
		})

		DescribeTable("picks the most specific fallbackAuth entry in a list mixing both forms",
			func(image, wantSecret string) {
				for _, name := range []string{"host-creds", "acme-creds", "foo-creds"} {
					newSecret(clusterNS, name, corev1.SecretTypeDockerConfigJson)
				}
				r := NewResolver(k8sClient, clusterNS, ModeReconciler)
				Expect(r.SetFallbackAuth([]FallbackAuthEntry{
					groupWithSecret("quay.io", "host-creds"),
					groupWithSecret("quay.io/acme", "acme-creds"),
					{Repository: acmeFoo, SecretRef: &kuikv1alpha1.SecretReference{Name: "foo-creds"}},
				})).To(Succeed())
				creds, err := r.Resolve(ctx, mustParse(image), nil, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(sources(creds)).To(Equal([]Source{SourceFallbackAuth, SourceAnonymous}))
				Expect(secretNames(creds[0])).To(Equal([]string{wantSecret}))
			},
			Entry("a repository over a repositoryGroup", acmeImage, "foo-creds"),
			Entry("a deeper repositoryGroup over a shallower one", "quay.io/acme/bar:v1", "acme-creds"),
		)

		It("resolves with the fallbackAuth entries set last", func() {
			newSecret(clusterNS, "old-creds", corev1.SecretTypeDockerConfigJson)
			newSecret(clusterNS, "new-creds", corev1.SecretTypeDockerConfigJson)
			r := NewResolver(k8sClient, clusterNS, ModeReconciler)
			Expect(r.SetFallbackAuth([]FallbackAuthEntry{groupWithSecret("quay.io", "old-creds")})).To(Succeed())
			Expect(r.SetFallbackAuth([]FallbackAuthEntry{groupWithSecret("quay.io/acme", "new-creds")})).To(Succeed())
			creds, err := r.Resolve(ctx, mustParse(acmeImage), nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(secretNames(creds[0])).To(Equal([]string{"new-creds"}))

			creds, err = r.Resolve(ctx, mustParse("quay.io/other/foo:v1"), nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal([]Source{SourceAnonymous}))
		})
	})

	Context("in webhook mode", func() {
		It("yields the entry's auth and the pod's pull secrets, skips a matching fallbackAuth entry and ends with anonymous", func() {
			fallback, entryAuth, pod := declareEveryStep()
			creds, err := resolve(ModeWebhook, fallback, entryAuth, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal([]Source{SourceEntryAuth, SourcePodPullSecret, SourceAnonymous}))
		})
	})

	Context("with a declared secretRef", func() {
		It("reports a Secret of that name found only outside the cluster resource namespace as not found", func() {
			newSecret(podNS, entryCreds, corev1.SecretTypeDockerConfigJson)
			_, err := resolve(ModeReconciler, nil, secretRefAuth(entryCreds), podWithPullSecrets(podNS))
			var notFound *ErrSecretNotFound
			Expect(errors.As(err, &notFound)).To(BeTrue(), "got %v", err)
			Expect(notFound.Name).To(Equal(entryCreds))
		})

		DescribeTable("reports as malformed a Secret whose type is not kubernetes.io/dockerconfigjson",
			func(secretType string) {
				newSecret(clusterNS, entryCreds, corev1.SecretType(secretType))
				_, err := resolve(ModeReconciler, nil, secretRefAuth(entryCreds), nil)
				var malformed *ErrSecretMalformed
				Expect(errors.As(err, &malformed)).To(BeTrue(), "got %v", err)
				Expect(malformed.Name).To(Equal(entryCreds))
			},
			Entry("Opaque", "Opaque"),
			Entry("the legacy kubernetes.io/dockercfg", "kubernetes.io/dockercfg"),
		)
	})

	Context("with the pod's pull secrets", func() {
		It("reads them in the pod's namespace, not in the cluster resource namespace", func() {
			newSecret(clusterNS, podCreds, corev1.SecretTypeDockerConfigJson)
			creds, err := resolve(ModeReconciler, nil, nil, podWithPullSecrets(podNS, podCreds))
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal([]Source{SourceAnonymous}))
		})

		It("moves on to fallbackAuth when the API server forbids reading the pod's namespace", func() {
			fallback, _, pod := declareEveryStep()

			By("reading through an identity granted Secrets in the cluster resource namespace only")
			user, err := testEnv.AddUser(envtest.User{Name: "kuik-restricted"}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: restrictedRole},
				Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}}},
			})).To(Succeed())
			Expect(k8sClient.Create(ctx, &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: restrictedRole},
				RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: restrictedRole},
				Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "kuik-restricted"}},
			})).To(Succeed())
			restricted, err := client.New(user.Config(), client.Options{Scheme: scheme.Scheme})
			Expect(err).NotTo(HaveOccurred())

			r := NewResolver(restricted, clusterNS, ModeReconciler)
			Expect(r.SetFallbackAuth(fallback)).To(Succeed())
			creds, err := r.Resolve(ctx, mustParse(acmeImage), nil, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal([]Source{SourceFallbackAuth, SourceAnonymous}))
		})

		It("keeps a pull secret of the legacy kubernetes.io/dockercfg type, which the kubelet reads", func() {
			newSecret(podNS, podCreds, corev1.SecretTypeDockercfg)
			creds, err := resolve(ModeReconciler, nil, nil, podWithPullSecrets(podNS, podCreds))
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal([]Source{SourcePodPullSecret, SourceAnonymous}))
			Expect(secretNames(creds[0])).To(Equal([]string{podCreds}))
		})

		DescribeTable("skips a pull secret the kubelet would skip and keeps the others",
			func(secretType string, exists bool) {
				if exists {
					newSecret(podNS, "skipped", corev1.SecretType(secretType))
				}
				newSecret(podNS, podCreds, corev1.SecretTypeDockerConfigJson)
				creds, err := resolve(ModeReconciler, nil, nil, podWithPullSecrets(podNS, "skipped", podCreds))
				Expect(err).NotTo(HaveOccurred())
				Expect(sources(creds)).To(Equal([]Source{SourcePodPullSecret, SourceAnonymous}))
				Expect(secretNames(creds[0])).To(Equal([]string{podCreds}))
			},
			Entry("a Secret that does not exist", "", false),
			Entry("a Secret of type Opaque", "Opaque", true),
		)
	})

	DescribeTable("yields a pending credential for a provider and moves on to the next step",
		func(declaredOn string) {
			provider := &kuikv1alpha1.Provider{Name: kuikv1alpha1.ProviderAWS}
			newSecret(podNS, podCreds, corev1.SecretTypeDockerConfigJson)
			var entryAuth *kuikv1alpha1.Auth
			var fallback []FallbackAuthEntry
			want := []Source{SourcePodPullSecret, SourceAnonymous}
			if declaredOn == "entry" {
				entryAuth = &kuikv1alpha1.Auth{Provider: provider}
				want = append([]Source{SourceProvider}, want...)
			} else {
				fallback = []FallbackAuthEntry{{RepositoryGroup: "quay.io/acme", Provider: provider}}
				want = []Source{SourcePodPullSecret, SourceProvider, SourceAnonymous}
			}
			creds, err := resolve(ModeReconciler, fallback, entryAuth, podWithPullSecrets(podNS, podCreds))
			Expect(err).NotTo(HaveOccurred())
			Expect(sources(creds)).To(Equal(want))
			for _, c := range creds {
				if c.Source == SourceProvider {
					Expect(c.Pending).To(BeTrue())
					Expect(c.Secrets).To(BeEmpty())
				}
			}
		},
		Entry("on the entry's auth", "entry"),
		Entry("on a fallbackAuth entry", "fallbackAuth"),
	)
})
