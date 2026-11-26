package registry

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/authn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

var _ = Describe("ListTags", func() {
	var client *Client

	BeforeEach(func() {
		client = NewClient()
	})

	It("lists every tag of the repository, other clusters' included", func() {
		reg := newRegistry()
		img := registrytest.Image()
		for _, tag := range []string{"v1_a", "v1_b", "v2_a"} {
			reg.Push("mirror/app:"+tag, img)
		}

		tags, err := client.ListTags(ctx, endpoint(reg, "mirror/app"))

		Expect(err).NotTo(HaveOccurred())
		Expect(tags).To(ConsistOf("v1_a", "v1_b", "v2_a"))
	})

	It("returns no tag for a repository the registry does not know", func() {
		reg := newRegistry()

		tags, err := client.ListTags(ctx, endpoint(reg, "mirror/unknown"))

		Expect(err).NotTo(HaveOccurred())
		Expect(tags).To(BeEmpty())
	})

	It("lists every page of a paginated tags/list before returning", func() {
		reg := newRegistry(registrytest.WithTagsPageSize(2))
		img := registrytest.Image()
		want := []string{"t1", "t2", "t3", "t4", "t5"}
		for _, tag := range want {
			reg.Push("mirror/app:"+tag, img)
		}

		tags, err := client.ListTags(ctx, endpoint(reg, "mirror/app"))

		Expect(err).NotTo(HaveOccurred())
		Expect(tags).To(ConsistOf(want))
		Expect(len(reg.Requests(http.MethodGet, "/tags/list"))).To(BeNumerically(">=", 3))
	})
})

// nameUnknown is the error body of a registry that does not know the repository of a request.
const nameUnknown = `{"errors":[{"code":"NAME_UNKNOWN","message":"repository name not known to registry"}]}`

// answerDelete answers every manifest DELETE with status and body, and lets the registry
// answer the other requests.
func answerDelete(status int, body string) registrytest.Interceptor {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodDelete || !strings.Contains(r.URL.Path, "/manifests/") {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return true
	}
}

// denied is the error body of a registry that refuses a credential for a request.
const denied = `{"errors":[{"code":"DENIED","message":"requested access to the resource is denied"}]}`

// refuseDelete makes a registry answer every manifest DELETE with status and the body denied.
func refuseDelete(status int) func(*Client, *registrytest.Registry) {
	return func(_ *Client, reg *registrytest.Registry) { reg.Intercept(answerDelete(status, denied)) }
}

// refusedByTokenService makes reg send every request to a token service that answers 401, as a
// registry with bearer authentication whose token service refuses the credential: the deletion
// is refused before its DELETE is sent. The token service has a host name, auth.test, which
// client must dial on the machine (testDial): go-containerregistry follows no realm on a
// loopback address.
func refusedByTokenService(client *Client, reg *registrytest.Registry) {
	ca := registrytest.NewCA()
	var asked atomic.Int32
	// The 401 the client reports is the token service's, not the challenge of the registry.
	DeferCleanup(func() { Expect(asked.Load()).To(BeNumerically(">", 0), "requests to the token service") })
	tokens := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`)
	}))
	tokens.TLS = &tls.Config{Certificates: []tls.Certificate{ca.Certificate("auth.test")}}
	tokens.StartTLS()
	DeferCleanup(tokens.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(tokens.URL, "https://"))
	authHost := "auth.test:" + port
	reg.Intercept(func(w http.ResponseWriter, _ *http.Request) bool {
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+authHost+`/token",service="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
		return true
	})
	client.SetTLS(map[string]TLSHost{authHost: bundleOf(ca)})
}

// answerDeleteOf answers every manifest DELETE with the status its user gets, and denied as
// its body, and lets the registry answer the other requests.
func answerDeleteOf(statuses map[string]int) registrytest.Interceptor {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodDelete || !strings.Contains(r.URL.Path, "/manifests/") {
			return false
		}
		name, _, _ := r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statuses[name])
		_, _ = w.Write([]byte(denied))
		return true
	}
}

// refusedThenUnsupported refuses the deletions of the first of readerThenLegacy with a 403,
// and answers 405 to those of the second.
var refusedThenUnsupported = map[string]int{"reader": http.StatusForbidden, "legacy": http.StatusMethodNotAllowed}

// readerThenLegacy are the credentials of 2 users, in the order they are tried. Each has a
// password: go-containerregistry sends no Authorization header without one.
func readerThenLegacy() []authn.Authenticator {
	return []authn.Authenticator{
		&authn.Basic{Username: "reader", Password: password},
		&authn.Basic{Username: "legacy", Password: password},
	}
}

