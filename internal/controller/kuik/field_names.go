package kuik

const (
	// Finalizer names
	cleanupFinalizer        = "kuik.enix.io/secret-cleanup"
	imageSetMirrorFinalizer = "kuik.enix.io/mirror-cleanup"

	// Label names
	OwnerVersionLabel = "kuik.enix.io/owner-version"
	OwnerGroupLabel   = "kuik.enix.io/owner-group"
	OwnerKindLabel    = "kuik.enix.io/owner-kind"
	OwnerUIDLabel     = "kuik.enix.io/owner-uid"
	OwnerNameLabel    = "kuik.enix.io/owner-name"

	// Annotation names
	OriginalImagesAnnotation = "kuik.enix.io/original-images"
)
