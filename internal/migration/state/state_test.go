package state

import (
	"os"
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/internal/migration/inventory"
)

const (
	clusterMirror = "mirror.example.com/all"
	teamB         = "team-b"
	v2Namespace   = "keeper"
)

// running is an inventory of a cluster where v2 runs, with resources and pods.
func running(resources []inventory.Resource, pods ...inventory.Pod) *inventory.Inventory {
	return &inventory.Inventory{
		V2:          inventory.V2Installation{Namespace: v2Namespace, Deployment: "keeper-manager", Version: "2.3.1"},
		V2Resources: resources,
		Pods:        pods,
	}
}

// gone is an inventory of a cluster where v2 no longer runs, its webhook configuration and
// resources left behind.
func gone(resources []inventory.Resource, pods ...inventory.Pod) *inventory.Inventory {
	return &inventory.Inventory{
		V2:          inventory.V2Installation{Namespace: v2Namespace, WebhookConfiguration: "keeper-mutating-webhook"},
		V2Resources: resources,
		Pods:        pods,
	}
}

func mirror(status string) inventory.Resource {
	return inventory.Resource{
		Kind:       "ClusterImageSetMirror",
		Name:       "all",
		Finalizers: []string{"kuik.enix.io/mirror-cleanup"},
		Spec: map[string]any{"mirrors": []any{map[string]any{
			"registry": "mirror.example.com", "path": "all",
		}}},
		Status: map[string]any{"matchingImages": []any{map[string]any{"image": status}}},
	}
}

func rewritten(name, image string) inventory.Pod {
	return inventory.Pod{
		Namespace: teamB, Name: name,
		Owner: &inventory.Owner{Kind: "Deployment", Name: name},
		Containers: []inventory.Container{{
			Name: "app", Image: clusterMirror + "/library/" + image,
			Origin: "docker.io/library/" + image, OriginFrom: inventory.FromAnnotation,
		}},
	}
}

var _ = Describe("State file", func() {
	var path string

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "kuik-migration-state.json")
	})

	It("is created on the first run with the v2 resources and the rewritten pods", func() {
		outcome, err := Sync(path, running([]inventory.Resource{mirror("docker.io/library/nginx:1.27")}, rewritten("web", "nginx:1.27")))
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Created))

		file, err := Load(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(file).To(Equal(&File{
			FormatVersion: FormatVersion,
			V2Version:     "2.3.1",
			V2Namespace:   v2Namespace,
			Resources:     []inventory.Resource{mirror("docker.io/library/nginx:1.27")},
			Pods:          []inventory.Pod{rewritten("web", "nginx:1.27")},
		}))
	})

	It("keeps the pods of earlier runs and adds the ones rewritten since", func() {
		_, err := Sync(path, running(nil, rewritten("web", "nginx:1.27"), rewritten("cache", "redis:7")))
		Expect(err).NotTo(HaveOccurred())

		// web was deleted, worker created since.
		outcome, err := Sync(path, running(nil, rewritten("cache", "redis:7"), rewritten("worker", "busybox:1.36")))
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Updated))

		file, err := Load(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(file).NotTo(BeNil())
		Expect(file.Pods).To(ConsistOf(
			rewritten("web", "nginx:1.27"),
			rewritten("cache", "redis:7"),
			rewritten("worker", "busybox:1.36"),
		))
	})

	It("takes the latest status of the v2 resources while v2 runs", func() {
		_, err := Sync(path, running([]inventory.Resource{mirror("docker.io/library/nginx:1.26")}))
		Expect(err).NotTo(HaveOccurred())

		_, err = Sync(path, running([]inventory.Resource{mirror("docker.io/library/nginx:1.27")}))
		Expect(err).NotTo(HaveOccurred())

		file, err := Load(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(file).NotTo(BeNil())
		Expect(file.Resources).To(Equal([]inventory.Resource{mirror("docker.io/library/nginx:1.27")}))
	})

	It("is left unchanged once v2 is gone", func() {
		_, err := Sync(path, running([]inventory.Resource{mirror("docker.io/library/nginx:1.27")}, rewritten("web", "nginx:1.27")))
		Expect(err).NotTo(HaveOccurred())
		before, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())

		outcome, err := Sync(path, gone([]inventory.Resource{mirror("docker.io/library/nginx:1.28")}, rewritten("worker", "busybox:1.36")))
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Unchanged))
		Expect(os.ReadFile(path)).To(Equal(before))

		// Without a file, none is written once v2 is gone.
		missing := filepath.Join(GinkgoT().TempDir(), "kuik-migration-state.json")
		outcome, err = Sync(missing, gone(nil, rewritten("worker", "busybox:1.36")))
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Missing))
		Expect(missing).NotTo(BeAnExistingFile())
	})

	It("refuses a file that is not a state file", func() {
		for _, content := range []string{`{"pods": []}`, `{"formatVersion": 0}`} {
			other := []byte(content)
			Expect(os.WriteFile(path, other, 0o600)).To(Succeed())

			_, err := Load(path)
			Expect(err).To(MatchError(ContainSubstring("not a kuik migration state file")), content)

			_, err = Sync(path, running(nil, rewritten("web", "nginx:1.27")))
			Expect(err).To(MatchError(ContainSubstring("not a kuik migration state file")), content)
			Expect(os.ReadFile(path)).To(Equal(other), content)
		}
	})

	It("refuses a file written by a newer format", func() {
		newer := []byte(`{"formatVersion": ` + strconv.Itoa(FormatVersion+1) + `, "pods": []}`)
		Expect(os.WriteFile(path, newer, 0o600)).To(Succeed())

		_, err := Load(path)
		Expect(err).To(MatchError(ContainSubstring("format version " + strconv.Itoa(FormatVersion+1))))

		_, err = Sync(path, running(nil, rewritten("web", "nginx:1.27")))
		Expect(err).To(MatchError(ContainSubstring("format version " + strconv.Itoa(FormatVersion+1))))
		Expect(os.ReadFile(path)).To(Equal(newer))
	})
})
