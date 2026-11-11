package registry

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/random"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/clock"
	testingclock "k8s.io/utils/clock/testing"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

const (
	// pacingInterval is the requestInterval of the pacing specs, unless one sets another.
	pacingInterval = time.Second
	// pingPath is the path of the ping go-containerregistry sends before any other request.
	pingPath = "/v2/"
	// tokenPath is the path of the token service bearerChallenge names.
	tokenPath = "/token"
	// firstHead and secondHead are the paths of the manifest HEADs of checks of first:v1 and
	// second:v1.
	firstHead  = "/v2/first/manifests/v1"
	secondHead = "/v2/second/manifests/v1"
	appHead    = "/v2/app/manifests/v1"
)

// arrival is one request a registry received, at the time of the fake clock.
type arrival struct {
	at     time.Time
	method string
	path   string
}

// arrivals records the requests one server receives, against the fake clock.
type arrivals struct {
	clk *testingclock.FakeClock

	mu   sync.Mutex
	list []arrival
}

func (a *arrivals) record(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = append(a.list, arrival{at: a.clk.Now(), method: r.Method, path: r.URL.Path})
}

func (a *arrivals) all() []arrival {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]arrival(nil), a.list...)
}

func (a *arrivals) count() int { return len(a.all()) }

// matching returns the arrivals whose path contains pathPart.
func (a *arrivals) matching(pathPart string) []arrival {
	var found []arrival
	for _, r := range a.all() {
		if strings.Contains(r.path, pathPart) {
			found = append(found, r)
		}
	}
	return found
}

func (a *arrivals) paths() []string {
	all := a.all()
	paths := make([]string, 0, len(all))
	for _, r := range all {
		paths = append(paths, r.path)
	}
	return paths
}

// offsets returns the time of each arrival from start.
func (a *arrivals) offsets(start time.Time) []time.Duration {
	all := a.all()
	offsets := make([]time.Duration, 0, len(all))
	for _, r := range all {
		offsets = append(offsets, r.at.Sub(start))
	}
	return offsets
}

