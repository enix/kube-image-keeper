package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

func applyCommand() *command {
	var file string
	f := &privateFlags{}
	return &command{
		usage: "create, or update when the acceptance test created them, the objects of a YAML file, a fixture " +
			"or not, its ${VARIABLES} filled from the flags; every one passes the guard",
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&file, "file", "", "YAML file, one or more documents (required)")
			f.register(fs)
		},
		run: func(ctx context.Context, e *env, _ []string) error {
			if file == "" {
				return errors.New("--file is required")
			}
			if err := e.checkKuikNamespace(ctx); err != nil {
				return err
			}
			if err := e.checkPullAuth(ctx, f.pullAuth); err != nil {
				return err
			}
			e.pullAuth = f.pullAuth
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			expanded, err := expand(file, raw, e.vars(f))
			if err != nil {
				return err
			}
			return e.applyAll(ctx, file, expanded, false)
		},
	}
}

// applyAll decodes the documents of raw, checks every object against the guard, then applies
// them in order: nothing is written when one of them is refused. With createOnly, an object
// that already exists is refused too.
func (e *env) applyAll(ctx context.Context, file string, raw []byte, createOnly bool) error {
	objs, err := e.decode(file, raw)
	if err != nil {
		return err
	}
	for _, obj := range objs {
		if err := e.guard(obj); err != nil {
			return err
		}
		if err := e.checkWrite(obj); err != nil {
			return err
		}
	}
	for _, obj := range objs {
		if err := e.apply(ctx, obj, createOnly); err != nil {
			return err
		}
	}
	return nil
}

// decode reads every document of raw into the typed object of its kind, strictly: an
// unknown field is an error, as kubectl --validate=strict makes it. A document holding only
// comments is skipped.
func (e *env) decode(file string, raw []byte) ([]client.Object, error) {
	decoder := serializer.NewCodecFactory(e.scheme, serializer.EnableStrict).UniversalDeserializer()
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(raw)))
	var objs []client.Object
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		if onlyComments(doc) {
			continue
		}
		decoded, _, err := decoder.Decode(doc, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", file, err)
		}
		obj, ok := decoded.(client.Object)
		if !ok {
			return nil, fmt.Errorf("decode %s: %T is not an object", file, decoded)
		}
		objs = append(objs, obj)
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("no object in %s", file)
	}
	return objs, nil
}

func onlyComments(doc []byte) bool {
	for line := range strings.Lines(string(doc)) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			return false
		}
	}
	return true
}

func deleteCommand() *command {
	var namespace string
	return &command{
		usage: "delete one object the acceptance test created: KIND/NAME, KIND among " + strings.Join(deletableKinds, ", "),
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&namespace, "namespace", testNamespace, "namespace of a Pod, Secret or ConfigMap")
		},
		args: 1,
		run: func(ctx context.Context, e *env, args []string) error {
			kind, name, ok := strings.Cut(args[0], "/")
			if !ok || name == "" {
				return fmt.Errorf("%q: expected KIND/NAME", args[0])
			}
			obj := newDeletable(strings.ToLower(kind))
			if obj == nil {
				return fmt.Errorf("kind %q: expected one of %s", kind, strings.Join(deletableKinds, ", "))
			}
			obj.SetName(name)
			switch obj.(type) {
			case *corev1.Pod, *corev1.Secret, *corev1.ConfigMap:
				obj.SetNamespace(namespace)
			}
			if err := e.checkKuikNamespace(ctx); err != nil {
				return err
			}
			return e.remove(ctx, obj)
		},
	}
}

var deletableKinds = []string{"pod", "secret", "configmap", "imagealternative", "imagemirror"}

// newDeletable is the empty object of a kind delete takes. Namespaces are not among them: a
// test namespace goes with cleanup, once the test is over.
func newDeletable(kind string) client.Object {
	switch kind {
	case "pod":
		return &corev1.Pod{}
	case "secret":
		return &corev1.Secret{}
	case "configmap":
		return &corev1.ConfigMap{}
	case "imagealternative":
		return &kuikv1alpha1.ImageAlternative{}
	case "imagemirror":
		return &kuikv1alpha1.ImageMirror{}
	}
	return nil
}
