package registry

// Reload makes the requests waiting re-read the requestInterval of their host.
func (p *RequestPacer) Reload() {}

// queued counts the requests waiting on host, named as the image writes it.
func (p *RequestPacer) queued(_ string) int { return 0 }
