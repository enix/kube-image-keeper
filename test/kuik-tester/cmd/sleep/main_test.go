package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// runWithin runs sleep with args and returns its exit code, failing the spec
// if it has not returned within limit.
func runWithin(limit time.Duration, args []string, sig <-chan os.Signal) int {
	GinkgoHelper()
	done := make(chan int, 1)
	go func() {
		defer GinkgoRecover()
		done <- run(args, sig)
	}()
	select {
	case rc := <-done:
		return rc
	case <-time.After(limit):
		Fail("run " + fmt.Sprint(args) + " did not return within " + limit.String())
		return -1
	}
}

var _ = Describe("run", func() {
	DescribeTable("exit code",
		func(arg string, want int) {
			Expect(runWithin(time.Second, []string{arg}, nil)).To(Equal(want))
		},
		Entry("a duration with a unit", "50ms", 0),
		Entry("zero", "0", 0),
		Entry("a fractional number of seconds", "0.01", 0),
		Entry("a word", "abc", 2),
		Entry("a negative number", "-1", 2),
		Entry("a negative duration", "-1s", 2),
		// NaN, infinities and seconds that overflow time.Duration.
		Entry("NaN", "NaN", 2),
		Entry("lowercase nan", "nan", 2),
		Entry("inf", "inf", 2),
		Entry("-Inf", "-Inf", 2),
		Entry("+Infinity", "+Infinity", 2),
		Entry("1e30 seconds", "1e30", 2),
		Entry("-1e30 seconds", "-1e30", 2),
		Entry("seconds overflowing time.Duration", "9223372037", 2),
		Entry("1e10 seconds", "1e10", 2),
	)

	It("exits on a signal", func() {
		sig := make(chan os.Signal, 1)
		sig <- syscall.SIGTERM
		Expect(runWithin(time.Second, nil, sig)).To(Equal(0))
	})

	It("sleeps until a signal when given infinity", func() {
		sig := make(chan os.Signal, 1)
		done := make(chan int, 1)
		go func() {
			defer GinkgoRecover()
			done <- run([]string{"infinity"}, sig)
		}()
		Consistently(done, 50*time.Millisecond).ShouldNot(Receive(), "run returned before any signal")
		sig <- syscall.SIGTERM
		var rc int
		Eventually(done).WithTimeout(time.Second).Should(Receive(&rc), "run did not return on SIGTERM")
		Expect(rc).To(Equal(0))
	})
})

var _ = Describe("parseDuration", func() {
	It("accepts the largest number of seconds", func() {
		d, err := parseDuration("9223372036")
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(Equal(9223372036 * time.Second))
	})
})