var _ = Describe("DeleteTag", func() {
	var (
		client *Client
		reg    *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		reg = newRegistry()
	})

	It("deletes the tag and leaves the other tags of the same manifest", func() {
		img := registrytest.Image()
		reg.Push("mirror/app:v1_a", img)
		reg.Push("mirror/app:v1_b", img)

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))).To(Succeed())

		_, err := reg.Head("mirror/app:v1_a")
		Expect(err).To(HaveOccurred())
		_, err = reg.Head("mirror/app:v1_b")
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a digest reference", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app@"+digest.String()))).NotTo(Succeed())

		Expect(reg.Requests(http.MethodDelete, "")).To(BeEmpty())
		_, err := reg.Head("mirror/app:v1_a")
		Expect(err).NotTo(HaveOccurred())
	})

	It("succeeds when the tag is already gone", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_gone"))).To(Succeed())
	})

	It("returns ErrDeleteUnsupported when the registry answers 405", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())
		reg.Intercept(registrytest.Status(http.MethodDelete, "/manifests/", http.StatusMethodNotAllowed, nil))

		err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(errors.Is(err, ErrDeleteUnsupported)).To(BeTrue())
	})

	It("succeeds when the registry answers 400 with the error code NAME_UNKNOWN: the repository is unknown, so the tag is already gone", func() {
		reg.Intercept(answerDelete(http.StatusBadRequest, nameUnknown))

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/unknown:v1_a"))).To(Succeed())

		Expect(reg.Requests(http.MethodDelete, "/manifests/v1_a")).To(HaveLen(1))
	})

	DescribeTable("returns ErrDeleteUnsupported when the registry answers 400 without the error code NAME_UNKNOWN",
		func(body string) {
			reg.Push("mirror/app:v1_a", registrytest.Image())
			reg.Intercept(answerDelete(http.StatusBadRequest, body))

			err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))

			Expect(errors.Is(err, ErrDeleteUnsupported)).To(BeTrue())
		},
		Entry("with the error code UNSUPPORTED", `{"errors":[{"code":"UNSUPPORTED"}]}`),
		Entry("with no error body", ""),
	)

	DescribeTable("returns ErrDeleteUnauthorized when the registry refuses the credential of the deletion",
		func(refuse func(*Client, *registrytest.Registry)) {
			// The token service has a host name, which this client dials on the machine.
			client := newClient(clientOptions{dial: testDial(new(string))})
			reg.Push("mirror/app:v1_a", registrytest.Image())
			refuse(client, reg)

			err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))

			Expect(err).To(MatchError(ErrDeleteUnauthorized))
			Expect(err).NotTo(MatchError(ErrDeleteUnsupported))
		},
		Entry("401 Unauthorized", refuseDelete(http.StatusUnauthorized)),
		Entry("403 Forbidden", refuseDelete(http.StatusForbidden)),
		Entry("401 from the token service the registry sends its clients to", refusedByTokenService),
	)

	It("returns both ErrDeleteUnauthorized and ErrDeleteUnsupported when the registry refuses one credential and answers 405 to another", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())
		reg.Intercept(answerDeleteOf(refusedThenUnsupported))

		err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a", readerThenLegacy()...))

		Expect(errors.Is(err, ErrDeleteUnauthorized)).To(BeTrue())
		Expect(errors.Is(err, ErrDeleteUnsupported)).To(BeTrue())
		Expect(reg.Requests(http.MethodDelete, "/manifests/v1_a")).To(HaveLen(2))
	})

	It("counts nothing in kuik_registry_requests_total", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))).To(Succeed())

		Expect(countedFor(reg.Host())).To(BeZero())
	})
})

