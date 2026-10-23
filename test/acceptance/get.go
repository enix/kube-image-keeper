package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/jsonpath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// The lower-case names kubectl knows the kinds by, which get and delete take.
const (
	kindPod              = "pod"
	kindConfigMap        = "configmap"
	kindSecret           = "secret"
	kindNamespace        = "namespace"
	kindImageAlternative = "imagealternative"
	kindImageMirror      = "imagemirror"
	kindImageMonitor     = "imagemonitor"
	kindPolicy           = "validatingadmissionpolicy"
	kindPolicyBinding    = "validatingadmissionpolicybinding"
)

// readKinds are the kinds get takes.
var readKinds = map[string]struct {
	gvk        schema.GroupVersionKind
	namespaced bool
}{
	kindPod:              {corev1.SchemeGroupVersion.WithKind("Pod"), true},
	kindConfigMap:        {corev1.SchemeGroupVersion.WithKind("ConfigMap"), true},
	kindSecret:           {corev1.SchemeGroupVersion.WithKind("Secret"), true},
	kindNamespace:        {corev1.SchemeGroupVersion.WithKind("Namespace"), false},
	kindImageAlternative: {kuikv1alpha1.GroupVersion.WithKind("ImageAlternative"), false},
	kindImageMirror:      {kuikv1alpha1.GroupVersion.WithKind("ImageMirror"), false},
	kindImageMonitor:     {kuikv1alpha1.GroupVersion.WithKind("ImageMonitor"), false},
	kindPolicy:           {admissionregistrationv1.SchemeGroupVersion.WithKind("ValidatingAdmissionPolicy"), false},
	kindPolicyBinding:    {admissionregistrationv1.SchemeGroupVersion.WithKind("ValidatingAdmissionPolicyBinding"), false},
}

type getFlags struct {
	namespace, selector, jsonPath string
}

func getCommand() *command {
	f := &getFlags{}
	kinds := make([]string, 0, len(readKinds))
	for kind := range readKinds {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return &command{
		usage: "print one object, or the objects of a kind, as YAML or through --jsonpath, as kubectl get does: " +
			"KIND/NAME or KIND, KIND among " + strings.Join(kinds, ", ") + "; reads only what canRead allows, " +
			"a Secret as a summary without its data; a missing object prints NotFound and exits 0",
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&f.namespace, "namespace", testNamespace, "namespace of a namespaced kind")
			fs.StringVar(&f.namespace, "n", testNamespace, "short for --namespace")
			fs.StringVar(&f.selector, "selector", "", "label selector of a list, kubectl's syntax")
			fs.StringVar(&f.selector, "l", "", "short for --selector")
			fs.StringVar(&f.jsonPath, "jsonpath", "", "kubectl's JSONPath template, such as '{.status.phase}'")
		},
		args: 1,
		run:  func(ctx context.Context, e *env, args []string) error { return e.get(ctx, f, args[0]) },
	}
}

func (e *env) get(ctx context.Context, f *getFlags, arg string) error {
	kind, name, _ := strings.Cut(arg, "/")
	kind = strings.ToLower(kind)
	k, ok := readKinds[kind]
	if !ok {
		return fmt.Errorf("kind %q: get does not read it", kind)
	}
	namespace := ""
	if k.namespaced {
		namespace = f.namespace
	}
	if kind == kindSecret && f.jsonPath != "" {
		return errors.New("--jsonpath is refused on a Secret: get prints its summary only, never its data")
	}
	if name != "" && f.selector != "" {
		return errors.New("--selector goes with a list, KIND without a name")
	}
	if err := e.canRead(kind, namespace, name); err != nil {
		return err
	}
	if kind == kindPod && namespace == e.kuikNamespace {
		return e.getReleasePods(ctx, f, k.gvk, name)
	}
	if name == "" {
		return e.getList(ctx, f, k.gvk, namespace, f.selector)
	}
	created, err := e.scheme.New(k.gvk)
	if err != nil {
		return err
	}
	obj, ok := created.(client.Object)
	if !ok {
		return fmt.Errorf("%s is not an object", k.gvk)
	}
	switch err := e.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, obj); {
	case apierrors.IsNotFound(err):
		where := ""
		if namespace != "" {
			where = " in " + namespace
		}
		fmt.Printf("NotFound: %s %s%s\n", kind, name, where)
		return nil
	case err != nil:
		return fmt.Errorf("read %s %s: %w", kind, name, err)
	}
	if secret, ok := obj.(*corev1.Secret); ok {
		printSecret(secret)
		return nil
	}
	out, err := toMap(obj, k.gvk)
	if err != nil {
		return err
	}
	return printRead(out, f.jsonPath)
}

