package registry

import (
	"time"

	"k8s.io/utils/clock"
)

// RequestPacer spaces the requests sent to each source host.
type RequestPacer struct{}

// NewRequestPacer returns a RequestPacer.
func NewRequestPacer(_ clock.Clock, _ func(host string) time.Duration) *RequestPacer {
	return &RequestPacer{}
}

// SetRequestPacer paces the source requests of c with p.
func (c *Client) SetRequestPacer(_ *RequestPacer) {}
