package pullsecret

import (
	"encoding/base64"
	"encoding/json"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

const (
	crName = "acme"
	// container names the one container of a rewritten pod.
	container = "app"
	// mirrorPath is the destination.path of the mirrors.
	mirrorPath = "registry.tld/mirror/"

	ghcrGroup = "ghcr.io/acme"
	quayGroup = "quay.io/acme"
	quayHost  = "quay.io"

	teamLabel = "team"

	quaySecret = "quay"
	ghcrSecret = "ghcr"
	hubSecret  = "hub"
)

// secretRef is an auth with a secretRef and the default injectPullSecret.
func secretRef(name string) *kuikv1alpha1.Auth {
	return &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}
}

func repository(path string, auth *kuikv1alpha1.Auth) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{Repository: path, Auth: auth}
}

func group(path string, auth *kuikv1alpha1.Auth) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{RepositoryGroup: path, Auth: auth}
}

func alternative(policy kuikv1alpha1.RewritePolicy, entries ...kuikv1alpha1.Alternative) *kuikv1alpha1.ImageAlternative {
	return &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: crName, UID: types.UID("alternative-uid")},
		Spec:       kuikv1alpha1.ImageAlternativeSpec{RewritePolicy: policy, Alternatives: entries},
	}
}

func mirror(policy kuikv1alpha1.RewritePolicy, pull *kuikv1alpha1.Auth) *kuikv1alpha1.ImageMirror {
	cr := &kuikv1alpha1.ImageMirror{
		ObjectMeta: metav1.ObjectMeta{Name: crName, UID: types.UID("mirror-uid")},
		Spec: kuikv1alpha1.ImageMirrorSpec{
			RewritePolicy: policy,
			Destination:   kuikv1alpha1.MirrorDestination{Path: mirrorPath},
		},
	}
	if pull != nil {
		cr.Spec.Destination.Pull = &kuikv1alpha1.DestinationCredentials{Auth: *pull}
	}
	return cr
}

func namespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

// rewrittenPod is a running pod in team-a whose container app runs image, recorded as
// rewritten to rewrittenTo by by.
func rewrittenPod(by, rewrittenTo, image string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: container},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: container, Image: image}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	annotate(pod, podrecord.AnnotationRewrites, by, rewrittenTo)
	return pod
}

func annotate(pod *corev1.Pod, annotation, by, rewrittenTo string) {
	value, err := json.Marshal(map[string]podrecord.Rewrite{
		container: {By: by, Origin: "quay.io/acme/foo:1", RewrittenTo: rewrittenTo, Policy: "OnFailure"},
	})
	Expect(err).NotTo(HaveOccurred())
	pod.Annotations = map[string]string{annotation: string(value)}
}

// dockerConfig is a docker-registry Secret holding one user per auths key.
func dockerConfig(name string, users map[string]string) *corev1.Secret {
	auths := map[string]map[string]string{}
	for key, user := range users {
		auths[key] = map[string]string{
			"auth": base64.StdEncoding.EncodeToString([]byte(user + ":secret")),
		}
	}
	data, err := json.Marshal(map[string]any{"auths": auths})
	Expect(err).NotTo(HaveOccurred())
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kuik-system", Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: data},
	}
}

func sourcesOf(secrets ...*corev1.Secret) Sources {
	return func(name string) (*corev1.Secret, bool) {
		for _, s := range secrets {
			if s.Name == name {
				return s, true
			}
		}
		return nil, false
	}
}

// auths decodes the auths of a built Secret into the username each key holds.
func auths(secret *corev1ac.SecretApplyConfiguration) map[string]string {
	var config struct {
		Auths map[string]struct {
			Username string `json:"username"`
		} `json:"auths"`
	}
	Expect(json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config)).To(Succeed())
	users := map[string]string{}
	for key, entry := range config.Auths {
		users[key] = entry.Username
	}
	return users
}

var injectFalse = new(false)
