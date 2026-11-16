//go:build e2e

package dest

import (
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
)

var _ = Describe("Client against zot", func() {
	var c *Client

	BeforeEach(func() {
		zot, ok := testutil.ZotURL()
		if !ok {
			Skip("ZOT_URL is not set")
		}
		var err error
		c, err = New(backendURL(zot))
		Expect(err).NotTo(HaveOccurred())
	})

	It("pushes, tags, deletes tags and manifests", func() {
		checkActions(ctx, c, testutil.UniqueRepo("zot-dest-actions"))
	})

	It("purges the tags under a prefix", func() {
		checkPurge(ctx, c, fmt.Sprintf("purge-%d", time.Now().UnixNano()))
	})

	// Every synthetic image of an arch carries the same layer, so the purge deletes
	// blobs other repositories hold too. The in-process registry keeps one blob
	// store for every repository, hence a zot spec only.
	It("keeps the blobs of the repositories it does not purge", func() {
		prefix := fmt.Sprintf("purge-blobs-%d", time.Now().UnixNano())
		purged, kept := prefix+"/a", prefix+"2/x"
		for _, r := range []string{purged, kept} {
			Expect(c.PushSynthetic(ctx, r, "t1", r, 1)).To(Succeed())
		}
		blobs := blobsOf(ctx, c, kept, "t1")
		Expect(blobsOf(ctx, c, purged, "t1")).To(ContainElement(BeElementOf(blobs)), "the two images share no blob")
		_, err := c.Purge(ctx, prefix)
		Expect(err).NotTo(HaveOccurred())
		for _, b := range blobs {
			Expect(blobStatus(ctx, c, kept, b)).To(Equal(http.StatusOK), "blob %s of %s after the purge", b, kept)
		}
		_, err = c.Purge(ctx, prefix+"2")
		Expect(err).NotTo(HaveOccurred())
	})
})
