// Package keychain turns docker config Secrets into registry credentials, with the
// kubelet's own resolution.
package keychain

import (
	"errors"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
)

// MalformedSecretError reports a Secret whose content is not a docker config.
type MalformedSecretError struct {
	Namespace string
	Name      string
	Err       error
}

func (e *MalformedSecretError) Error() string {
	return "secret " + e.Namespace + "/" + e.Name + " is not a docker config: " + e.Err.Error()
}

func (e *MalformedSecretError) Unwrap() error { return e.Err }

// Authenticators returns one authenticator per credential of secrets that covers image,
// the most specific path first. A malformed Secret is a *MalformedSecretError.
func Authenticators(image string, secrets []corev1.Secret) ([]authn.Authenticator, error) {
	return nil, errors.New("not implemented")
}
