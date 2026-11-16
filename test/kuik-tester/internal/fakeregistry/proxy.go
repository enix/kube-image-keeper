package fakeregistry

import (
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// forward hands r to the backend registry. The headers already set on w
// (API version, rule headers, rate limits) are set aside while the proxy
// runs, because ReverseProxy clears w.Header() after a 1xx response, and
// are put on the final response, replacing the backend's values.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, backend *url.URL, e *api.LogEntry) {
	e.Forwarded = true
	pre := w.Header().Clone()
	clear(w.Header())
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(backend)
			pr.SetXForwarded()
			// SetXForwarded only looks at r.TLS, wrong behind the Ingress.
			pr.Out.Header.Set("X-Forwarded-Proto", s.scheme(r))
			// The client the log resolved, not the Ingress.
			pr.Out.Header.Set("X-Forwarded-For", e.ClientIP)
			pr.Out.Header.Del("Authorization")
		},
		ModifyResponse: func(res *http.Response) error {
			maps.Copy(res.Header, pre)
			// res.Location would resolve a relative value against the request.
			if loc, err := url.Parse(res.Header.Get("Location")); err == nil && loc.IsAbs() && loc.Host == backend.Host {
				loc.Scheme, loc.Host = s.scheme(r), r.Host
				res.Header.Set("Location", loc.String())
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return
			}
			maps.Copy(w.Header(), pre)
			writeError(w, r, http.StatusBadGateway, codeUnknown, "backend unreachable")
		},
		FlushInterval: -1,
		Transport:     s.transport,
	}
	rp.ServeHTTP(w, r)
}
