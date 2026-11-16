package main

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// certReloader serves the key pair of two files and loads them again when
// the modification time of either changes; a pair that fails to load keeps
// the previous one in use.
type certReloader struct {
	certFile, keyFile string

	mu              sync.Mutex
	cert            *tls.Certificate
	certMod, keyMod time.Time // of the pair in use, or of the last failed attempt
}

// newCertReloader loads the pair once; the error names both files.
func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	c := &certReloader{certFile: certFile, keyFile: keyFile}
	certMod, keyMod, err := c.mtimes()
	if err != nil {
		return nil, fmt.Errorf("loading the TLS pair %s and %s: %w", certFile, keyFile, err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading the TLS pair %s and %s: %w", certFile, keyFile, err)
	}
	c.cert, c.certMod, c.keyMod = &cert, certMod, keyMod
	return c, nil
}

func (c *certReloader) mtimes() (certMod, keyMod time.Time, err error) {
	ci, err := os.Stat(c.certFile)
	if err != nil {
		return certMod, keyMod, err
	}
	ki, err := os.Stat(c.keyFile)
	if err != nil {
		return certMod, keyMod, err
	}
	return ci.ModTime(), ki.ModTime(), nil
}

// getCertificate is a tls.Config.GetCertificate.
func (c *certReloader) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	certMod, keyMod, err := c.mtimes()
	if err != nil || (certMod.Equal(c.certMod) && keyMod.Equal(c.keyMod)) {
		// A missing file is retried at the next handshake.
		return c.cert, nil
	}
	c.certMod, c.keyMod = certMod, keyMod
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		slog.Warn("Kept the previous TLS certificate until both new files load", "path", c.certFile, "error", err)
		return c.cert, nil
	}
	c.cert = &cert
	slog.Info("Reloaded the TLS certificate", "path", c.certFile)
	return c.cert, nil
}
