// Package clienttest serves an in-process kuik-tester admin API for the tests
// of code built on package client: the real admin handlers over an empty
// in-memory state, with proxy hosts backed by in-memory OCI registries.
//
// The registry endpoints themselves are not served: the request log only
// holds the entries a test adds with AddRequest.
package clienttest

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/admin"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/client"
)

// logSize is the number of request log entries kept.
const logSize = 100

// Server is an in-process kuik-tester admin API.
type Server struct {
	// URL is the admin base URL, without /api/v1.
	URL string
	// Client is a client of the server, with no set.
	Client *client.Client

	log      *reqlog.Log
	admin    *httptest.Server
	backends []*httptest.Server
}

// NewServer starts a Server with one proxy host per name, each forwarded to
// an in-memory registry of its own; a name is a host label (or a path.Match
// pattern without a dot). It panics on a setup error. The caller closes the
// server with Close.
func NewServer(proxies ...string) *Server {
	s := &Server{}
	cfg := &testerconfig.Config{BaseDomain: "kt.test", LogSize: logSize}
	for _, name := range proxies {
		srv := testutil.NewBackend()
		s.backends = append(s.backends, srv)
		cfg.Proxies = append(cfg.Proxies, testerconfig.ProxyHost{Name: name, Backend: srv.URL})
	}
	if err := cfg.Resolve(); err != nil {
		s.Close()
		panic(fmt.Sprintf("clienttest: %v", err))
	}
	topology, err := dest.NewProxies(cfg)
	if err != nil {
		s.Close()
		panic(fmt.Sprintf("clienttest: %v", err))
	}
	st, err := state.Open("")
	if err != nil {
		s.Close()
		panic(fmt.Sprintf("clienttest: %v", err))
	}
	s.log = reqlog.New(logSize)
	svc := admin.NewService(cfg, st, quota.New(), topology)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", admin.NewAPI(svc, s.log)))
	s.admin = httptest.NewServer(admin.SameOrigin(mux))
	s.URL = s.admin.URL
	s.Client = client.New(s.URL, "")
	return s
}

// Close stops the admin server and every backend registry.
func (s *Server) Close() {
	if s.admin != nil {
		s.admin.Close()
	}
	for _, b := range s.backends {
		b.Close()
	}
}

// AddRequest records e in the request log, as if the registry had served it.
func (s *Server) AddRequest(e api.LogEntry) { s.log.Add(e) }
