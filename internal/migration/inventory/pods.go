package inventory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

// OriginalImagesAnnotation is the annotation v2 sets on every pod it admits: the image of each
// container, as the pod spec wrote it, keyed by container name. Init containers are rewritten
// but never recorded (2.3.x: internal/webhook/core/v1/pod_webhook.go).
const OriginalImagesAnnotation = "kuik.enix.io/original-images"

const (
	kindClusterImageSetMirror = "ClusterImageSetMirror"
	kindImageSetMirror        = "ImageSetMirror"
)

// mirrorPaths are the paths v2 mirrors images under, `<registry>/<path>`: a
// ClusterImageSetMirror's apply in every namespace, an ImageSetMirror's in its own only.
type mirrorPaths struct {
	cluster     []string
	byNamespace map[string][]string
}

func newMirrorPaths(resources []Resource) mirrorPaths {
	m := mirrorPaths{byNamespace: map[string][]string{}}
	for _, res := range resources {
		if res.Kind != kindClusterImageSetMirror && res.Kind != kindImageSetMirror {
			continue
		}
		mirrors, _ := res.Spec["mirrors"].([]any)
		for _, mirror := range mirrors {
			fields, _ := mirror.(map[string]any)
			registry, _ := fields["registry"].(string)
			mirrorPath, _ := fields["path"].(string)
			if registry == "" {
				continue
			}
			// v2 writes path.Join(registry, path, <reference without its host>).
			prefix := strings.ToLower(path.Join(registry, mirrorPath)) + "/"
			if res.Kind == kindClusterImageSetMirror {
				m.cluster = append(m.cluster, prefix)
			} else {
				m.byNamespace[res.Namespace] = append(m.byNamespace[res.Namespace], prefix)
			}
		}
	}
	return m
}

// contains reports whether the canonical reference ref sits under a mirror path that applies
// in namespace.
func (m mirrorPaths) contains(namespace, ref string) bool {
	under := func(prefix string) bool { return strings.HasPrefix(ref, prefix) }
	return slices.ContainsFunc(m.cluster, under) || slices.ContainsFunc(m.byNamespace[namespace], under)
}

// canonical returns the canonical form of image, false when it does not parse.
func canonical(image string) (imagepath.Reference, bool) {
	ref, err := imagepath.Parse(image)
	return ref, err == nil
}

// readPods finds the pods that run a container v2 rewrote or reference a v2 pull secret.
func readPods(ctx context.Context, r client.Reader, inv *Inventory) ([]Pod, error) {
	var list corev1.PodList
	if err := r.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("listing Pods: %w", err)
	}
	paths := newMirrorPaths(inv.V2Resources)
	secrets := map[types.NamespacedName]bool{}
	for _, s := range inv.PullSecrets {
		secrets[types.NamespacedName{Namespace: s.Namespace, Name: s.Name}] = true
	}
	owners := &owners{r: r, cache: map[ownerKey]ownerInfo{}}

	var pods []Pod
	for i := range list.Items {
		p, err := readPod(ctx, &list.Items[i], paths, secrets, inv.V3.ClusterID, owners)
		if err != nil {
			return nil, err
		}
		if p != nil {
			pods = append(pods, *p)
		}
	}
	slices.SortFunc(pods, func(a, b Pod) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return pods, nil
}

// readPod returns what v2 left in pod, nil when it left nothing.
func readPod(ctx context.Context, pod *corev1.Pod, paths mirrorPaths, secrets map[types.NamespacedName]bool,
	clusterID string, owners *owners) (*Pod, error) {
	var pullSecrets []string
	for _, ref := range pod.Spec.ImagePullSecrets {
		if secrets[types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}] {
			pullSecrets = append(pullSecrets, ref.Name)
		}
	}
	v3 := v3Output(pod, clusterID)
	type rewritten struct {
		container corev1.Container
		init      bool
	}
	var found []rewritten
	for _, list := range []struct {
		containers []corev1.Container
		init       bool
	}{{pod.Spec.InitContainers, true}, {pod.Spec.Containers, false}} {
		for _, c := range list.containers {
			ref, ok := canonical(c.Image)
			// v2 never rewrites an image pinned by digest.
			if !ok || ref.Digest != "" || v3(c) || !paths.contains(pod.Namespace, ref.String()) {
				continue
			}
			found = append(found, rewritten{container: c, init: list.init})
		}
	}
	if len(found) == 0 && len(pullSecrets) == 0 {
		return nil, nil
	}

	info, err := owners.resolve(ctx, pod)
	if err != nil {
		return nil, err
	}
	recorded := recordedOrigins(pod)
	p := &Pod{Namespace: pod.Namespace, Name: pod.Name, Owner: info.owner, PullSecrets: pullSecrets}
	for _, f := range found {
		p.Containers = append(p.Containers, origin(f.container, f.init, recorded, info.template))
	}
	return p, nil
}

