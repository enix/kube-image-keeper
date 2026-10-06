package plan

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

const (
	clusterID       = "cluster-a"
	destinationPath = "registry.tld/mirror/"
	digest          = "sha256:594cee0bdd01eca2a3d4da1c2c1d4cc13a9a1b2a0e0e8ac4c6b8fe8d8a0b0c1d"
)

// container is one container of a test pod: its name and the image it runs.
type container struct{ name, image string }

// runningPod is a Running pod whose containers run the given images.
func runningPod(containers ...container) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	for _, c := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: c.name, Image: c.image})
	}
	return pod
}

// rewritten records on pod that kuik rewrote container from origin to the image it runs now.
func rewritten(pod *corev1.Pod, name, origin, by string) *corev1.Pod {
	before, _ := podrecord.Read(pod)
	after, _ := podrecord.Read(pod)
	for _, c := range pod.Spec.Containers {
		if c.Name == name {
			after.Rewrites[name] = podrecord.Rewrite{By: by, Origin: origin, RewrittenTo: c.Image, Policy: "OnFailure"}
		}
	}
	after.Write(pod, before)
	return pod
}
