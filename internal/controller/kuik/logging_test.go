package kuik

import (
	"bytes"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/enix/kube-image-keeper/internal/routing"
)

var _ = Describe("The logger of a reconcile", func() {
	// logged returns the line a reconcile of kind logs for the object named name.
	logged := func(kind, name string) map[string]any {
		GinkgoHelper()
		out := &bytes.Buffer{}
		base := zap.New(zap.WriteTo(out), zap.JSONEncoder())
		reconcileLog(base, kind)(&reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}).Info("Updated ImageMirror status")
		var line map[string]any
		Expect(json.Unmarshal(bytes.TrimSpace(out.Bytes()), &line)).To(Succeed())
		return line
	}

	DescribeTable("names the logger after the kind and the resource as <kind>/<name>",
		func(kind, logger string) {
			line := logged(kind, "prod")
			Expect(line).To(HaveKeyWithValue("logger", logger))
			Expect(line).To(HaveKeyWithValue("resource", kind+"/prod"))
		},
		Entry("of an ImageAlternative", routing.KindImageAlternative, "imagealternative"),
		Entry("of an ImageMirror", routing.KindImageMirror, "imagemirror"),
		Entry("of an ImageMonitor", routing.KindImageMonitor, "imagemonitor"),
	)

	It("leaves out the keys controller-runtime names the object with, which resource replaces", func() {
		line := logged(routing.KindImageMirror, "prod")
		Expect(line).NotTo(HaveKey(routing.KindImageMirror))
		Expect(line).NotTo(HaveKey("name"))
		Expect(line).NotTo(HaveKey("namespace"))
	})
})
