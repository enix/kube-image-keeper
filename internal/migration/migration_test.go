package migration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kit "github.com/enix/kube-image-keeper/internal/migration/inventory/inventorytest"
	"github.com/enix/kube-image-keeper/internal/migration/state"
)

const (
	v2Namespace = "keeper"
	v3Namespace = "kuik-system"
	teamB       = "team-b"
	v2Secret    = "kuik-registry-credentials-0123456789abcdef"
)

// v2Cluster is a cluster where v2 runs, rewrote a pod and copied a pull secret.
func v2Cluster() []client.Object {
	rewritten := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace:   teamB,
		Name:        "web",
		Annotations: map[string]string{"kuik.enix.io/original-images": `{"app":"nginx:1.27"}`},
	}}
	rewritten.Spec.Containers = []corev1.Container{{Name: "app", Image: "mirror.example.com/all/library/nginx:1.27"}}
	rewritten.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: v2Secret}}
	return append(kit.V2CRDs(),
		kit.V2Manager(v2Namespace, "keeper", "2.3.1"),
		kit.V2Webhook(v2Namespace, "keeper"),
		kit.Object("ClusterImageSetMirror", "", "all", kit.Mirrors("mirror.example.com", "all"), nil),
		kit.V2PullSecret(teamB, v2Secret, "ClusterImageSetMirror", "all", "uid-all"),
		rewritten,
	)
}

// v3Cluster is a cluster where only v3 runs.
func v3Cluster() []client.Object {
	return append(kit.V3CRDs(),
		kit.V3Deployment(v3Namespace, "kuik-webhook", true, "webhook"),
		kit.V3Deployment(v3Namespace, "kuik-reconciler", true, "reconciler"),
		kit.V3Deployment(v3Namespace, "kuik-secret-syncer", true, "secret-syncer"),
		kit.V3Webhook(v3Namespace, "kuik"),
		kit.ClusterIDConfigMap(v3Namespace, "blue-cluster"),
	)
}

var _ = Describe("migrate", func() {
	var (
		out, errOut *bytes.Buffer
		stateFile   string
	)

	BeforeEach(func() {
		out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
		stateFile = filepath.Join(GinkgoT().TempDir(), DefaultStateFile)
	})

	run := func(objects ...client.Object) int {
		c, guard := kit.Reader(objects...)
		code := Run(context.Background(), c, Options{StateFile: stateFile, Namespace: v3Namespace, Out: out, Err: errOut})
		Expect(guard.Writes).To(BeZero())
		Expect(guard.SecretReads).To(BeZero())
		return code
	}

	It("prints the v2 installation, resources, rewritten pods and pull secrets it found", func() {
		run(v2Cluster()...)

		Expect(out.String()).To(And(
			ContainSubstring("keeper/keeper-manager"),
			ContainSubstring("2.3.1"),
			ContainSubstring("keeper-mutating-webhook"),
			ContainSubstring("ClusterImageSetMirror all"),
			ContainSubstring("team-b/web"),
			ContainSubstring("mirror.example.com/all/library/nginx:1.27"),
			ContainSubstring("docker.io/library/nginx:1.27"),
			ContainSubstring("team-b/"+v2Secret),
		))
	})

	It("saves the state file in the current directory", func() {
		dir := GinkgoT().TempDir()
		previous, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chdir(dir)).To(Succeed())
		DeferCleanup(os.Chdir, previous)
		c, _ := kit.Reader(v2Cluster()...)

		Run(context.Background(), c, Options{Namespace: v3Namespace, Out: out})

		file, err := state.Load(filepath.Join(dir, DefaultStateFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Pods).To(ConsistOf(HaveField("Name", "web")))
		Expect(file.Resources).To(ConsistOf(HaveField("Kind", "ClusterImageSetMirror")))
	})

	It("stops with an explanation when v2 is gone, v2 leftovers remain and no state file exists", func() {
		code := run(append(kit.V2CRDs(),
			kit.V2Webhook(v2Namespace, "keeper"),
			kit.Object("ClusterImageSetMirror", "", "all", kit.Mirrors("mirror.example.com", "all"), nil),
		)...)

		Expect(code).NotTo(BeZero())
		Expect(errOut.String()).To(ContainSubstring("no state file"))
		Expect(out.String()).NotTo(ContainSubstring("no state file"))
		Expect(stateFile).NotTo(BeAnExistingFile())
	})

	It("exits 0 when the cluster holds no trace of v2 and no state file exists", func() {
		code := run(v3Cluster()...)

		Expect(code).To(BeZero())
		Expect(out.String()).To(ContainSubstring("no trace of kuik v2"))
		Expect(stateFile).NotTo(BeAnExistingFile())
	})

	It("exits non-zero while the migration is not complete", func() {
		Expect(run(append(v2Cluster(), v3Cluster()...)...)).NotTo(BeZero())
		Expect(out.String()).To(ContainSubstring("not complete"))

		// v2 is gone without a trace, but the state file of the earlier run remains: the
		// later steps still need it.
		out.Reset()
		Expect(stateFile).To(BeAnExistingFile())
		Expect(run(v3Cluster()...)).NotTo(BeZero())
		Expect(out.String()).To(ContainSubstring("not complete"))
		Expect(errOut.String()).To(BeEmpty())

		// A state file the run cannot read is an error, reported apart from the output.
		Expect(os.WriteFile(stateFile, []byte(`{"formatVersion": 999}`), 0o600)).To(Succeed())
		out.Reset()
		Expect(run(v3Cluster()...)).NotTo(BeZero())
		Expect(errOut.String()).To(ContainSubstring("format version 999"))
		Expect(out.String()).NotTo(ContainSubstring("format version 999"))
	})

	It("finds a pod rewritten under a mirror path whose v2 resource is gone", func() {
		ism := kit.Object("ImageSetMirror", "team-a", "team-a", kit.Mirrors("mirror.example.com", "team-a"), nil)
		Expect(run(append(v2Cluster(), ism)...)).NotTo(BeZero())

		// The ImageSetMirror is deleted while v2 runs; a pod it rewrote remains.
		rewritten := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace:   "team-a",
			Name:        "api",
			Annotations: map[string]string{"kuik.enix.io/original-images": `{"app":"registry.example.com/acme/api:2"}`},
		}}
		rewritten.Spec.Containers = []corev1.Container{{Name: "app", Image: "mirror.example.com/team-a/acme/api:2"}}
		out.Reset()
		Expect(run(append(v2Cluster(), rewritten)...)).NotTo(BeZero())

		Expect(out.String()).To(ContainSubstring("team-a/api"))
		file, err := state.Load(stateFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Pods).To(ContainElement(And(
			HaveField("Namespace", "team-a"),
			HaveField("Name", "api"),
			HaveField("Containers", ConsistOf(HaveField("Origin", "registry.example.com/acme/api:2"))),
		)))
	})
})
