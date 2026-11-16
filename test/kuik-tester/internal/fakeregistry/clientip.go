package fakeregistry

import (
	"net"
	"net/http"
	"slices"
	"strings"
)

func (s *Server) trusted(ip net.IP) bool {
	for _, n := range s.trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func peerHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// trustedPeer reports whether the connection comes from a trusted proxy.
func (s *Server) trustedPeer(r *http.Request) bool {
	ip := net.ParseIP(peerHost(r))
	return ip != nil && s.trusted(ip)
}

// clientIP returns the address of the original client: the peer itself
// unless it is a trusted proxy, in which case X-Forwarded-For is walked
// from the right up to the first address that is not a trusted proxy.
func (s *Server) clientIP(r *http.Request) string {
	peer := peerHost(r)
	if !s.trustedPeer(r) {
		return peer
	}
	var addrs []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for a := range strings.SplitSeq(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				addrs = append(addrs, a)
			}
		}
	}
	if len(addrs) == 0 {
		return peer
	}
	var ip net.IP
	for _, a := range slices.Backward(addrs) {
		if ip = parseXFF(a); ip == nil {
			return peer
		}
		if !s.trusted(ip) {
			return ip.String()
		}
	}
	// Every hop is a trusted proxy: the leftmost one, parsed last, is the
	// best answer.
	return ip.String()
}

// parseXFF parses an X-Forwarded-For entry, which may carry a port and
// brackets around an IPv6 address.
func parseXFF(a string) net.IP {
	if h, _, err := net.SplitHostPort(a); err == nil {
		a = h
	}
	return net.ParseIP(strings.Trim(a, "[]"))
}