var _ = Describe("RefusalDetail", func() {
	var (
		client *Client
		reg    *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		reg = newRegistry()
		reg.Push("mirror/app:v1_a", registrytest.Image())
	})

	It("gives the HTTP status and the registry's error code and message of a refused deletion", func() {
		reg.Intercept(answerDelete(http.StatusForbidden, denied))
		err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))

		refusal, ok := RefusalDetail(err, ErrDeleteUnauthorized)

		Expect(ok).To(BeTrue())
		Expect(refusal).To(Equal(Refusal{StatusCode: http.StatusForbidden, Message: "DENIED: requested access to the resource is denied"}))
	})

	It("gives the HTTP status alone when the registry sent no error body", func() {
		reg.Intercept(answerDelete(http.StatusMethodNotAllowed, ""))
		err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))

		refusal, ok := RefusalDetail(err, ErrDeleteUnsupported)

		Expect(ok).To(BeTrue())
		Expect(refusal).To(Equal(Refusal{StatusCode: http.StatusMethodNotAllowed}))
	})

	DescribeTable("gives what the registry's error body holds when it lacks the code or the message",
		func(body, message string) {
			reg.Intercept(answerDelete(http.StatusBadRequest, body))
			err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))

			refusal, ok := RefusalDetail(err, ErrDeleteUnsupported)

			Expect(ok).To(BeTrue())
			Expect(refusal).To(Equal(Refusal{StatusCode: http.StatusBadRequest, Message: message}))
		},
		Entry("the code alone", `{"errors":[{"code":"UNSUPPORTED"}]}`, "UNSUPPORTED"),
		Entry("the message alone", `{"errors":[{"message":"tag deletion is disabled"}]}`, "tag deletion is disabled"),
	)

	It("gives the answer of a refused manifest deletion, asked as ErrManifestDeleteUnsupported and not as ErrDeleteUnsupported", func() {
		digest := reg.Push("mirror/app:v2_a", registrytest.Image())
		reg.Intercept(answerDelete(http.StatusMethodNotAllowed, `{"errors":[{"code":"UNSUPPORTED","message":"the operation is unsupported"}]}`))
		err := client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))

		refusal, ok := RefusalDetail(err, ErrManifestDeleteUnsupported)
		Expect(ok).To(BeTrue())
		Expect(refusal).To(Equal(Refusal{StatusCode: http.StatusMethodNotAllowed, Message: "UNSUPPORTED: the operation is unsupported"}))
		_, ok = RefusalDetail(err, ErrDeleteUnsupported)
		Expect(ok).To(BeFalse())
	})

	It("gives the answer of the attempt refused the way asked when another credential was refused another way", func() {
		reg.Intercept(answerDeleteOf(refusedThenUnsupported))
		err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a", readerThenLegacy()...))

		unsupported, ok := RefusalDetail(err, ErrDeleteUnsupported)
		Expect(ok).To(BeTrue())
		Expect(unsupported.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		unauthorized, ok := RefusalDetail(err, ErrDeleteUnauthorized)
		Expect(ok).To(BeTrue())
		Expect(unauthorized.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("reports no refusal for an error that is no refused deletion", func() {
		reg.Intercept(answerDelete(http.StatusInternalServerError, ""))
		err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))
		Expect(err).To(HaveOccurred())

		_, ok := RefusalDetail(err, ErrDeleteUnauthorized)

		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("ManifestDigest", func() {
	var (
		client *Client
		reg    *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		reg = newRegistry()
	})

	It("returns the digest of the manifest a tag points at", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		got, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(digest))
	})

	It("returns the digest of the index a multi-platform tag points at", func() {
		digest := reg.PushIndex("mirror/app:v1_a", registrytest.Index())

		got, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(digest))
	})

	It("falls back to a GET when the registry answers the HEAD without Docker-Content-Digest", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())
		reg.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method != http.MethodHead || !strings.Contains(r.URL.Path, "/manifests/") {
				return false
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Content-Length", "2")
			w.WriteHeader(http.StatusOK)
			return true
		})

		got, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(digest))
		Expect(reg.Requests(http.MethodGet, "/manifests/v1_a")).NotTo(BeEmpty())
	})

	It("reports a tag the registry does not hold as not found, with no error", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		_, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_gone"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	It("counts nothing in kuik_registry_requests_total", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		_, _, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(countedFor(reg.Host())).To(BeZero())
	})
})

var _ = Describe("DeleteManifest", func() {
	var (
		client *Client
		reg    *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		reg = newRegistry()
	})

	It("deletes the manifest of a digest reference", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())

		Expect(reg.Requests(http.MethodDelete, "/manifests/"+digest.String())).To(HaveLen(1))
		_, err := reg.Head("mirror/app@" + digest.String())
		Expect(err).To(HaveOccurred())
	})

	It("refuses a tag reference, sending no DELETE", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app:v1_a"))).NotTo(Succeed())

		Expect(reg.Requests(http.MethodDelete, "")).To(BeEmpty())
	})

	It("succeeds when the manifest is already gone", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())
		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())
	})

	DescribeTable("returns ErrManifestDeleteUnsupported when the registry refuses manifest deletion",
		func(code int) {
			digest := reg.Push("mirror/app:v1_a", registrytest.Image())
			reg.Intercept(registrytest.Status(http.MethodDelete, "/manifests/", code, nil))

			err := client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))

			Expect(errors.Is(err, ErrManifestDeleteUnsupported)).To(BeTrue())
		},
		Entry("400 Bad Request", http.StatusBadRequest),
		Entry("405 Method Not Allowed", http.StatusMethodNotAllowed),
		Entry("501 Not Implemented", http.StatusNotImplemented),
	)

	DescribeTable("returns ErrDeleteUnauthorized when the registry refuses the credential of the deletion",
		func(code int) {
			digest := reg.Push("mirror/app:v1_a", registrytest.Image())
			reg.Intercept(answerDelete(code, denied))

			err := client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))

			Expect(errors.Is(err, ErrDeleteUnauthorized)).To(BeTrue())
			Expect(errors.Is(err, ErrManifestDeleteUnsupported)).To(BeFalse())
		},
		Entry("401 Unauthorized", http.StatusUnauthorized),
		Entry("403 Forbidden", http.StatusForbidden),
	)

	It("succeeds when the registry answers 400 with the error code NAME_UNKNOWN: the repository is unknown, so the manifest is already gone", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())
		reg.Intercept(answerDelete(http.StatusBadRequest, nameUnknown))

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/unknown@"+digest.String()))).To(Succeed())

		Expect(reg.Requests(http.MethodDelete, "/manifests/"+digest.String())).To(HaveLen(1))
	})

	It("counts nothing in kuik_registry_requests_total", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())

		Expect(countedFor(reg.Host())).To(BeZero())
	})
})
