// Command sleep is the entrypoint of every synthetic image. With no argument
// it blocks until SIGTERM or SIGINT, then exits 0, so test pods stop at once.
// With an argument (a duration in Go syntax, e.g. "10s", a number of seconds,
// e.g. "0.5", or "infinity") it exits 0 after that time, or on a signal,
// whichever comes first: an initContainer on a synthetic image can then
// complete. "infinity" behaves like no argument.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	os.Exit(run(os.Args[1:], c))
}

// run returns the exit code: 0 after the duration or on a signal, 2 on an
// unparsable argument.
func run(args []string, sig <-chan os.Signal) int {
	var timer <-chan time.Time
	if len(args) > 0 && args[0] != "infinity" {
		d, err := parseDuration(args[0])
		if err != nil || d < 0 {
			fmt.Fprintf(os.Stderr, "sleep: invalid duration %q\n", args[0])
			return 2
		}
		timer = time.After(d)
	}
	select {
	case <-sig:
	case <-timer:
	}
	return 0
}

// maxSeconds is the most seconds a time.Duration holds; beyond it the
// float to int conversion is implementation-defined (it differs between
// architectures), so it is refused.
const maxSeconds = float64(math.MaxInt64 / int64(time.Second))

// parseDuration reads a number of seconds, else a Go duration. NaN, an
// infinity and a number of seconds that overflows time.Duration are errors.
func parseDuration(s string) (time.Duration, error) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if math.IsNaN(f) || math.Abs(f) > maxSeconds {
			return 0, errors.New("out of range")
		}
		return time.Duration(f * float64(time.Second)), nil
	}
	return time.ParseDuration(s)
}
