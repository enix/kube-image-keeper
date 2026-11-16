// Package synth builds the synthetic images served by kuik-tester.
package synth

import (
	"archive/tar"
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// Config labels carrying what makes each synthetic image unique.
const (
	labelContent    = "kuik-tester.content"
	labelGeneration = "kuik-tester.generation"
)

// Platforms are the platforms of every synthetic index, in index order:
// linux/<arch> for each embedded sleep binary, sorted by arch.
var Platforms = func() []v1.Platform {
	ps := make([]v1.Platform, 0, len(sleepBinaries))
	for _, arch := range slices.Sorted(maps.Keys(sleepBinaries)) {
		ps = append(ps, v1.Platform{OS: "linux", Architecture: arch})
	}
	return ps
}()

// PlatformName is the "os/arch" form of a platform, as the API spells it.
func PlatformName(p v1.Platform) string { return p.OS + "/" + p.Architecture }

// Select returns the platforms of Platforms whose name is in names, in
// Platforms order; unknown names are ignored. Nil or empty names give nil,
// which Build reads as every platform.
func Select(names []string) []v1.Platform {
	if len(names) == 0 {
		return nil
	}
	var out []v1.Platform
	for _, p := range Platforms {
		for _, n := range names {
			if n == PlatformName(p) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// layers holds the shared layer of each arch, built on first use; there is
// one per sleep binary, so it stays bounded.
var layers = sync.OnceValues(func() (map[string]v1.Layer, error) {
	m := map[string]v1.Layer{}
	for _, p := range Platforms {
		bin, err := SleepBinary(p.Architecture)
		if err != nil {
			return nil, err
		}
		tb, err := sleepTar(bin)
		if err != nil {
			return nil, err
		}
		m[p.Architecture] = static.NewLayer(tb, types.OCIUncompressedLayer)
	}
	return m, nil
})

// Layer returns the layer shared by every synthetic image of arch. It is an
// uncompressed tar, so its digest equals its diffID.
func Layer(arch string) (v1.Layer, error) {
	m, err := layers()
	if err != nil {
		return nil, err
	}
	l, ok := m[arch]
	if !ok {
		return nil, fmt.Errorf("unsupported architecture %q", arch)
	}
	return l, nil
}

func sleepTar(bin []byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "sleep",
		Mode:     0o755,
		Size:     int64(len(bin)),
		ModTime:  time.Unix(0, 0),
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(bin); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Build returns the index for a content key, a generation and a platform
// list (nil means Platforms). It keeps no state: the same inputs always give
// the same digests.
func Build(contentKey string, generation int, platforms []v1.Platform) (v1.ImageIndex, error) {
	if len(platforms) == 0 {
		platforms = Platforms
	}
	idx := mutate.IndexMediaType(empty.Index, types.OCIImageIndex)
	for _, p := range platforms {
		img, err := platformImage(p, contentKey, generation)
		if err != nil {
			return nil, err
		}
		idx = mutate.AppendManifests(idx, mutate.IndexAddendum{
			Add:        img,
			Descriptor: v1.Descriptor{MediaType: types.OCIManifestSchema1, Platform: &p},
		})
	}
	// Compute once so later calls only read memoized values.
	if _, err := idx.Digest(); err != nil {
		return nil, err
	}
	return idx, nil
}

func platformImage(p v1.Platform, contentKey string, generation int) (v1.Image, error) {
	img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, types.OCIConfigJSON)
	img, err := mutate.ConfigFile(img, &v1.ConfigFile{
		OS:           p.OS,
		Architecture: p.Architecture,
		RootFS:       v1.RootFS{Type: "layers"},
		Config: v1.Config{
			Entrypoint: []string{"/sleep"},
			Labels: map[string]string{
				labelContent:    contentKey,
				labelGeneration: strconv.Itoa(generation),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	l, err := Layer(p.Architecture)
	if err != nil {
		return nil, err
	}
	return mutate.AppendLayers(img, l)
}
