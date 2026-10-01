package routingstatus

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Routing status", func() {
	Describe("the pods and containers gauges", func() {
		PIt("counts every live pod it is given in pods.tracked", func() {})
		PIt("leaves out a static pod, which the webhook never routes", func() {})
		PIt("counts in pods.rewritten the pods carrying a standing rewrite by this resource", func() {})
		PIt("counts a pod with several rewritten containers once in pods.rewritten and once per container in containers.rewritten", func() {})
		PIt("partitions the containers into the five states, containers.tracked being their sum", func() {})
		PIt("counts a container another resource rewrote, conceded or let go stale as untouched", func() {})
		PIt("leaves out of pods.rewritten a pod whose only rewrite by this resource went stale", func() {})
	})

	Describe("activeFallbacks", func() {
		PIt("lists the OnFailure rewrites of this resource per origin and rewrittenTo, with the pods carrying them", func() {})
		PIt("leaves out an Always rewrite", func() {})
		PIt("leaves out a rewrite that went stale", func() {})
	})

	Describe("noAlternatives", func() {
		PIt("lists per origin the containers no candidate served that this resource offered one for, with their pods", func() {})
	})

	Describe("concededRewrites and staleRewrites", func() {
		PIt("lists the conceded rewrites of this resource per origin, rewrittenTo and replacedBy", func() {})
		PIt("lists the stale rewrites of this resource per origin, rewrittenTo and replacedBy", func() {})
		PIt("reads image from the record's origin, never from the live container", func() {})
		PIt("reads replacedBy from the live container", func() {})
	})

	Describe("since, on every list", func() {
		PIt("stamps a new entry with now", func() {})
		PIt("carries since forward from the previous status for an entry still present", func() {})
		PIt("drops an entry no pod accounts for any more", func() {})
		PIt("counts a pod once in an entry it carries several containers of", func() {})
	})

	Describe("SetConditions", func() {
		PIt("sets FallbackActive True with reason OriginUnavailable while activeFallbacks is not empty", func() {})
		PIt("sets AlternativesExhausted True with reason AllCandidatesFailed while noAlternatives is not empty", func() {})
		PIt("counts the images and pods of the whole list in the message, before any cap", func() {})
		PIt("removes both conditions once their list is empty", func() {})
	})

	Describe("Cap", func() {
		PIt("caps the four lists under their status field names", func() {})
	})

	Describe("pod events", func() {
		PIt("emits a Normal ImageFallback for an OnFailure rewrite, naming the origin, the retained reference and the resource", func() {})
		PIt("emits nothing for an Always rewrite", func() {})
		PIt("emits a Warning NoAlternativeAvailable from the first resource listed, naming every resource that offered one", func() {})
		PIt("emits no NoAlternativeAvailable from a resource listed after the first", func() {})
		PIt("emits a Warning RewriteConceded naming the container, the origin, the reference kuik placed, the resource and the image that won", func() {})
		PIt("emits ImageFallback, NoAlternativeAvailable and RewriteConceded only for pods created after the lease", func() {})
		PIt("emits no pod event before the lease is acquired", func() {})
		PIt("emits each event once per container, not again at the next report", func() {})
		PIt("emits ImageFallback, RewriteConceded and RewriteStale only from the resource the record names", func() {})
		PIt("emits a Warning RewriteStale on each pod of an entry entering staleRewrites, whenever the pod was created", func() {})
		PIt("names the container, the origin, the reference kuik placed, the resource and the image the pod runs now in RewriteStale", func() {})
		PIt("emits RewriteStale for a pod joining an entry already listed", func() {})
		PIt("re-announces no pod of an entry the previous status already listed, at the first report of a lease", func() {})
	})

	Describe("the routing series", func() {
		PIt("exports kuik_routing_containers for each of the five states, zero included", func() {})
		PIt("exports kuik_routing_pods_tracked and kuik_routing_pods_rewritten", func() {})
		PIt("exports kuik_fallback_active_pods per origin image, with the pods as value and the host as registry", func() {})
		PIt("exports kuik_alternatives_exhausted_pods per origin image", func() {})
		PIt("exports kuik_rewrite_conceded_pods per origin image", func() {})
		PIt("exports kuik_rewrite_stale_pods per origin image", func() {})
		PIt("labels the conceded and stale series with the record's origin, never the live image", func() {})
		PIt("counts distinct pods in a series that several entries of one origin feed", func() {})
		PIt("deletes an anomaly series once its anomaly ends", func() {})
		PIt("holds every entry, whatever the status cap", func() {})
		PIt("removes every series of a forgotten resource", func() {})
	})
})
