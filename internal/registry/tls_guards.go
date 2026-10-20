package registry

import (
	"net/http"
	"net/url"
)

// PendingTLSHost is a stub.
func PendingTLSHost(source string) TLSHost { return TLSHost{CASource: source} }

// refuseIPProxy is a stub.
func refuseIPProxy(proxy func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return proxy
}