// getReleasePods reads the pods of the kuik namespace through the labels of the release only, so
// no other pod of that namespace is ever read: one by name, or the list.
func (e *env) getReleasePods(ctx context.Context, f *getFlags, gvk schema.GroupVersionKind, name string) error {
	release := "app.kubernetes.io/name=kube-image-keeper,app.kubernetes.io/instance=" + e.release
	if f.selector != "" {
		release += "," + f.selector
	}
	if name == "" {
		return e.getList(ctx, f, gvk, e.kuikNamespace, release)
	}
	selector, err := labels.Parse(release)
	if err != nil {
		return fmt.Errorf("--selector: %w", err)
	}
	var pods corev1.PodList
	if err := e.client.List(ctx, &pods, client.InNamespace(e.kuikNamespace),
		client.MatchingLabelsSelector{Selector: selector}, client.MatchingFields{"metadata.name": name}); err != nil {
		return fmt.Errorf("read pod %s: %w", name, err)
	}
	if len(pods.Items) == 0 {
		fmt.Printf("NotFound: pod %s in %s, among the pods of release %s\n", name, e.kuikNamespace, e.release)
		return nil
	}
	out, err := toMap(&pods.Items[0], gvk)
	if err != nil {
		return err
	}
	return printRead(out, f.jsonPath)
}

// getList prints the objects of a kind as a List, as kubectl get KIND does, selected by
// selector when it is not empty.
func (e *env) getList(ctx context.Context, f *getFlags, gvk schema.GroupVersionKind, namespace, selector string) error {
	created, err := e.scheme.New(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	if err != nil {
		return err
	}
	list, ok := created.(client.ObjectList)
	if !ok {
		return fmt.Errorf("%s has no list", gvk)
	}
	var options []client.ListOption
	if namespace != "" {
		options = append(options, client.InNamespace(namespace))
	}
	if selector != "" {
		parsed, err := labels.Parse(selector)
		if err != nil {
			return fmt.Errorf("--selector: %w", err)
		}
		options = append(options, client.MatchingLabelsSelector{Selector: parsed})
	}
	if err := e.client.List(ctx, list, options...); err != nil {
		return fmt.Errorf("list %s: %w", gvk.Kind, err)
	}
	objs, err := meta.ExtractList(list)
	if err != nil {
		return err
	}
	items := make([]any, 0, len(objs))
	for _, o := range objs {
		if secret, ok := o.(*corev1.Secret); ok {
			printSecret(secret)
			continue
		}
		item, err := toMap(o, gvk)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	if gvk.Kind == "Secret" {
		if len(objs) == 0 {
			fmt.Println("no Secret")
		}
		return nil
	}
	return printRead(map[string]any{"apiVersion": "v1", "kind": "List", "items": items}, f.jsonPath)
}

// toMap is obj as kubectl prints it, without its managedFields.
func toMap(obj runtime.Object, gvk schema.GroupVersionKind) (map[string]any, error) {
	out, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	out["apiVersion"], out["kind"] = gvk.GroupVersion().String(), gvk.Kind
	if metadata, ok := out["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
	}
	return out, nil
}

// printRead prints data as YAML, or through the JSONPath template as kubectl -o jsonpath does,
// a missing key printing nothing.
func printRead(data map[string]any, template string) error {
	if template == "" {
		out, err := yaml.Marshal(data)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
		return nil
	}
	j := jsonpath.New("get").AllowMissingKeys(true)
	if err := j.Parse(template); err != nil {
		return fmt.Errorf("--jsonpath: %w", err)
	}
	var out bytes.Buffer
	if err := j.Execute(&out, data); err != nil {
		return fmt.Errorf("--jsonpath: %w", err)
	}
	if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		out.WriteString("\n")
	}
	fmt.Print(out.String())
	return nil
}