// bearerChallenge makes a registry ask for a bearer token from the token service at realm,
// and answers that service itself when realm is its own host.
func bearerChallenge(realm string) registrytest.Interceptor {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == tokenPath {
			_, _ = fmt.Fprint(w, `{"token":"test-token"}`)
			return true
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			return false
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+realm+`/token",service="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}
}

// expectSpaced checks that each request arrived at least pacingInterval after the one before.
func expectSpaced(list []arrival) {
	GinkgoHelper()
	Expect(len(list)).To(BeNumerically(">", 1))
	for i := 1; i < len(list); i++ {
		Expect(list[i].at.Sub(list[i-1].at)).To(BeNumerically(">=", pacingInterval),
			"%s %s after %s %s", list[i].method, list[i].path, list[i-1].method, list[i-1].path)
	}
}

// holdingClock is a fake clock that can hold back the next timer it makes: the timer fires
// on the fake clock as usual, but its waiter only sees it once released, as a goroutine the
// scheduler resumes late would.
type holdingClock struct {
	*testingclock.FakeClock

	mu      sync.Mutex
	holding chan struct{}
}

// holdNext holds back the next timer made, until the returned function is called.
func (c *holdingClock) holdNext() (release func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	held := make(chan struct{})
	c.holding = held
	return func() { close(held) }
}

func (c *holdingClock) NewTimer(d time.Duration) clock.Timer {
	timer := c.FakeClock.NewTimer(d)
	c.mu.Lock()
	held := c.holding
	c.holding = nil
	c.mu.Unlock()
	if held == nil {
		return timer
	}
	fired := make(chan time.Time, 1)
	go func() {
		t := <-timer.C()
		<-held
		fired <- t
	}()
	return &heldTimer{Timer: timer, fired: fired}
}

type heldTimer struct {
	clock.Timer
	fired chan time.Time
}

func (t *heldTimer) C() <-chan time.Time { return t.fired }

var _ = Describe("Request pacing", func() {
	const interval = pacingInterval

	var (
		clk       *testingclock.FakeClock
		start     time.Time
		client    *Client
		pacer     *RequestPacer
		ca        *registrytest.CA
		tlsHosts  map[string]TLSHost
		dockerHub string
		// opCtx bounds the operations of a spec: a request still waiting when it ends stops.
		opCtx context.Context

		intervalsMu sync.Mutex
		intervals   map[string]time.Duration
		fallback    time.Duration
		// onNextRead runs once, on the next read of an interval, after the value is read.
		onNextRead func()
	)

	// setInterval sets the requestInterval every host without its own resolves to, as a
	// reload would.
	setInterval := func(d time.Duration) {
		intervalsMu.Lock()
		defer intervalsMu.Unlock()
		fallback = d
	}
	intervalOf := func(host string) time.Duration {
		intervalsMu.Lock()
		d, ok := intervals[host]
		if !ok {
			d = fallback
		}
		hook := onNextRead
		onNextRead = nil
		intervalsMu.Unlock()
		if hook != nil {
			hook()
		}
		return d
	}
	// reload sets the requestInterval every host without its own resolves to, and hands the
	// reload to the pacer as the reconciler does.
	reload := func(d time.Duration) {
		setInterval(d)
		pacer.Reload()
	}
	// queued counts the requests waiting on host, the one whose turn has come included.
	queued := func(host string) func() int {
		return func() int { return pacer.queued(host) }
	}

	BeforeEach(func() {
		start = time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
		clk = testingclock.NewFakeClock(start)
		intervals = map[string]time.Duration{}
		onNextRead = nil
		setInterval(interval)
		dockerHub = ""
		client = newClient(clientOptions{dial: testDial(&dockerHub)})
		pacer = NewRequestPacer(clk, intervalOf)
		client.SetRequestPacer(pacer)
		ca = registrytest.NewCA()
		tlsHosts = map[string]TLSHost{}
		var cancel context.CancelFunc
		opCtx, cancel = context.WithCancel(ctx)
		DeferCleanup(cancel)
	})

	// serve starts an HTTPS registry under dnsName, trusted by the client, and returns it
	// with its host.
	serve := func(dnsName string) (*registrytest.Registry, string) {
		GinkgoHelper()
		reg := newRegistry(registrytest.WithTLS(ca.Certificate(dnsName)))
		host := dnsName + ":" + reg.Port()
		tlsHosts[host] = bundleOf(ca)
		client.SetTLS(tlsHosts)
		return reg, host
	}

	// onDefaultPort starts an HTTPS registry under name and makes client reach it on the
	// default port, name and name:443 alike.
	onDefaultPort := func(name string) *registrytest.Registry {
		GinkgoHelper()
		reg := newRegistry(registrytest.WithTLS(ca.Certificate(name)))
		var dialer net.Dialer
		client = newClient(clientOptions{dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, _, err := net.SplitHostPort(addr); err == nil && host == name {
				addr = net.JoinHostPort("127.0.0.1", reg.Port())
			}
			return dialer.DialContext(ctx, network, addr)
		}})
		client.SetRequestPacer(pacer)
		client.SetTLS(map[string]TLSHost{name: bundleOf(ca), name + ":443": bundleOf(ca)})
		return reg
	}

	// record records the requests reg receives from now on, then lets next answer them (the
	// registry when nil).
	record := func(reg *registrytest.Registry, next registrytest.Interceptor) *arrivals {
		rec := &arrivals{clk: clk}
		reg.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			rec.record(r)
			return next != nil && next(w, r)
		})
		return rec
	}

	// run runs f in the background and returns a channel closed when it returns.
	run := func(f func()) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			f()
		}()
		return done
	}

	// releaseAfter steps the clock d to the slot of the next waiting request, and checks it
	// arrives there and not a millisecond before: count is what it adds to.
	releaseAfter := func(d time.Duration, count func() int) {
		GinkgoHelper()
		Eventually(clk.HasWaiters).Should(BeTrue())
		before := count()
		clk.Step(d - time.Millisecond)
		Consistently(count, "20ms", "2ms").Should(Equal(before))
		clk.Step(time.Millisecond)
		Eventually(count).Should(BeNumerically(">", before))
	}

	// drive releases each waiting request, interval after the one before, until done is
	// closed, and returns how many waited.
	drive := func(count func() int, done <-chan struct{}) int {
		GinkgoHelper()
		released := 0
		for {
			select {
			case <-done:
				return released
			default:
			}
			if !clk.HasWaiters() {
				time.Sleep(time.Millisecond)
				continue
			}
			releaseAfter(interval, count)
			released++
		}
	}

	check := func(c *Client, host, ref string) {
		GinkgoHelper()
		_, err := c.Check(opCtx, Endpoint{Reference: host + "/" + ref})
		Expect(err).NotTo(HaveOccurred())
	}

	copyTo := func(host, ref string, dst Endpoint) {
		GinkgoHelper()
		_, err := client.Copy(opCtx, Endpoint{Reference: host + "/" + ref}, dst, []string{copyTag})
		Expect(err).NotTo(HaveOccurred())
	}

	Context("with a requestInterval on a source host", func() {
		It("spaces the requests of a check to that host by the interval, ping and token included", func() {
			reg, host := serve("registry-a.test")
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, bearerChallenge(host))

			released := drive(rec.count, run(func() { check(client, host, "app:v1") }))

			Expect(rec.paths()).To(Equal([]string{pingPath, tokenPath, appHead}))
			Expect(released).To(Equal(2))
			expectSpaced(rec.all())
		})

		It("spaces the source requests of a copy by the interval, child manifests and blobs included", func() {
			src, host := serve("registry-a.test")
			src.PushIndex("app:v1", registrytest.Index())
			rec := record(src, nil)
			dst := newRegistry()

			released := drive(rec.count, run(func() { copyTo(host, "app:v1", endpoint(dst, "mirror/app")) }))

			Expect(rec.matching("/manifests/sha256:")).To(HaveLen(2))
			Expect(rec.matching("/blobs/")).NotTo(BeEmpty())
			Expect(released).To(Equal(rec.count() - 1))
			expectSpaced(rec.all())
		})

		It("spaces the source check of a copy and its transfer in one series", func() {
			src, host := serve("registry-a.test")
			src.Push("app:v1", registrytest.Image())
			rec := record(src, nil)
			dst := newRegistry()

			released := drive(rec.count, run(func() {
				_, err := client.CheckCopySource(opCtx, Endpoint{Reference: host + "/app:v1"})
				Expect(err).NotTo(HaveOccurred())
				copyTo(host, "app:v1", endpoint(dst, "mirror/app"))
			}))

			Expect(released).To(Equal(rec.count() - 1))
			expectSpaced(rec.all())
		})

		It("spaces a check and a copy to the same host in one series", func() {
			src, host := serve("registry-a.test")
			src.Push("app:v1", registrytest.Image())
			rec := record(src, nil)
			dst := newRegistry()

			checked := run(func() { check(client, host, "app:v1") })
			// The check's ping went, its HEAD waits: the copy asks behind it.
			Eventually(queued(host)).Should(Equal(1))
			copied := run(func() { copyTo(host, "app:v1", endpoint(dst, "mirror/app")) })
			Eventually(queued(host)).Should(Equal(2))
			released := drive(rec.count, run(func() {
				<-checked
				<-copied
			}))

			Expect(rec.matching("/blobs/")).NotTo(BeEmpty())
			Expect(released).To(Equal(rec.count() - 1))
			expectSpaced(rec.all())
		})

		It("waits at most one request of a copy of the same host before each request of a check", func() {
			src, host := serve("registry-a.test")
			big, err := random.Image(256, 6)
			Expect(err).NotTo(HaveOccurred())
			src.Push("big:v1", big)
			src.Push("app:v1", registrytest.Image())
			rec := record(src, bearerChallenge(host))
			dst := newRegistry()

			copied := run(func() { copyTo(host, "big:v1", endpoint(dst, "mirror/big")) })
			for len(rec.matching("/blobs/")) == 0 {
				releaseAfter(interval, rec.count)
			}
			// From here on, the copy only reads its blobs: every ping, token and app request
			// is the check's.
			from := rec.count()
			checked := run(func() { check(client, host, "app:v1") })
			drive(rec.count, run(func() {
				<-copied
				<-checked
			}))

			var checkRequests []arrival
			for _, r := range rec.all()[from:] {
				if r.path == pingPath || r.path == tokenPath || strings.HasPrefix(r.path, "/v2/app/") {
					checkRequests = append(checkRequests, r)
				}
			}
			Expect(checkRequests).To(HaveLen(3))
			for i := 1; i < len(checkRequests); i++ {
				Expect(checkRequests[i].at.Sub(checkRequests[i-1].at)).To(BeNumerically("<=", 2*interval),
					"%s after %s", checkRequests[i].path, checkRequests[i-1].path)
			}
			expectSpaced(rec.all())
		})

		It("spaces the next request from the last one sent when the latest one gave up waiting", func() {
			reg, host := serve("registry-a.test")
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)

			// The ping goes at once, the HEAD gives up waiting for its slot.
			bounded, cancel := context.WithCancel(opCtx)
			defer cancel()
			var err error
			gaveUp := run(func() { _, err = client.Check(bounded, Endpoint{Reference: host + "/app:v1"}) })
			Eventually(clk.Waiters).Should(Equal(1))
			cancel()
			Eventually(gaveUp).Should(BeClosed())
			Expect(checkReason(err)).To(Equal(kuikv1alpha1.CheckUnreachable))

			done := run(func() { check(client, host, "app:v1") })
			releaseAfter(interval, rec.count)
			releaseAfter(interval, rec.count)
			Eventually(done).Should(BeClosed())

			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, interval, 2 * interval}))
		})

		It("spaces the next request one interval from the last one sent after 2 waiters gave up in order", func() {
			reg, host := serve("registry-a.test")
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)
			image := Endpoint{Reference: host + "/app:v1"}

			ctxX, cancelX := context.WithCancel(opCtx)
			defer cancelX()
			ctxY, cancelY := context.WithCancel(opCtx)
			defer cancelY()
			var errX, errY error
			x := run(func() { _, errX = client.Check(ctxX, image) })
			// X's ping went, its HEAD waits; then Y's ping waits behind it.
			Eventually(queued(host)).Should(Equal(1))
			y := run(func() { _, errY = client.Check(ctxY, image) })
			Eventually(queued(host)).Should(Equal(2))
			cancelX()
			Eventually(x).Should(BeClosed())
			cancelY()
			Eventually(y).Should(BeClosed())
			Expect(checkReason(errX)).To(Equal(kuikv1alpha1.CheckUnreachable))
			Expect(checkReason(errY)).To(Equal(kuikv1alpha1.CheckUnreachable))

			done := run(func() { check(client, host, "app:v1") })
			releaseAfter(interval, rec.count)
			releaseAfter(interval, rec.count)
			Eventually(done).Should(BeClosed())

			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, interval, 2 * interval}))
		})

		It("sends the next waiting request one interval after the last request sent when the one ahead gives up", func() {
			reg, host := serve("registry-a.test")
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)

			ctxX, cancelX := context.WithCancel(opCtx)
			defer cancelX()
			x := run(func() { _, _ = client.Check(ctxX, Endpoint{Reference: host + "/app:v1"}) })
			// X's ping went, its HEAD waits; then Y's ping waits behind it.
			Eventually(queued(host)).Should(Equal(1))
			y := run(func() { check(client, host, "app:v1") })
			Eventually(queued(host)).Should(Equal(2))
			cancelX()
			Eventually(x).Should(BeClosed())

			releaseAfter(interval, rec.count)
			releaseAfter(interval, rec.count)
			Eventually(y).Should(BeClosed())

			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, interval, 2 * interval}))
		})

		It("sends a request no sooner than the interval after the one ahead left, even when that one resumed late", func() {
			held := &holdingClock{FakeClock: clk}
			pacer = NewRequestPacer(held, intervalOf)
			client.SetRequestPacer(pacer)
			reg, host := serve("registry-a.test")
			reg.Push("first:v1", registrytest.Image())
			reg.Push("second:v1", registrytest.Image())
			rec := record(reg, nil)

			release := held.holdNext()
			first := run(func() { check(client, host, "first:v1") })
			// The first HEAD waits on the held timer, the second ping behind it.
			Eventually(clk.Waiters).Should(Equal(1))
			second := run(func() { check(client, host, "second:v1") })
			Eventually(queued(host)).Should(Equal(2))
			// Both are due, but the first HEAD resumes late: the second ping waits for it.
			clk.Step(5 * interval)
			Consistently(rec.count, "20ms", "2ms").Should(Equal(1))
			release()
			Eventually(rec.count).Should(Equal(2))
			releaseAfter(interval, rec.count)
			releaseAfter(interval, rec.count)
			Eventually(first).Should(BeClosed())
			Eventually(second).Should(BeClosed())

			Expect(rec.paths()).To(Equal([]string{pingPath, firstHead, pingPath, secondHead}))
			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, 5 * interval, 6 * interval, 7 * interval}))
		})

		It("sends the requests waiting on a host in the order they asked", func() {
			reg, host := serve("registry-a.test")
			repos := []string{"first", "second", "third"}
			for _, repo := range repos {
				reg.Push(repo+":v1", registrytest.Image())
			}
			rec := record(reg, nil)

			done := make([]<-chan struct{}, 0, len(repos))
			for i, repo := range repos {
				done = append(done, run(func() { check(client, host, repo+":v1") }))
				// Each check waits before the next one asks.
				Eventually(queued(host)).Should(Equal(i + 1))
			}
			// Before each step, every request still owed has asked: first's HEAD and the 2
			// pings, then each HEAD as its ping returns.
			for _, waiting := range []int{3, 2, 2, 2, 1} {
				Eventually(queued(host)).Should(Equal(waiting))
				releaseAfter(interval, rec.count)
			}
			for _, d := range done {
				Eventually(d).Should(BeClosed())
			}

			Expect(rec.paths()).To(Equal([]string{
				pingPath, "/v2/first/manifests/v1",
				pingPath, pingPath,
				"/v2/second/manifests/v1", "/v2/third/manifests/v1",
			}))
			expectSpaced(rec.all())
		})

		It("does not pace a request redirected to another host", func() {
			src, host := serve("registry-a.test")
			cdn, cdnHost := serve("cdn.test")
			img := registrytest.Image()
			src.Push("app:v1", img)
			cdn.Push("app:v1", img)
			srcRec := record(src, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/blobs/") {
					return false
				}
				http.Redirect(w, r, "https://"+cdnHost+r.URL.Path, http.StatusTemporaryRedirect)
				return true
			})
			cdnRec := record(cdn, nil)
			dst := newRegistry()

			released := drive(srcRec.count, run(func() { copyTo(host, "app:v1", endpoint(dst, "mirror/app")) }))

			Expect(len(cdnRec.matching("/blobs/"))).To(BeNumerically(">", 1))
			Expect(released).To(Equal(srcRec.count() - 1))
			expectSpaced(srcRec.all())
		})

		It("paces a blob redirected to the source host with its default port written out", func() {
			const name = "registry-a.test"
			reg := onDefaultPort(name)
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/blobs/") || r.Host == name+":443" {
					return false
				}
				http.Redirect(w, r, "https://"+name+":443"+r.URL.Path, http.StatusTemporaryRedirect)
				return true
			})
			dst := newRegistry()

			released := drive(rec.count, run(func() { copyTo(name, "app:v1", endpoint(dst, "mirror/app")) }))

			// Each blob is asked twice: the redirect, then the redirected request.
			Expect(len(rec.matching("/blobs/"))).To(BeNumerically(">=", 4))
			Expect(released).To(Equal(rec.count() - 1))
			expectSpaced(rec.all())
		})

		It("spaces the requests to a source written with its default port whose scheme falls back to http, reached over https", func() {
			// go-containerregistry may reach a .local host over plain HTTP; this one answers
			// its https ping, so every request goes to registry-a.local:443 over HTTPS.
			const name = "registry-a.local"
			reg := onDefaultPort(name)
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)

			released := drive(rec.count, run(func() { check(client, name+":443", "app:v1") }))

			Expect(rec.paths()).To(Equal([]string{pingPath, appHead}))
			Expect(released).To(Equal(1))
			expectSpaced(rec.all())
		})

		It("spaces the requests to a plain-HTTP source after its failed https ping, the http ping included", func() {
			// go-containerregistry reaches a loopback host over plain HTTP once its https ping
			// fails: that ping never reaches the registry, the http one waits its turn after it.
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)

			released := drive(rec.count, run(func() { check(client, reg.Host(), "app:v1") }))

			Expect(rec.paths()).To(Equal([]string{pingPath, appHead}))
			Expect(rec.offsets(start)).To(Equal([]time.Duration{interval, 2 * interval}))
			Expect(released).To(Equal(2))
		})

		It("reads the requestInterval of a source written with its default port from the block written the same way", func() {
			const name = "registry-a.test"
			setInterval(0)
			intervalsMu.Lock()
			intervals[name+":443"] = interval
			intervalsMu.Unlock()
			reg := onDefaultPort(name)
			big, err := random.Image(256, 6)
			Expect(err).NotTo(HaveOccurred())
			reg.Push("app:v1", big)
			rec := record(reg, nil)
			dst := newRegistry()

			done := run(func() { copyTo(name+":443", "app:v1", endpoint(dst, "mirror/app")) })
			released, mostWaiting := 0, 0
			for copying := true; copying; {
				select {
				case <-done:
					copying = false
					continue
				default:
				}
				if !clk.HasWaiters() {
					time.Sleep(time.Millisecond)
					continue
				}
				mostWaiting = max(mostWaiting, pacer.queued(name+":443"))
				releaseAfter(interval, rec.count)
				released++
			}

			Expect(rec.matching("/blobs/")).NotTo(BeEmpty())
			Expect(released).To(Equal(rec.count() - 1))
			expectSpaced(rec.all())
			// The copy runs one request at a time.
			Expect(mostWaiting).To(Equal(1))
		})

		It("does not pace a token request sent to another host", func() {
			tokenRec := &arrivals{clk: clk}
			tokens := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokenRec.record(r)
				_, _ = fmt.Fprint(w, `{"token":"test-token"}`)
			}))
			tokens.TLS = &tls.Config{Certificates: []tls.Certificate{ca.Certificate("auth.test")}}
			tokens.StartTLS()
			DeferCleanup(tokens.Close)
			_, tokenPort, _ := net.SplitHostPort(strings.TrimPrefix(tokens.URL, "https://"))
			authHost := "auth.test:" + tokenPort
			tlsHosts[authHost] = bundleOf(ca)
			regA, hostA := serve("registry-a.test")
			regB, hostB := serve("registry-b.test")
			regA.Push("app:v1", registrytest.Image())
			regB.Push("app:v1", registrytest.Image())
			recA, recB := record(regA, bearerChallenge(authHost)), record(regB, bearerChallenge(authHost))

			// 2 checks on 2 sources ask auth.test for a token at the same time: neither waits.
			checkedA := run(func() { check(client, hostA, "app:v1") })
			checkedB := run(func() { check(client, hostB, "app:v1") })
			Eventually(tokenRec.count).Should(Equal(2))
			Eventually(clk.Waiters).Should(Equal(2))
			clk.Step(interval)
			Eventually(checkedA).Should(BeClosed())
			Eventually(checkedB).Should(BeClosed())

			Expect(tokenRec.offsets(start)).To(Equal([]time.Duration{0, 0}))
			Expect(recA.paths()).To(Equal([]string{pingPath, appHead}))
			Expect(recA.offsets(start)).To(Equal([]time.Duration{0, interval}))
			Expect(recB.offsets(start)).To(Equal([]time.Duration{0, interval}))
		})

		It("does not pace the requests to a mirror destination", func() {
			// The destination is a repository of the source host: only the source side waits.
			reg, host := serve("registry-a.test")
			reg.PushIndex("app:v1", registrytest.Index())
			rec := record(reg, nil)
			sourceRequests := func() int { return len(rec.matching("/v2/app/")) }
			dst := Endpoint{Reference: host + "/mirror/app"}

			released := drive(sourceRequests, run(func() {
				copyTo(host, "app:v1", dst)
				_, err := client.CheckDestination(opCtx, Endpoint{Reference: host + "/mirror/app:" + copyTag})
				Expect(err).NotTo(HaveOccurred())
				_, err = client.ListTags(opCtx, dst)
				Expect(err).NotTo(HaveOccurred())
			}))

			Expect(rec.matching("/v2/mirror/app/")).NotTo(BeEmpty())
			// The source ping goes at once; every other source request waits its slot.
			Expect(released).To(Equal(sourceRequests()))
			expectSpaced(rec.matching("/v2/app/"))
		})

		It("does not pace the requests to another host", func() {
			regA, hostA := serve("registry-a.test")
			regB, hostB := serve("registry-b.test")
			regA.Push("app:v1", registrytest.Image())
			regB.Push("app:v1", registrytest.Image())
			recA, recB := record(regA, nil), record(regB, nil)

			checkedA := run(func() { check(client, hostA, "app:v1") })
			Eventually(clk.Waiters).Should(Equal(1))
			checkedB := run(func() { check(client, hostB, "app:v1") })
			// B's ping goes while A's HEAD waits, and B's HEAD waits on B alone.
			Eventually(recB.count).Should(Equal(1))
			Eventually(clk.Waiters).Should(Equal(2))
			clk.Step(interval)
			Eventually(checkedA).Should(BeClosed())
			Eventually(checkedB).Should(BeClosed())

			Expect(recA.offsets(start)).To(Equal([]time.Duration{0, interval}))
			Expect(recB.offsets(start)).To(Equal([]time.Duration{0, interval}))
		})

		It("fails a check whose wait outlasts check.timeout, without sending the request or counting it in kuik_registry_requests_total", func() {
			setInterval(time.Hour)
			reg, host := serve("registry-a.test")
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)
			done := run(func() { check(client, host, "app:v1") })
			Eventually(clk.HasWaiters).Should(BeTrue())
			clk.Step(time.Hour)
			Eventually(done).Should(BeClosed())
			sent := rec.count()

			bounded, cancel := WithTimeout(opCtx, 200*time.Millisecond)
			defer cancel()
			_, err := client.Check(bounded, Endpoint{Reference: host + "/app:v1"})

			Expect(checkReason(err)).To(Equal(kuikv1alpha1.CheckUnreachable))
			Expect(rec.count()).To(Equal(sent))
			Expect(countedFor(host)).To(Equal(1.0))
		})

		It("fails a copy whose wait outlasts copy.timeout, without sending the request or counting it in kuik_registry_requests_total", func() {
			setInterval(time.Hour)
			src, host := serve("registry-a.test")
			src.Push("app:v1", registrytest.Image())
			rec := record(src, nil)
			dst := newRegistry()
			done := run(func() {
				_, err := client.CheckCopySource(opCtx, Endpoint{Reference: host + "/app:v1"})
				Expect(err).NotTo(HaveOccurred())
			})
			Eventually(clk.HasWaiters).Should(BeTrue())
			clk.Step(time.Hour)
			Eventually(done).Should(BeClosed())
			sent := rec.count()
			dst.Reset()

			bounded, cancel := WithTimeout(opCtx, 200*time.Millisecond)
			defer cancel()
			_, err := client.Copy(bounded, Endpoint{Reference: host + "/app:v1"}, endpoint(dst, "mirror/app"), []string{copyTag})

			Expect(copyReason(err)).To(Equal(kuikv1alpha1.CopySourceUnreachable))
			Expect(rec.count()).To(Equal(sent))
			Expect(dst.Requests("", "")).To(BeEmpty())
			Expect(countedFor(host)).To(Equal(1.0))
		})
	})

	Context("with requestInterval 0", func() {
		It("sends every request at once", func() {
			regA, hostA := serve("registry-a.test")
			regB, hostB := serve("registry-b.test")
			regA.Push("app:v1", registrytest.Image())
			regB.Push("app:v1", registrytest.Image())
			recA, recB := record(regA, nil), record(regB, nil)
			intervalsMu.Lock()
			intervals[hostA] = 0
			intervalsMu.Unlock()

			check(client, hostA, "app:v1")
			check(client, hostA, "app:v1")

			Expect(recA.offsets(start)).To(Equal([]time.Duration{0, 0, 0, 0}))

			By("still pacing a host with an interval")
			done := run(func() { check(client, hostB, "app:v1") })
			releaseAfter(interval, recB.count)
			Eventually(done).Should(BeClosed())
		})
	})

	Context("on a reload", func() {
		It("spaces the next request by the new interval from the last request sent", func() {
			setInterval(10 * time.Second)
			reg, host := serve("registry-a.test")
			reg.Push("first:v1", registrytest.Image())
			reg.Push("second:v1", registrytest.Image())
			rec := record(reg, nil)

			first := run(func() { check(client, host, "first:v1") })
			Eventually(queued(host)).Should(Equal(1))
			second := run(func() { check(client, host, "second:v1") })
			Eventually(queued(host)).Should(Equal(2))
			reload(3 * time.Second)

			// The HEAD of the first check, already waiting, goes 3s after its ping, not 10s.
			releaseAfter(3*time.Second, rec.count)
			releaseAfter(3*time.Second, rec.count)
			releaseAfter(3*time.Second, rec.count)
			Eventually(first).Should(BeClosed())
			Eventually(second).Should(BeClosed())

			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, 3 * time.Second, 6 * time.Second, 9 * time.Second}))
			Expect(rec.paths()).To(Equal([]string{pingPath, firstHead, pingPath, secondHead}))
		})

		It("applies a reload that lands while the waiting request reads its interval", func() {
			setInterval(10 * time.Second)
			reg, host := serve("registry-a.test")
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)

			done := run(func() { check(client, host, "app:v1") })
			// The ping went, the HEAD waits 10s.
			Eventually(clk.Waiters).Should(Equal(1))
			// When the HEAD reads its interval again, a reload to 3s lands right after the
			// read, before the HEAD waits once more.
			intervalsMu.Lock()
			onNextRead = func() { reload(3 * time.Second) }
			intervalsMu.Unlock()
			pacer.Reload()

			releaseAfter(3*time.Second, rec.count)
			Eventually(done).Should(BeClosed())

			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, 3 * time.Second}))
		})

		It("sends the requests waiting and those that ask after the interval drops to 0 at once", func() {
			setInterval(10 * time.Second)
			reg, host := serve("registry-a.test")
			reg.Push("first:v1", registrytest.Image())
			reg.Push("second:v1", registrytest.Image())
			reg.Push("third:v1", registrytest.Image())
			rec := record(reg, nil)

			first := run(func() { check(client, host, "first:v1") })
			Eventually(queued(host)).Should(Equal(1))
			second := run(func() { check(client, host, "second:v1") })
			Eventually(queued(host)).Should(Equal(2))
			reload(0)
			Eventually(first).Should(BeClosed())
			Eventually(second).Should(BeClosed())
			check(client, host, "third:v1")

			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, 0, 0, 0, 0, 0}))
			Expect(clk.Waiters()).To(BeZero())
		})
	})

	Context("on the webhook's active check", func() {
		It("sends every request at once, whatever the requestInterval of the host", func() {
			reg, host := serve("registry-a.test")
			reg.Push("app:v1", registrytest.Image())
			rec := record(reg, nil)
			reconciling := run(func() { check(client, host, "app:v1") })
			Eventually(clk.Waiters).Should(Equal(1))

			// The webhook's client has no pacer, whatever the reconciler's waits on.
			webhook := newClient(clientOptions{dial: testDial(&dockerHub)})
			webhook.SetTLS(tlsHosts)
			bounded, cancel := WithTimeout(WithoutRetries(opCtx), 5*time.Second)
			defer cancel()
			_, err := webhook.Check(bounded, Endpoint{Reference: host + "/app:v1"})

			Expect(err).NotTo(HaveOccurred())
			Expect(rec.offsets(start)).To(Equal([]time.Duration{0, 0, 0}))
			Expect(clk.Waiters()).To(Equal(1))
			releaseAfter(interval, rec.count)
			Eventually(reconciling).Should(BeClosed())
		})
	})
})
