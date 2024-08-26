package registry

/*
import (
	"fmt"
	"os"
	"testing"

	cranepkg "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	v1 "k8s.io/api/core/v1"
)

// Write remote image to tarball file
func Test_parsing(t *testing.T) {

	sourceImangeName := "mcr.microsoft.com/powershell:lts"
	desc, _ := GetDescriptor(sourceImangeName, []v1.Secret{}, []string{}, nil)
	destRef, _ := parseLocalReference(sourceImangeName)
	// architectures := []string{"amd64"}
	switch desc.MediaType {
	case types.OCIImageIndex, types.DockerManifestList:
		// index, err := desc.ImageIndex()
		// if err != nil {
		// 	return
		// }
		// filteredIndex := mutate.RemoveManifests(index, func(desc cranepkg.Descriptor) bool {
		// 	for _, arch := range architectures {
		// 		if arch == desc.Platform.Architecture {
		// 			return false
		// 		}
		// 	}
		// 	return true
		// })

		// if err := remote.WriteIndex(destRef, filteredIndex); err != nil {
		// 	return
		// }
		var k = 1
		k = k + 1

		progressUpdate := make(chan cranepkg.Update, 100)
		callback := func(u cranepkg.Update) {
			fmt.Print(u.Complete)
		}
		go func() {
			for update := range progressUpdate {
				if callback != nil {
					callback(update)
				}
			}
		}()
		image, err := desc.Image()
		if err != nil {
			return
		}
		file, err := os.Create("output.tar")
		if err := tarball.Write(destRef, image, file, tarball.WithProgress(progressUpdate)); err != nil {
			return
		}

		if err := remote.Write(destRef, image, remote.WithProgress(progressUpdate)); err != nil {
			return
		}
	}
}
*/
