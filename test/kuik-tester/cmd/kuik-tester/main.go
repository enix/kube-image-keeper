// Command kuik-tester serves synthetic and proxied registries with
// injectable faults, plus an admin UI and API.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/admin"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/fakeregistry"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
)

func main() {
	path := flag.String("config", "/etc/kuik-tester/config.yaml", "configuration file")
	flag.Parse()
	if err := run(*path); err != nil {
		slog.Error("Stopped on an error", "error", err)
		os.Exit(1)
	}
}

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 64 << 10
	shutdownTimeout   = 10 * time.Second
)

// newServer returns a listener's server: header and idle limits, no read or
// write timeout (SSE, latency faults, uploads), request contexts derived
// from base so cancelling base before Shutdown ends them.
func newServer(base context.Context, addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
}

// newRegistryServer returns the registry server; a nil certs serves plain
// HTTP. With TLS it stays on HTTP/1.1: under HTTP/2 the connection cannot be
// hijacked, so a reset rule would only produce a stream error instead of a
// real connection reset.
func newRegistryServer(base context.Context, addr string, h http.Handler, certs *certReloader) *http.Server {
	srv := newServer(base, addr, h)
	if certs != nil {
		srv.TLSConfig = &tls.Config{GetCertificate: certs.getCertificate, MinVersion: tls.VersionTLS12}
		srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	}
	return srv
}

func run(path string) error {
	cfg, err := testerconfig.Load(path)
	if err != nil {
		return err
	}
	if err := checkWritable(cfg.StatePath); err != nil {
		return err
	}
	var certs *certReloader
	if cfg.TLSCertFile != "" {
		if certs, err = newCertReloader(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
			return err
		}
	}
	if !synth.Runnable {
		slog.Warn("Built without the sleepbins tag, containers cannot start on the synthetic images")
	}
	st, err := state.Open(cfg.StatePath)
	if err != nil {
		return err
	}
	counters := quota.New()
	log := reqlog.New(cfg.LogSize)
	proxies, err := dest.NewProxies(cfg)
	if err != nil {
		return err
	}
	reg := fakeregistry.New(cfg, proxies, st, log, counters)
	svc := admin.NewService(cfg, st, counters, proxies)

	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	if ttl := cfg.ExpireAfter.Std(); ttl > 0 {
		st.SetRefresh(min(time.Hour, ttl/10))
		go expireLoop(base, st, log, ttl)
	}
	// Only the registry server serves TLS.
	type listener struct {
		srv    *http.Server
		useTLS bool
	}
	useTLS := certs != nil
	listeners := []listener{
		{newRegistryServer(base, cfg.RegistryAddr, reg.Handler(), certs), useTLS},
		{newServer(base, cfg.AdminAddr, newAdminHandler(svc, log)), false},
	}
	if cfg.PlainHTTPAddr != "" {
		plain := newServer(base, cfg.PlainHTTPAddr, reg.FixedHostHandler(cfg.PlainHTTPHost))
		listeners = append(listeners, listener{plain, false})
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	errc := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() {
			slog.Info("Started listening", "addr", l.srv.Addr, "tls", l.useTLS)
			var err error
			if l.useTLS {
				err = l.srv.ListenAndServeTLS("", "")
			} else {
				err = l.srv.ListenAndServe()
			}
			if !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	cancelBase() // ends the open requests and streams
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, l := range listeners {
		if serr := l.srv.Shutdown(shutdown); serr != nil {
			slog.Warn("Stopped a listener before its requests ended", "addr", l.srv.Addr, "error", serr)
		}
	}
	return err
}

// maxSweepInterval bounds how long old images and requests outlive their
// expiry.
const maxSweepInterval = 10 * time.Minute

// expireLoop drops the images, digests and logged requests not seen for ttl,
// every min(maxSweepInterval, ttl/10), until ctx ends.
func expireLoop(ctx context.Context, st *state.Store, log *reqlog.Log, ttl time.Duration) {
	t := time.NewTicker(max(time.Second, min(maxSweepInterval, ttl/10)))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now().UTC()
		images, digests, err := st.Expire(now, ttl)
		if err != nil {
			slog.Error("Could not expire old images", "error", err)
		}
		requests := log.Expire(now.Add(-ttl))
		if images+digests+requests > 0 {
			slog.Info("Expired old images and requests",
				"images", images, "digests", digests, "requests", requests, "expireAfter", ttl)
		}
	}
}

// checkWritable proves that the directory of statePath accepts a new file, by
// writing, syncing and removing a probe file. An empty statePath keeps the
// state in memory and needs no directory.
func checkWritable(statePath string) error {
	if statePath == "" {
		return nil
	}
	dir := filepath.Dir(statePath)
	f, err := os.CreateTemp(dir, ".kuik-tester-probe-*")
	if err != nil {
		return fmt.Errorf("state directory %s is not writable: %w", dir, err)
	}
	_, err = f.Write([]byte{0})
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if rerr := os.Remove(f.Name()); err == nil {
		err = rerr
	}
	if err != nil {
		return fmt.Errorf("state directory %s is not writable: %w", dir, err)
	}
	return nil
}
