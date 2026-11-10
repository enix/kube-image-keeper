package main

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clocktesting "k8s.io/utils/clock/testing"
)

var _ = Describe("The shutdown delay", func() {
	const delay = 10 * time.Second

	var (
		clk    *clocktesting.FakeClock
		signal context.CancelFunc
		ctx    context.Context
		ready  func() error
	)

	BeforeEach(func() {
		clk = clocktesting.NewFakeClock(time.Now())
		var parent context.Context
		parent, signal = context.WithCancel(context.Background())
		DeferCleanup(signal)
		var check func(*http.Request) error
		ctx, check = delayShutdown(parent, delay, clk)
		ready = func() error { return check(nil) }
	})

	It("keeps the process running for the delay after the stop signal, then stops it", func() {
		signal()
		Eventually(clk.HasWaiters).Should(BeTrue())

		clk.Step(delay - time.Millisecond)
		Consistently(ctx.Done(), 100*time.Millisecond).ShouldNot(BeClosed())

		clk.Step(time.Millisecond)
		Eventually(ctx.Done()).Should(BeClosed())
	})

	It("is ready until the stop signal, then not ready while the process keeps running", func() {
		Expect(ready()).To(Succeed())

		signal()

		Eventually(ready).ShouldNot(Succeed())
		Expect(ctx.Err()).NotTo(HaveOccurred())
	})
})
