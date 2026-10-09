package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
)

var _ = Describe("Counts", func() {
	const (
		app   = "quay.io/acme/app:v1"
		tool  = "quay.io/acme/tool:v2"
		other = "quay.io/acme/other:v3"
	)

	parse := func(s string) imagepath.Reference {
		GinkgoHelper()
		ref, err := imagepath.Parse(s)
		Expect(err).NotTo(HaveOccurred())
		return ref
	}
	observed := func(live ...string) Observed {
		o := Observed{ClusterID: clusterID, Path: destinationPath, SelfChecked: map[string]bool{}, Copied: map[string]bool{}}
		for _, s := range live {
			o.Live = append(o.Live, parse(s))
		}
		return o
	}
	pending := func(ref, origin string) kuikv1alpha1.PendingDeletion {
		return kuikv1alpha1.PendingDeletion{Ref: ref, Origin: origin, UnusedSince: metav1.Now()}
	}

	It("counts as running a reference whose destination reference a container carries", func() {
		o := observed(app, tool)
		o.Images = []string{mirrorpath.Destination(destinationPath, parse(app), clusterID), tool}

		Expect(Counts(o).Running).To(Equal(int32(1)))
	})

	It("counts as standby a reference a pod declares whose destination reference no container carries, copied or not", func() {
		o := observed(app, tool)
		o.Images = []string{app, tool}
		o.SelfChecked[app] = true

		c := Counts(o)
		Expect(c.Standby).To(Equal(int32(2)))
		Expect(c.Tracked).To(Equal(c.Running + c.Standby + c.Retained))
	})

	It("counts as retained each distinct origin of pendingDeletion, a pinned reference's two tags counting once", func() {
		pinned := "ghcr.io/acme/job:v42@" + digest
		o := observed()
		o.Status.PendingDeletion = []kuikv1alpha1.PendingDeletion{
			pending(destinationPath+"ghcr.io/acme/job:v42_"+clusterID, pinned),
			pending(destinationPath+"ghcr.io/acme/job:sha256-x_"+clusterID, pinned),
		}

		c := Counts(o)
		Expect(c.Retained).To(Equal(int32(1)))
		Expect(c.Tracked).To(Equal(int32(1)))
	})

	It("counts as orphan tags the pendingDeletion entries carrying no origin, one per tag", func() {
		o := observed()
		o.Status.PendingDeletion = []kuikv1alpha1.PendingDeletion{
			pending(destinationPath+"ghcr.io/acme/job:v42_"+clusterID, ""),
			pending(destinationPath+"ghcr.io/acme/job:sha256-x_"+clusterID, ""),
		}

		c := Counts(o)
		Expect(c.OrphanTags).To(Equal(int32(2)))
		Expect(c.Tracked).To(BeZero())
	})

	It("counts as available the references the last self-check found at the destination, and the rest as unavailable", func() {
		o := observed(app, tool)
		o.SelfChecked[app] = true

		c := Counts(o)
		Expect(c.Available).To(Equal(int32(1)))
		Expect(c.Unavailable).To(Equal(int32(1)))
	})

	It("counts as available a reference copied since the last self-check", func() {
		o := observed(app)
		o.Copied[app] = true

		Expect(Counts(o).Available).To(Equal(int32(1)))
	})

	It("counts as drifted every drifted reference, beyond the capped driftedImages", func() {
		o := observed(app, tool)
		o.Drifted = map[string]bool{app: true, tool: true}
		o.Status.DriftedImages = []kuikv1alpha1.MirrorDriftedImage{{Ref: app, UpstreamDigest: "sha256:b", CopiedDigest: "sha256:a"}}

		Expect(Counts(o).Drifted).To(Equal(int32(2)))
	})

	It("counts as missingSource every failing copy with reason SourceNotFound, beyond the capped failedImageCopies", func() {
		o := observed(app, tool, other)
		o.Failed = map[string]kuikv1alpha1.CopyFailureReason{
			app:   kuikv1alpha1.CopySourceNotFound,
			tool:  kuikv1alpha1.CopySourceNotFound,
			other: kuikv1alpha1.CopyQuotaExceeded,
		}
		o.Status.FailedImageCopies = []kuikv1alpha1.FailedImageCopy{{Ref: app, Reason: kuikv1alpha1.CopySourceNotFound}}

		Expect(Counts(o).MissingSource).To(Equal(int32(2)))
	})
})

var _ = Describe("CopyFailureEvents", func() {
	failure := func(ref string, reason kuikv1alpha1.CopyFailureReason) kuikv1alpha1.FailedImageCopy {
		return kuikv1alpha1.FailedImageCopy{Ref: ref, Reason: reason}
	}

	It("names a single failing image in its own event", func() {
		events := CopyFailureEvents([]kuikv1alpha1.FailedImageCopy{failure("quay.io/acme/tool:1.4", kuikv1alpha1.CopyUnauthorized)})

		Expect(events).To(HaveLen(1))
		Expect(events[0].Reason).To(Equal(kuikv1alpha1.CopyUnauthorized))
		Expect(events[0].Message).To(ContainSubstring("quay.io/acme/tool:1.4"))
	})

	It("coalesces the images failing with one reason in a pass under their narrowest common prefix, with their count", func() {
		events := CopyFailureEvents([]kuikv1alpha1.FailedImageCopy{
			failure("quay.io/acme/team/a:1", kuikv1alpha1.CopyUnauthorized),
			failure("quay.io/acme/team/b:2", kuikv1alpha1.CopyUnauthorized),
			failure("quay.io/acme/c:3", kuikv1alpha1.CopyUnauthorized),
		})

		Expect(events).To(HaveLen(1))
		Expect(events[0].Message).To(ContainSubstring("3 images under quay.io/acme/"))
	})

	It("falls back to the host as the prefix of images sharing nothing else", func() {
		events := CopyFailureEvents([]kuikv1alpha1.FailedImageCopy{
			failure("quay.io/acme/a:1", kuikv1alpha1.CopyQuotaExceeded),
			failure("quay.io/other/b:2", kuikv1alpha1.CopyQuotaExceeded),
		})

		Expect(events).To(HaveLen(1))
		Expect(events[0].Message).To(ContainSubstring("2 images under quay.io/"))
	})

	It("emits one event per reason when the failures of a pass differ in reason", func() {
		events := CopyFailureEvents([]kuikv1alpha1.FailedImageCopy{
			failure("quay.io/acme/a:1", kuikv1alpha1.CopyUnauthorized),
			failure("quay.io/acme/b:2", kuikv1alpha1.CopyQuotaExceeded),
		})

		Expect(events).To(HaveLen(2))
		Expect([]kuikv1alpha1.CopyFailureReason{events[0].Reason, events[1].Reason}).To(
			ConsistOf(kuikv1alpha1.CopyUnauthorized, kuikv1alpha1.CopyQuotaExceeded))
	})
})
