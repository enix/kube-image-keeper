package secretsyncer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/test/utils"
)

// The syncer runs as a user bound to the generated secret-syncer roles, so that every spec
// also proves the permissions of docs/v3/architecture.md, "Permissions", are enough. The
// specs set things up with the admin client. envtest runs no controller manager, so no
// garbage collector: the collection of a Secret with its resource is the e2e suite's.

// installNamespace is the namespace the generated Roles name.
const installNamespace = "kuik-system"

const (
	// syncerUser is the identity the syncer runs as.
	syncerUser = "kuik-secret-syncer"
	// syncerRole names the generated ClusterRole and Role of the syncer, and their bindings.
	syncerRole = "secret-syncer"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	syncerCfg *rest.Config
	k8sClient client.Client
)

func TestSecretSyncer(t *testing.T) {
	if testing.Short() {
		t.Skip("envtest suite")
	}
	RegisterFailHandler(Fail)

	RunSpecs(t, "Secret Syncer Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	Expect(kuikv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := utils.EnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: installNamespace}})).To(Succeed())
	createGeneratedRoles()
	bindSyncerRoles()

	user, err := testEnv.AddUser(envtest.User{Name: syncerUser}, nil)
	Expect(err).NotTo(HaveOccurred())
	syncerCfg = user.Config()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// createGeneratedRoles creates every role of config/rbac/role.yaml, as controller-gen
// writes them from the markers.
func createGeneratedRoles() {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "rbac", "role.yaml"))
	Expect(err).NotTo(HaveOccurred())
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var object unstructured.Unstructured
		err := decoder.Decode(&object.Object)
		if errors.Is(err, io.EOF) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		if len(object.Object) == 0 {
			continue
		}
		Expect(k8sClient.Create(ctx, &object)).To(Succeed())
	}
}

// bindSyncerRoles binds the secret-syncer ClusterRole and Role to syncerUser.
func bindSyncerRoles() {
	subjects := []rbacv1.Subject{{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: syncerUser}}
	Expect(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: syncerRole},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: syncerRole},
		Subjects:   subjects,
	})).To(Succeed())
	Expect(k8sClient.Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: installNamespace, Name: syncerRole},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: syncerRole},
		Subjects:   subjects,
	})).To(Succeed())
}
