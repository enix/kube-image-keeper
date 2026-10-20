package registry

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// PendingTLSHost is a host whose CA bundle, named by source, is not read yet. Its requests wait
// for the next settings rather than fail: a reconciler loop started before the CA Secrets
// were read would otherwise report the host unreachable and wait a whole interval.
func PendingTLSHost(source string) TLSHost {
	return TLSHost{CASource: source, Pending: true}
}

// refuseIPProxy wraps the proxy function of the transport of a host with a tls entry.
// connectionVerifier tells the hops apart by server name, so an HTTPS proxy that would send
// the registry's would be verified as the registry, and could receive the proxy credentials
// under the entry's policy: a proxy sharing the registry's host name, whatever its port, and
// a proxy addressed by IP in front of a registry addressed by IP, Go sending no server name to
// either. Such a request is refused before anything is dialled.
func refuseIPProxy(proxy func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if proxy == nil {
			return nil, nil
		}
		proxyURL, err := proxy(req)
		if err != nil || proxyURL == nil || proxyURL.Scheme != "https" {
			return proxyURL, err
		}
		if net.ParseIP(req.URL.Hostname()) != nil && net.ParseIP(proxyURL.Hostname()) != nil {
			return nil, &notSentError{fmt.Errorf(
				"refusing the HTTPS proxy %s, addressed by IP, for %s: name the proxy by a host name", proxyURL.Host, req.URL.Host)}
		}
		// The names are compared as net/http sends them in TLS: in their IDNA ASCII form.
		proxyName, err := idnaASCII(proxyURL.Hostname())
		if err != nil {
			return nil, &notSentError{fmt.Errorf("refusing the HTTPS proxy %s for %s: its host name is not a valid IDNA name: %w",
				proxyURL.Host, req.URL.Host, err)}
		}
		requestName, err := idnaASCII(req.URL.Hostname())
		if err != nil {
			requestName = req.URL.Hostname()
		}
		if strings.EqualFold(requestName, proxyName) {
			return nil, &notSentError{fmt.Errorf(
				"refusing the HTTPS proxy %s for %s: a proxy sharing the registry's host name cannot be told apart from it",
				proxyURL.Host, req.URL.Host)}
		}
		return proxyURL, nil
	}
}

// idnaASCII is the IDNA ASCII form of a host name, the conversion net/http applies before it
// dials and sends a server name.
func idnaASCII(name string) (string, error) {
	if isASCII(name) {
		return name, nil
	}
	return idna.Lookup.ToASCII(name)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
