// Package clusterid generates the cluster ID every tag an ImageMirror writes carries, and
// keeps it in a ConfigMap of kuik's install namespace: the reconciler creates it on its first
// start and reads it back on the next ones, the webhook only reads it.
package clusterid

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ConfigMapName is the ConfigMap the cluster ID is kept in.
	ConfigMapName = "kuik-cluster-id"
	// Key is the key of the ConfigMap that holds the cluster ID.
	Key = "clusterID"
)

// pattern is the OCI tag alphabet without `_`, the suffix separator.
var pattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)

// maxLength keeps the anchor tag `sha256-<64 hex>_<clusterID>` within the OCI limit of 128
// characters: an anchor cannot be truncated without losing its digest.
const maxLength = 56

// Validate returns an error when id cannot suffix a tag.
func Validate(id string) error {
	switch {
	case id == "":
		return errors.New("clusterID is empty")
	case !pattern.MatchString(id):
		return fmt.Errorf("clusterID %q must match %s", id, pattern)
	case len(id) > maxLength:
		return fmt.Errorf("clusterID %q must be at most %d characters", id, maxLength)
	}
	return nil
}

// Generate returns a new cluster ID, `<adjective>-<surname>-<4 hex>`: a name of the Docker
// generator, joined by `-` since `_` separates the suffix of a tag, and a random suffix so
// that two clusters drawing the same name still differ.
func Generate() string {
	id, err := generate(rand.Reader)
	if err != nil {
		// crypto/rand.Reader never fails: it crashes the program instead.
		panic(err)
	}
	return id
}

// generate draws a cluster ID from random.
func generate(random io.Reader) (string, error) {
	for {
		adjective, err := pick(random, left[:])
		if err != nil {
			return "", err
		}
		surname, err := pick(random, right[:])
		if err != nil {
			return "", err
		}
		// Steve Wozniak is not boring: Docker draws again, so does kuik.
		if adjective == "boring" && surname == "wozniak" {
			continue
		}
		suffix := make([]byte, 2)
		if _, err := io.ReadFull(random, suffix); err != nil {
			return "", err
		}
		return adjective + "-" + surname + "-" + hex.EncodeToString(suffix), nil
	}
}

// pick returns a word of words drawn uniformly from 2 big-endian bytes of random, drawing
// again on the values past the last whole multiple of len(words).
func pick(random io.Reader, words []string) (string, error) {
	const span = 1 << 16
	limit := uint32(span - span%len(words))
	buf := make([]byte, 2)
	for {
		if _, err := io.ReadFull(random, buf); err != nil {
			return "", err
		}
		if v := uint32(binary.BigEndian.Uint16(buf)); v < limit {
			return words[int(v)%len(words)], nil
		}
	}
}

// The reconciler creates the ConfigMap on its first start and reads it on the next ones; it
// never updates it.
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create,namespace=kuik-system,roleName=reconciler

// Ensure returns the cluster ID kept in the ConfigMap of namespace, creating the ConfigMap
// with a generated ID when it is missing. It never rewrites an existing ConfigMap: changing
// the ID orphans every tag written under the previous one, so a stored value that does not
// validate is an error for an operator to fix. c must read from the API server, not from a
// cache.
func Ensure(ctx context.Context, c client.Client, namespace string) (string, error) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ConfigMapName, Namespace: namespace},
		Data:       map[string]string{Key: Generate()},
	}
	err := c.Create(ctx, cm)
	if err == nil {
		return cm.Data[Key], nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating ConfigMap %s/%s: %w", namespace, ConfigMapName, err)
	}
	return read(ctx, c, namespace)
}

// read returns the cluster ID the ConfigMap of namespace holds, an error when the ConfigMap
// is missing or its value does not validate.
func read(ctx context.Context, r client.Reader, namespace string) (string, error) {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: namespace, Name: ConfigMapName}
	if err := r.Get(ctx, key, cm); err != nil {
		return "", fmt.Errorf("reading ConfigMap %s: %w", key, err)
	}
	id, found := cm.Data[Key]
	if !found {
		return "", &invalidError{fmt.Errorf("ConfigMap %s has no key %s", key, Key)}
	}
	if err := Validate(id); err != nil {
		return "", &invalidError{fmt.Errorf("ConfigMap %s holds an invalid %s: %w", key, Key, err)}
	}
	return id, nil
}

// invalidError is a ConfigMap read whose content is no cluster ID: an operator must fix it,
// kuik never rewrites it.
type invalidError struct{ error }

func (e *invalidError) Unwrap() error { return e.error }
