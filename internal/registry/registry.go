package registry

import (
	"crypto/sha1"
	"fmt"
	"regexp"
	"strings"
)

// See https://github.com/kubernetes/apimachinery/blob/v0.20.6/pkg/util/validation/validation.go#L198
var sanitizeNameRegex = regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)

func SanitizeName(image string) string {
	return strings.Join(sanitizeNameRegex.FindAllString(strings.ToLower(image), -1), "-")
}

func ContainerAnnotationKey(containerName string, initContainer bool) string {
	template := "original-image-%s"
	if initContainer {
		template = "original-init-image-%s"
	}

	if len(containerName)+len(template)-2 > 63 {
		containerName = fmt.Sprintf("%x", sha1.Sum([]byte(containerName)))
	}

	return fmt.Sprintf(template, containerName)
}