// v3Output returns whether a container of pod runs what v3 placed: a rewrite v3 recorded that
// still stands, or a tag carrying the cluster ID, which every tag an ImageMirror writes ends
// with.
func v3Output(pod *corev1.Pod, clusterID string) func(corev1.Container) bool {
	records, _ := podrecord.Read(pod)
	return func(c corev1.Container) bool {
		for _, rewrites := range []map[string]podrecord.Rewrite{records.Rewrites, records.Conceded} {
			if r, ok := rewrites[c.Name]; ok && r.Stands(c.Image) {
				return true
			}
		}
		ref, ok := canonical(c.Image)
		return ok && clusterID != "" && strings.HasSuffix(ref.Tag, "_"+clusterID)
	}
}

// recordedOrigins decodes the annotation v2 sets on a pod; a malformed one reads as empty.
func recordedOrigins(pod *corev1.Pod) map[string]string {
	images := map[string]string{}
	if value, ok := pod.Annotations[OriginalImagesAnnotation]; ok {
		_ = json.Unmarshal([]byte(value), &images)
	}
	return images
}

// origin returns the container c with its origin: from the annotation v2 set, which records
// regular containers only, else from the owner's template. A template that disagrees with the
// annotation is reported beside it.
func origin(c corev1.Container, init bool, recorded map[string]string, template *corev1.PodTemplateSpec) Container {
	out := Container{Name: c.Name, Init: init, Image: c.Image}
	fromTemplate := templateImage(template, c.Name, init)
	if image, ok := recorded[c.Name]; ok && !init {
		if ref, ok := canonical(image); ok {
			out.Origin, out.OriginFrom = ref.String(), FromAnnotation
			if fromTemplate != "" && fromTemplate != out.Origin {
				out.TemplateOrigin = fromTemplate
			}
			return out
		}
	}
	if fromTemplate != "" {
		out.Origin, out.OriginFrom = fromTemplate, FromTemplate
	}
	return out
}

// templateImage returns the canonical image of the container named name in template, empty
// when there is none.
func templateImage(template *corev1.PodTemplateSpec, name string, init bool) string {
	if template == nil {
		return ""
	}
	containers := template.Spec.Containers
	if init {
		containers = template.Spec.InitContainers
	}
	for _, c := range containers {
		if c.Name != name {
			continue
		}
		if ref, ok := canonical(c.Image); ok {
			return ref.String()
		}
	}
	return ""
}

// workloads are the owners whose pod template the migration reads.
var workloads = map[metav1.TypeMeta]func() client.Object{
	{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet"}:  func() client.Object { return &appsv1.ReplicaSet{} },
	{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "StatefulSet"}: func() client.Object { return &appsv1.StatefulSet{} },
	{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "DaemonSet"}:   func() client.Object { return &appsv1.DaemonSet{} },
	{APIVersion: batchv1.SchemeGroupVersion.String(), Kind: "Job"}:        func() client.Object { return &batchv1.Job{} },
}

type ownerKey struct {
	kind, namespace, name string
}

// ownerInfo is the workload a pod belongs to, and the template the pod came from.
type ownerInfo struct {
	owner    *Owner
	template *corev1.PodTemplateSpec
}

// owners resolves the owners of pods, reading each workload once.
type owners struct {
	r     client.Reader
	cache map[ownerKey]ownerInfo
}

// resolve returns the owner of pod and the template it came from. A ReplicaSet resolves to its
// Deployment, but its own template is the one the pod came from: the Deployment may have
// rolled on since.
func (o *owners) resolve(ctx context.Context, pod *corev1.Pod) (ownerInfo, error) {
	ref := metav1.GetControllerOf(pod)
	if ref == nil {
		return ownerInfo{}, nil
	}
	key := ownerKey{kind: ref.Kind, namespace: pod.Namespace, name: ref.Name}
	if info, ok := o.cache[key]; ok {
		return info, nil
	}
	info, err := o.read(ctx, ref, pod.Namespace)
	if err != nil {
		return ownerInfo{}, err
	}
	o.cache[key] = info
	return info, nil
}

func (o *owners) read(ctx context.Context, ref *metav1.OwnerReference, namespace string) (ownerInfo, error) {
	info := ownerInfo{owner: &Owner{Kind: ref.Kind, Name: ref.Name}}
	newWorkload, known := workloads[metav1.TypeMeta{APIVersion: ref.APIVersion, Kind: ref.Kind}]
	if !known {
		// A custom owner: its template, if it has one, is not a known field.
		return info, nil
	}
	obj := newWorkload()
	key := types.NamespacedName{Namespace: namespace, Name: ref.Name}
	if err := o.r.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return info, nil
		}
		return ownerInfo{}, fmt.Errorf("reading %s %s: %w", ref.Kind, key, err)
	}
	switch w := obj.(type) {
	case *appsv1.ReplicaSet:
		info.template = &w.Spec.Template
		if d := metav1.GetControllerOf(w); d != nil && d.Kind == "Deployment" {
			info.owner = &Owner{Kind: d.Kind, Name: d.Name}
		}
	case *appsv1.StatefulSet:
		info.template = &w.Spec.Template
	case *appsv1.DaemonSet:
		info.template = &w.Spec.Template
	case *batchv1.Job:
		info.template = &w.Spec.Template
	}
	return info, nil
}
