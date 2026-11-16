package main

import (
	"net/http"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/admin"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
)

// newAdminHandler serves the admin listener: GET /healthz, the API under
// /api/v1/ and the UI on every other path, all behind admin.SameOrigin.
func newAdminHandler(svc *admin.Service, log *reqlog.Log) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", admin.NewAPI(svc, log)))
	mux.Handle("/", admin.NewUI(svc, log))
	return admin.SameOrigin(mux)
}
