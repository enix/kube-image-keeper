package attribution

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

var (
	alternative = routing.Resource{Kind: routing.KindImageAlternative, Name: "quay"}
	mirror      = routing.Resource{Kind: routing.KindImageMirror, Name: "prod"}
	other       = routing.Resource{Kind: routing.KindImageAlternative, Name: "other"}
)

// The containers of the reference pod, one per state, and the live image of each.
var (
	containerOf = map[State]string{
		Untouched:      "plain",
		Rewritten:      "web",
		Conceded:       "proxy",
		Stale:          "api",
		NoAlternatives: "reloader",
	}
	liveImage = map[State]string{
		Untouched:      "docker.io/library/nginx:1.27",
		Rewritten:      "ghcr.io/acme/web:1",
		Conceded:       "internal.tld/proxy:1",
		Stale:          "quay.io/acme/api:2",
		NoAlternatives: "quay.io/acme/reloader:1",
	}
)

// referencePod has one container in each of the five states: web rewritten and api gone stale
// by the ImageAlternative, proxy conceded by it, reloader offered by both resources.
func referencePod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", Annotations: map[string]string{
			podrecord.AnnotationRewrites: `{` +
				`"web":{"by":"ImageAlternative/quay","origin":"quay.io/acme/web:1","rewrittenTo":"ghcr.io/acme/web:1","policy":"OnFailure"},` +
				`"api":{"by":"ImageAlternative/quay","origin":"quay.io/acme/api:1","rewrittenTo":"ghcr.io/acme/api:1","policy":"OnFailure"}}`,
			podrecord.AnnotationConcededRewrites: `{"proxy":{"by":"ImageAlternative/quay","origin":"quay.io/acme/proxy:1","rewrittenTo":"ghcr.io/acme/proxy:1","policy":"Always"}}`,
			podrecord.AnnotationNoAlternatives:   `{"reloader":["ImageAlternative/quay","ImageMirror/prod"]}`,
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "plain", Image: "nginx:1.27"},
			{Name: containerOf[Rewritten], Image: liveImage[Rewritten]},
			{Name: "proxy", Image: liveImage[Conceded]},
			{Name: "api", Image: liveImage[Stale]},
			{Name: "reloader", Image: liveImage[NoAlternatives]},
		}},
	}
}

func byName(containers []Container) map[string]Container {
	m := map[string]Container{}
	for _, c := range containers {
		m[c.Name] = c
	}
	return m
}

func names(containers []Container) []string {
	list := make([]string, 0, len(containers))
	for _, c := range containers {
		list = append(list, c.Name)
	}
	return list
}

var _ = Describe("Attribution", func() {
	Describe("Containers", func() {
		DescribeTable("the state of a container",
			func(state State) {
				c := byName(Containers(referencePod()))[containerOf[state]]
				Expect(c.State).To(Equal(state))
			},
			Entry("is untouched when no annotation names it", Untouched),
			Entry("is rewritten when rewrites names it and its live image equals rewrittenTo", Rewritten),
			Entry("is conceded when conceded-rewrites names it", Conceded),
			Entry("is stale when rewrites names it and its live image differs from rewrittenTo", Stale),
			Entry("is noAlternatives when no-alternatives names it", NoAlternatives),
		)
		DescribeTable("the origin of a container",
			func(state State) {
				c := byName(Containers(referencePod()))[containerOf[state]]
				if state == Rewritten {
					Expect(c.Origin).To(Equal("quay.io/acme/web:1"))
				} else {
					Expect(c.Origin).To(Equal(liveImage[state]))
				}
			},
			Entry("is the recorded origin of a rewritten container", Rewritten),
			Entry("is the live image of an untouched container", Untouched),
			Entry("is the live image of a noAlternatives container", NoAlternatives),
			Entry("is the live image of a conceded container, the other webhook's reference", Conceded),
			Entry("is the live image of a stale container", Stale),
		)

		It("normalises the live image, so a short name reads as its docker.io form", func() {
			c := byName(Containers(referencePod()))["plain"]
			Expect(c.Image).To(Equal("docker.io/library/nginx:1.27"))
		})

		It("returns the initContainers along with the containers", func() {
			pod := referencePod()
			pod.Spec.InitContainers = []corev1.Container{{Name: "migrate", Image: "quay.io/acme/migrate:1"}}
			Expect(names(Containers(pod))).To(ContainElements("migrate", "plain"))
		})

		It("never returns an ephemeralContainer", func() {
			pod := referencePod()
			pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug", Image: "busybox:1"},
			}}
			Expect(names(Containers(pod))).NotTo(ContainElement("debug"))
		})

		It("reads a container named only by a malformed annotation as untouched, the other annotations still read", func() {
			pod := referencePod()
			pod.Annotations[podrecord.AnnotationRewrites] = "{not json"
			containers := byName(Containers(pod))
			Expect(containers[containerOf[Rewritten]].State).To(Equal(Untouched))
			Expect(containers["proxy"].State).To(Equal(Conceded))
			Expect(containers["reloader"].State).To(Equal(NoAlternatives))
		})

		It("compares the live image to rewrittenTo as written, without normalising either", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					podrecord.AnnotationRewrites: `{"app":{"by":"ImageMirror/prod","origin":"quay.io/acme/web:1","rewrittenTo":"docker.io/library/nginx:1.27","policy":"Always"}}`,
				}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}}},
			}
			Expect(Containers(pod)[0].State).To(Equal(Stale))
		})
	})

	Describe("Container.StateFor", func() {
		It("keeps the state of a rewritten, conceded or stale container for the resource of its record", func() {
			containers := byName(Containers(referencePod()))
			for _, state := range []State{Rewritten, Conceded, Stale} {
				Expect(containers[containerOf[state]].StateFor(alternative)).To(Equal(state))
			}
		})

		It("reads a rewritten, conceded or stale container as untouched for any other resource", func() {
			containers := byName(Containers(referencePod()))
			for _, state := range []State{Rewritten, Conceded, Stale} {
				Expect(containers[containerOf[state]].StateFor(mirror)).To(Equal(Untouched))
			}
		})

		It("keeps noAlternatives for every resource that offered a candidate", func() {
			c := byName(Containers(referencePod()))["reloader"]
			Expect(c.StateFor(alternative)).To(Equal(NoAlternatives))
			Expect(c.StateFor(mirror)).To(Equal(NoAlternatives))
		})

		It("reads a noAlternatives container as untouched for a resource that did not offer one", func() {
			c := byName(Containers(referencePod()))["reloader"]
			Expect(c.StateFor(other)).To(Equal(Untouched))
		})
	})

	Describe("Static", func() {
		It("recognises a mirror pod by its kubernetes.io/config.mirror annotation", func() {
			pod := referencePod()
			pod.Annotations["kubernetes.io/config.mirror"] = "a1b2c3"
			Expect(Static(pod)).To(BeTrue())
		})

		It("reads a pod without the annotation as routable", func() {
			Expect(Static(referencePod())).To(BeFalse())
		})
	})

	DescribeTable("Live",
		func(phase corev1.PodPhase, live bool) {
			pod := referencePod()
			pod.Status.Phase = phase
			Expect(Live(pod)).To(Equal(live))
		},
		Entry("counts a Pending pod", corev1.PodPending, true),
		Entry("counts a Running pod", corev1.PodRunning, true),
		Entry("leaves out a Succeeded pod", corev1.PodSucceeded, false),
		Entry("leaves out a Failed pod", corev1.PodFailed, false),
		Entry("leaves out a pod in Unknown phase", corev1.PodUnknown, false),
	)
})
