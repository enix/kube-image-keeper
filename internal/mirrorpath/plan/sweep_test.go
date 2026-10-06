package plan

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
)

var _ = Describe("Sweep", func() {
	const (
		repository = destinationPath + "quay.io/acme/app"
		retention  = time.Hour
		// ownTag is the tag this cluster writes for quay.io/acme/app:v1.
		ownTag = "v1_" + clusterID
		// otherTag is the tag another cluster writes for the same image.
		otherTag = "v1_cluster-b"
	)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	parse := func(s string) imagepath.Reference {
		GinkgoHelper()
		ref, err := imagepath.Parse(s)
		Expect(err).NotTo(HaveOccurred())
		return ref
	}
	refs := func(ss ...string) []imagepath.Reference {
		out := make([]imagepath.Reference, 0, len(ss))
		for _, s := range ss {
			out = append(out, parse(s))
		}
		return out
	}
	entry := func(tag, origin string, unusedSince time.Time) kuikv1alpha1.PendingDeletion {
		return kuikv1alpha1.PendingDeletion{Ref: repository + ":" + tag, Origin: origin, UnusedSince: metav1.NewTime(unusedSince)}
	}
	sweep := func(live []imagepath.Reference, listed []string, pending ...kuikv1alpha1.PendingDeletion) Sweep {
		return Sweep{
			ClusterID: clusterID,
			Path:      destinationPath,
			Retention: retention,
			Live:      live,
			Listed:    map[string][]string{repository: listed},
			Pending:   pending,
			Now:       now,
		}
	}
	tagsOf := func(pending []kuikv1alpha1.PendingDeletion) []string {
		out := make([]string, 0, len(pending))
		for _, e := range pending {
			out = append(out, strings.TrimPrefix(e.Ref, repository+":"))
		}
		return out
	}

	Context("the tags it reasons about", func() {
		It("keeps only the tags ending in this cluster's suffix", func() {
			result := sweep(nil, []string{ownTag, otherTag, "v1"}).Run()

			Expect(tagsOf(result.Pending)).To(ConsistOf(ownTag))
		})

		It("never records as unused the truncated and hashed tag of a long reference still desired", func() {
			long := parse("quay.io/acme/app:" + strings.Repeat("a", 125))
			truncated := mirrorpath.Tags(long, clusterID)[0]

			result := sweep([]imagepath.Reference{long}, []string{truncated}).Run()

			Expect(result.Pending).To(BeEmpty())
		})
	})

	Context("pendingDeletion", func() {
		It("records a listed tag of this cluster outside the expected set, with no origin", func() {
			result := sweep(refs("quay.io/acme/app:v2"), []string{ownTag, "v2_cluster-a"}).Run()

			Expect(result.Pending).To(ConsistOf(entry(ownTag, "", now)))
		})

		It("records the tags of a reference whose last pod went, with its origin", func() {
			pending := Release(clusterID, destinationPath, refs("quay.io/acme/app:v1@"+digest), nil, nil, now)

			anchor := strings.Replace(digest, ":", "-", 1) + "_" + clusterID
			Expect(pending).To(ConsistOf(
				entry(ownTag, "quay.io/acme/app:v1@"+digest, now),
				entry(anchor, "quay.io/acme/app:v1@"+digest, now),
			))
		})

		It("never records a tag another desired reference still expects, a tagged and a pinned reference sharing it", func() {
			pending := Release(clusterID, destinationPath, refs("quay.io/acme/app:v1"), refs("quay.io/acme/app:v1@"+digest), nil, now)

			Expect(pending).To(BeEmpty())
		})

		It("leaves the unusedSince of an entry already recorded untouched", func() {
			earlier := now.Add(-10 * time.Minute)

			result := sweep(nil, []string{ownTag}, entry(ownTag, "", earlier)).Run()

			Expect(result.Pending).To(ConsistOf(entry(ownTag, "", earlier)))
		})

		It("drops an entry whose tag the desired state expects again", func() {
			result := sweep(refs("quay.io/acme/app:v1"), []string{ownTag},
				entry(ownTag, "quay.io/acme/app:v1", now.Add(-time.Minute))).Run()

			Expect(result.Pending).To(BeEmpty())
		})
	})

	Context("retention", func() {
		It("deletes an entry whose unusedSince is older than cleanup.retention", func() {
			result := sweep(nil, []string{ownTag}, entry(ownTag, "", now.Add(-retention-time.Second))).Run()

			Expect(result.Delete).To(ConsistOf(repository + ":v1_cluster-a"))
		})

		It("holds an entry whose retention has not elapsed", func() {
			result := sweep(nil, []string{ownTag}, entry(ownTag, "", now.Add(-retention+time.Second))).Run()

			Expect(result.Delete).To(BeEmpty())
			Expect(tagsOf(result.Pending)).To(ConsistOf(ownTag))
		})

		It("holds an entry younger than the deletion buffer under a retention of 0, and deletes it once older", func() {
			s := sweep(nil, []string{ownTag}, entry(ownTag, "", now.Add(-5*time.Second)))
			s.Retention = 0
			Expect(s.Run().Delete).To(BeEmpty())

			s.Pending = []kuikv1alpha1.PendingDeletion{entry(ownTag, "", now.Add(-DeletionBuffer-time.Second))}
			Expect(s.Run().Delete).To(ConsistOf(repository + ":v1_cluster-a"))
		})
	})

	Context("repositories", func() {
		It("retires a repository where no tag of this cluster remains, whatever other clusters hold there", func() {
			result := sweep(nil, []string{otherTag, "v2_cluster-b"}).Run()

			Expect(result.Retire).To(ConsistOf(repository))
		})

		It("keeps a repository where a tag of this cluster remains", func() {
			result := sweep(refs("quay.io/acme/app:v1"), []string{ownTag, otherTag}).Run()

			Expect(result.Retire).To(BeEmpty())
		})
	})
})
