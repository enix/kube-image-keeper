package testutil

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/google/go-containerregistry/pkg/registry"
)

// NewRegistry returns go-containerregistry's in-memory registry, which logs
// nothing. Use it as the handler of a test server that needs a registry of
// its own.
//
// The in-memory registry keeps one blob store for every repository and
// answers the DELETE of a blob it does not have with a 500; a real registry
// answers 404 (BLOB_UNKNOWN). NewRegistry answers 404 there too, so a client
// that deletes the same blob from two repositories sees what it would see on
// a real backend.
func NewRegistry() http.Handler {
	h := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/blobs/") {
			head := r.Clone(r.Context())
			head.Method = http.MethodHead
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, head)
			if rec.Code == http.StatusNotFound {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"errors":[{"code":"BLOB_UNKNOWN","message":"blob unknown to registry"}]}`)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// NewBackend starts an in-memory registry that logs nothing. The caller
// closes the returned server.
func NewBackend() *httptest.Server {
	return httptest.NewServer(NewRegistry())
}
