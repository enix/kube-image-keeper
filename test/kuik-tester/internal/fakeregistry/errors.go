package fakeregistry

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// OCI distribution error codes.
const (
	codeUnknown         = "UNKNOWN"
	codeUnauthorized    = "UNAUTHORIZED"
	codeDenied          = "DENIED"
	codeNameUnknown     = "NAME_UNKNOWN"
	codeManifestUnknown = "MANIFEST_UNKNOWN"
	codeBlobUnknown     = "BLOB_UNKNOWN"
	codeUnsupported     = "UNSUPPORTED"
	codeTooManyRequests = "TOOMANYREQUESTS"
)

// writeError writes an OCI distribution error. HEAD responses get the
// same headers and no body.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	body, _ := json.Marshal(map[string]any{
		"errors": []map[string]string{{"code": code, "message": msg}},
	})
	writeResponse(w, r, status, "application/json", "", body)
}

// writeBody writes a successful response with its digest headers.
func writeBody(w http.ResponseWriter, r *http.Request, contentType, digest string, body []byte) {
	writeResponse(w, r, http.StatusOK, contentType, digest, body)
}

// writeResponse is the one writer of registry responses: a HEAD gets the
// headers of the GET and no body.
func writeResponse(w http.ResponseWriter, r *http.Request, status int, contentType, digest string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if digest != "" {
		w.Header().Set("Docker-Content-Digest", digest)
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	b, _ := json.Marshal(v)
	writeBody(w, r, "application/json", "", b)
}
