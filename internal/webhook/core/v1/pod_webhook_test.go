package v1

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Pod webhook", func() {
	Context("gates", func() {
		PIt("leaves a static pod untouched: no rewrite, no annotation, no pull secret", func() {})
		PIt("leaves a container with imagePullPolicy Never untouched and routes the others", func() {})
	})

	Context("containers", func() {
		PIt("routes and records initContainers like containers", func() {})
		PIt("leaves ephemeralContainers untouched", func() {})
		PIt("routes a digest-pinned image, carrying the digest to the candidate", func() {})
	})

	Context("probing", func() {
		PIt("leaves the container and the annotations untouched when the original answers first", func() {})
		PIt("rewrites the container to the first candidate that answers", func() {})
		PIt("probes a candidate only once every candidate before it has failed", func() {})
		PIt("sends no request past the first candidate that answers", func() {})
		PIt("gives up on a candidate that does not answer within availabilityCheck.timeout", func() {})
		PIt("sends no request when no resource offers a candidate", func() {})
		PIt("resolves an image several containers share once", func() {})
		PIt("resolves apart two containers of one image whose pull policies give different candidate lists", func() {})
		PIt("probes the distinct images of a pod concurrently", func() {})
	})

	Context("credentials", func() {
		PIt("probes with the pod's imagePullSecrets, read in the namespace of the admission request", func() {})
		PIt("never probes with a fallbackAuth credential", func() {})
		// Interim until providers are implemented (notes/0010): the spec'd webhook reads the
		// Secret the syncer materialises for the provider.
		PIt("probes a candidate whose auth is a provider with the credentials that follow it", func() {})
	})

	Context("failing open", func() {
		PIt("probes a candidate whose declared Secret is missing with the credentials that follow, admitting the pod", func() {})
		PIt("leaves a container whose image does not parse untouched and routes the others", func() {})
	})

	Context("activeCheckCache", func() {
		PIt("answers a second admission of the same image within the TTL without a request", func() {})
		PIt("probes again once the TTL has elapsed", func() {})
		PIt("collapses concurrent admissions of the same image into one request", func() {})
		PIt("does not reuse an answer obtained with other credentials", func() {})
	})

	Context("records on a new container", func() {
		PIt("records a rewrite in kuik.enix.io/rewrites as by, the normalised origin, rewrittenTo and policy", func() {})
		DescribeTable("records the policy of the band the retained candidate comes from",
			func(policy string) {},
			PEntry("Always, from an Always resource ahead of the original", "Always"),
			PEntry("OnFailure, from a candidate behind the original", "OnFailure"),
		)
		PIt("records nothing when the candidates of an Always resource decline and the original answers", func() {})
		PIt("records every resource that offered a candidate in kuik.enix.io/no-alternatives when none answers, leaving the image", func() {})
	})

	Context("reinvocation and replayed spec", func() {
		PIt("leaves an intact container untouched, without probing", func() {})
		PIt("never resolves a container named in no-alternatives a second time", func() {})
		PIt("concedes a container whose image differs from its rewrittenTo, moving its entry unchanged to kuik.enix.io/conceded-rewrites", func() {})
		PIt("concedes even when the new image is another candidate of the same resource", func() {})
		PIt("leaves a conceded container alone on a further round, producing no patch", func() {})
		PIt("routes a container another webhook added and leaves the containers already served alone", func() {})
		PIt("drops the entry of a container no longer in the pod", func() {})
		PIt("names each container in at most one of the three annotations", func() {})
	})

	Context("injected pull secret", func() {
		DescribeTable("appends kuik-inject-<kind>-<name> when the retained candidate's auth injects",
			func(kind string) {},
			PEntry("for an ImageAlternative entry with a secretRef and injectPullSecret unset", "ImageAlternative"),
			PEntry("for an ImageMirror destination.pull", "ImageMirror"),
		)
		DescribeTable("appends nothing when the retained candidate's auth does not inject",
			func(auth string) {},
			PEntry("a secretRef with injectPullSecret false", "secretRef"),
			PEntry("a provider with injectPullSecret unset", "provider"),
		)
		PIt("appends one name per resource, however many containers it serves", func() {})
		PIt("removes the injected name of a conceded resource that serves no other container", func() {})
		PIt("keeps the injected name while another container of the conceded resource still needs it", func() {})
		PIt("restores the injected name a replayed spec lost", func() {})
		PIt("never removes a pull secret the pod declared itself", func() {})
	})

	Context("metrics", func() {
		PIt("counts a rewrite in kuik_routing_rewrites_total by kind, name and policy", func() {})
		PIt("counts a container no candidate served once per offering resource in kuik_routing_alternatives_exhausted_total", func() {})
		PIt("counts nothing again on a reinvocation", func() {})
	})

	Context("through the API server", func() {
		PIt("routes a pod with the resources as last changed", func() {})
		PIt("selects resources on the labels of the pod's namespace", func() {})
		PIt("never rewrites a pod on update", func() {})
		PIt("applies a reloaded webhook config to the next admission", func() {})
		PIt("writes nothing to the API server besides the patch it returns", func() {})
	})
})
