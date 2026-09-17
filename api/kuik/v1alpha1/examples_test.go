package v1alpha1

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const examplesDir = "../../../docs/v3/examples"

// v3Documents returns the documents of a multi-document YAML file that belong to this API
// group: the examples of the spec pair a v2 resource with its v3 counterpart.
func v3Documents(path string) []*unstructured.Unstructured {
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())

	var docs []*unstructured.Unstructured
	for doc := range strings.SplitSeq(string(raw), "\n---") {
		u := &unstructured.Unstructured{}
		Expect(yaml.Unmarshal([]byte(doc), &u.Object)).To(Succeed(), path)
		if u.GetAPIVersion() != SchemeGroupVersion.String() {
			continue
		}
		switch u.GetKind() {
		case "ImageAlternative", "ImageMirror", "ImageMonitor":
			docs = append(docs, u)
		}
	}
	return docs
}

var _ = Describe("The examples of the specification", func() {
	files, globErr := filepath.Glob(filepath.Join(examplesDir, "*.yaml"))

	It("ship at least one example file", func() {
		Expect(globErr).NotTo(HaveOccurred())
		Expect(files).NotTo(BeEmpty())
	})

	entries := make([]TableEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, Entry(filepath.Base(f), f))
	}

	DescribeTable("are accepted by the API server as written",
		func(path string) {
			docs := v3Documents(path)
			Expect(docs).NotTo(BeEmpty(), "%s carries no v3 resource", path)
			for _, u := range docs {
				Expect(k8sClient.Create(ctx, u)).To(Succeed(), "%s: %s/%s", path, u.GetKind(), u.GetName())
				DeferCleanup(func(u *unstructured.Unstructured) {
					Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, u))).To(Succeed())
				}, u)
			}
		},
		entries,
	)
})
