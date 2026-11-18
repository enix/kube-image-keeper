package inventory

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Group is the API group of every kuik CRD, v1, v2 and v3 alike.
const Group = "kuik.enix.io"

// v2Plurals are the CRDs only v2 ever shipped, true for those of the 2.0 prereleases that
// 2.3 no longer ships (git show 2.3.x:helm/kube-image-keeper/crds, and the commits that
// removed images and registrymonitors).
var v2Plurals = map[string]bool{
	"clusterimagesetmirrors":        false,
	"imagesetmirrors":               false,
	"clusterreplicatedimagesets":    false,
	"replicatedimagesets":           false,
	"clusterimagesetavailabilities": false,
	"images":                        true,
	"registrymonitors":              true,
}

// sharedPlurals are the CRDs a 2.0 prerelease and v3 both name. The prerelease ones carry a
// spec.registry field (ImageMirror {registry, path, targetRegistry}, ImageMonitor {registry,
// path}), which no v3 kind has.
var sharedPlurals = map[string]bool{"imagemirrors": true, "imagemonitors": true}

// v3Plurals are the CRDs of v3 alone.
var v3Plurals = map[string]bool{"imagealternatives": true}

// crd is a kuik CRD found on the cluster, v2's or v3's.
type crd struct {
	CRD
	gvk schema.GroupVersionKind
	v2  bool
	v3  bool
}

// readCRDs returns the kuik CRDs of v2 and v3, read as unstructured objects: the migration
// does not need the apiextensions types for the few fields it reads.
func readCRDs(ctx context.Context, r client.Reader) ([]crd, error) {
	list := &unstructured.UnstructuredList{}
	list.SetAPIVersion("apiextensions.k8s.io/v1")
	list.SetKind("CustomResourceDefinitionList")
	if err := r.List(ctx, list); err != nil {
		return nil, fmt.Errorf("listing CustomResourceDefinitions: %w", err)
	}
	var crds []crd
	for _, item := range list.Items {
		group, _, _ := unstructured.NestedString(item.Object, "spec", "group")
		if group != Group {
			continue
		}
		plural, _, _ := unstructured.NestedString(item.Object, "spec", "names", "plural")
		kind, _, _ := unstructured.NestedString(item.Object, "spec", "names", "kind")
		scope, _, _ := unstructured.NestedString(item.Object, "spec", "scope")
		version, specFields := storageVersion(&item)

		c := crd{
			CRD: CRD{Name: item.GetName(), Kind: kind, Namespaced: scope == "Namespaced"},
			gvk: schema.GroupVersionKind{Group: Group, Version: version, Kind: kind},
		}
		prerelease, isV2 := v2Plurals[plural]
		switch {
		case isV2:
			c.v2, c.Prerelease = true, prerelease
		case sharedPlurals[plural]:
			_, hasRegistry := specFields["registry"]
			c.v2, c.Prerelease = hasRegistry, hasRegistry
			c.v3 = !hasRegistry
		case v3Plurals[plural]:
			c.v3 = true
		default:
			continue
		}
		crds = append(crds, c)
	}
	return crds, nil
}

// storageVersion returns the version a CRD stores its objects in, and the properties of the
// spec in its schema.
func storageVersion(item *unstructured.Unstructured) (string, map[string]any) {
	versions, _, _ := unstructured.NestedSlice(item.Object, "spec", "versions")
	var chosen map[string]any
	for _, v := range versions {
		version, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if chosen == nil {
			chosen = version
		}
		if storage, _, _ := unstructured.NestedBool(version, "storage"); storage {
			chosen = version
			break
		}
	}
	if chosen == nil {
		return "", nil
	}
	name, _, _ := unstructured.NestedString(chosen, "name")
	fields, _, _ := unstructured.NestedMap(chosen, "schema", "openAPIV3Schema", "properties", "spec", "properties")
	return name, fields
}

// listObjects lists every object of the kind c defines, in every namespace.
func listObjects(ctx context.Context, r client.Reader, c crd) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(c.gvk.GroupVersion().WithKind(c.gvk.Kind + "List"))
	if err := r.List(ctx, list); err != nil {
		return nil, fmt.Errorf("listing %s: %w", c.Name, err)
	}
	return list.Items, nil
}
