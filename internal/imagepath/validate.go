package imagepath

import (
	"errors"
	"fmt"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// ValidateEntry checks one entry carrying a `repository` or a `repositoryGroup`: exactly one
// of the two, no tag, no digest, a host and at least one segment for a repository. It is the
// rule `alternatives` and `fallbackAuth` share.
func ValidateEntry(repository, repositoryGroup string) (Path, Kind, error) {
	switch {
	case repository != "" && repositoryGroup != "":
		return Path{}, Repository, errors.New("exactly one of repository or repositoryGroup must be set, not both")
	case repository != "":
		p, err := ParsePath(repository, Repository)
		return p, Repository, err
	case repositoryGroup != "":
		p, err := ParsePath(repositoryGroup, Group)
		return p, Group, err
	default:
		return Path{}, Repository, errors.New("exactly one of repository or repositoryGroup must be set")
	}
}

// ValidateAlternatives checks an `alternatives` list: every entry valid, and every entry of
// the same form, since the two sides of a rewrite must describe the same set of images. It
// duplicates the CRD's rules for the validating webhook.
func ValidateAlternatives(alternatives []kuikv1alpha1.Alternative) error {
	if len(alternatives) == 0 {
		return errors.New("alternatives must hold at least one entry")
	}
	var first Kind
	for i, a := range alternatives {
		_, kind, err := ValidateEntry(a.Repository, a.RepositoryGroup)
		if err != nil {
			return fmt.Errorf("alternatives[%d]: %w", i, err)
		}
		if i == 0 {
			first = kind
		} else if kind != first {
			return fmt.Errorf("alternatives[%d]: alternatives must all be repository entries or all be repositoryGroup entries", i)
		}
	}
	return nil
}
