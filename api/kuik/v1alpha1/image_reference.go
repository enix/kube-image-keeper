package v1alpha1

type ImageReference struct {
	// Registry is the registry where the image is located
	Registry string `json:"registry"`
	// Path is a string identifying the image in a registry
	Path string `json:"path"`
}

func (i *ImageReference) Reference() string {
	return i.Registry + "/" + i.Path
}
