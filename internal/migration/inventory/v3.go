package inventory

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/mirrorpath/clusterid"
)

// The modes of the kuik binary, as its subcommands name them (cmd/process.go).
const (
	modeWebhook      = "webhook"
	modeReconciler   = "reconciler"
	modeSecretSyncer = "secret-syncer"
	binary           = "manager"
)

// readV3 reads the cluster ID, the modes that run, the webhook configuration and the
// resources of v3 into v.
func readV3(ctx context.Context, r client.Reader, v *V3Installation, crds []crd,
	deployments []appsv1.Deployment, webhooks []admissionregistrationv1.MutatingWebhookConfiguration) error {
	if err := readClusterID(ctx, r, v); err != nil {
		return err
	}
	readModes(v, deployments)
	for i := range webhooks {
		w := &webhooks[i]
		if namespace, ok := podWebhookNamespace(w); ok && isV3Webhook(w) && namespace == v.Namespace {
			v.WebhookConfiguration = w.Name
			break
		}
	}
	resources, err := readV3Resources(ctx, r, crds)
	if err != nil {
		return err
	}
	v.Resources = resources
	return nil
}

// readClusterID reads the ConfigMap the v3 reconciler keeps the cluster ID in, and validates
// it as v3 does.
func readClusterID(ctx context.Context, r client.Reader, v *V3Installation) error {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: v.Namespace, Name: clusterid.ConfigMapName}
	if err := r.Get(ctx, key, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("reading ConfigMap %s: %w", key, err)
	}
	id, found := cm.Data[clusterid.Key]
	if !found {
		v.ClusterIDError = fmt.Sprintf("ConfigMap %s has no key %s", key, clusterid.Key)
		return nil
	}
	if err := clusterid.Validate(id); err != nil {
		v.ClusterIDError = err.Error()
		return nil
	}
	v.ClusterID = id
	return nil
}

// readModes finds the Deployments of v's namespace that run each mode: a container that runs
// the kuik binary with the mode as an argument. One Deployment may run several.
func readModes(v *V3Installation, deployments []appsv1.Deployment) {
	modes := map[string]*Mode{
		modeWebhook:      &v.Webhook,
		modeReconciler:   &v.Reconciler,
		modeSecretSyncer: &v.SecretSyncer,
	}
	for _, d := range deployments {
		if d.Namespace != v.Namespace {
			continue
		}
		for _, c := range d.Spec.Template.Spec.Containers {
			words := append(slices.Clone(c.Command), c.Args...)
			if len(words) == 0 || (path.Base(words[0]) != binary && repository(c.Image) != imageRepository) {
				continue
			}
			for _, word := range words {
				if mode, ok := modes[word]; ok && mode.Deployment == "" {
					*mode = Mode{Deployment: d.Name, Ready: d.Status.ReadyReplicas > 0}
				}
			}
		}
	}
}

// readV3Resources reads the Ready condition of every v3 resource, and whether an ImageMirror's
// destination misses images (docs/v3/status.md, "Conditions and their reasons").
func readV3Resources(ctx context.Context, r client.Reader, crds []crd) ([]V3Resource, error) {
	var resources []V3Resource
	for _, c := range crds {
		if !c.v3 {
			continue
		}
		items, err := listObjects(ctx, r, c)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			res := V3Resource{Kind: c.Kind, Name: item.GetName()}
			for _, cond := range conditions(&item) {
				switch {
				case cond.Type == kuikv1alpha1.ConditionReady:
					res.Ready, res.Reason, res.Message = cond.Status, cond.Reason, cond.Message
				case cond.Type == kuikv1alpha1.ConditionDestinationOutOfSync &&
					cond.Status == metav1.ConditionTrue && cond.Reason == kuikv1alpha1.ReasonMissingImages:
					res.MissingImages = true
				}
			}
			resources = append(resources, res)
		}
	}
	slices.SortFunc(resources, func(a, b V3Resource) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	return resources, nil
}

// conditions returns the status conditions of item; a malformed one is skipped.
func conditions(item *unstructured.Unstructured) []metav1.Condition {
	raw, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	var out []metav1.Condition
	for _, c := range raw {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		var cond metav1.Condition
		if runtime.DefaultUnstructuredConverter.FromUnstructured(m, &cond) == nil {
			out = append(out, cond)
		}
	}
	return out
}
