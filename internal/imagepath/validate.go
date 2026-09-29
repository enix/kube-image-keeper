package imagepath

import kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"

// ValidateEntry checks one entry carrying a `repository` or a `repositoryGroup`: exactly one
// of the two, no tag, no digest, a host and at least one segment for a repository.
func ValidateEntry(repository, repositoryGroup string) (Path, Kind, error) {
	return Path{}, Repository, errNotImplemented
}

// ValidateAlternatives checks an `alternatives` list: every entry valid, and every entry of
// the same form. It duplicates the CRD's rules for the validating webhook.
func ValidateAlternatives(alternatives []kuikv1alpha1.Alternative) error {
	return errNotImplemented
}
