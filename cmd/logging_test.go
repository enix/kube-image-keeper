package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	uzap "go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/enix/kube-image-keeper/internal/info"
)

var _ = Describe("Logging", func() {
	// options returns the options a process ends up with after parsing args, writing to out.
	options := func(out *bytes.Buffer, args ...string) []zap.Opts {
		GinkgoHelper()
		opts := loggerOptions()
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		opts.BindFlags(fs)
		Expect(fs.Parse(args)).To(Succeed())
		return []zap.Opts{zap.UseFlagOptions(&opts), zap.WriteTo(out)}
	}
	// lines decodes every line of out as a JSON object.
	lines := func(out *bytes.Buffer) []map[string]any {
		GinkgoHelper()
		var decoded []map[string]any
		for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
			var entry map[string]any
			Expect(json.Unmarshal([]byte(line), &entry)).To(Succeed(), "line %q", line)
			decoded = append(decoded, entry)
		}
		return decoded
	}

	Context("by default", func() {
		var out *bytes.Buffer
		var log *uzap.Logger
		BeforeEach(func() {
			out = &bytes.Buffer{}
			log = zap.NewRaw(options(out)...)
		})

		It("writes one JSON object per line", func() {
			log.Info("Created Secret")
			log.Info("Deleted Secret")
			entries := lines(out)
			Expect(entries).To(HaveLen(2))
			Expect(entries[0]).To(HaveKeyWithValue("msg", "Created Secret"))
			Expect(entries[1]).To(HaveKeyWithValue("msg", "Deleted Secret"))
		})

		It("writes timestamps in ISO8601", func() {
			log.Info("Created Secret")
			Expect(lines(out)[0]["ts"]).To(MatchRegexp(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}(Z|[+-]\d{4})$`))
		})

		It("adds no stacktrace to an error", func() {
			log.Error("Failed to list ImageMirrors", uzap.Error(errors.New("forbidden")))
			Expect(lines(out)[0]).NotTo(HaveKey("stacktrace"))
		})

		It("adds a stacktrace to a DPanic line, without panicking", func() {
			Expect(func() { log.DPanic("Broke an invariant") }).NotTo(Panic())
			Expect(lines(out)[0]).To(HaveKey("stacktrace"))
		})
	})

	Context("with -zap-devel", func() {
		It("writes console text, for a local run", func() {
			out := &bytes.Buffer{}
			zap.NewRaw(options(out, "-zap-devel")...).Info("Created Secret")
			Expect(out.String()).To(ContainSubstring("Created Secret"))
			Expect(json.Valid(bytes.TrimSpace(out.Bytes()))).To(BeFalse())
		})
	})

	Context("at start-up", func() {
		It("logs the version, revision, build date and Go version on the Starting manager line", func() {
			version, revision, built := info.Version, info.Revision, info.BuildDateTime
			DeferCleanup(func() { info.Version, info.Revision, info.BuildDateTime = version, revision, built })
			info.Version, info.Revision, info.BuildDateTime = "3.0.0-alpha.3", "252a096", "2026-10-06T09:00:00Z"

			out := &bytes.Buffer{}
			logStarting(zap.New(options(out)...), webhookProcess)
			entries := lines(out)
			Expect(entries).To(HaveLen(1))
			Expect(entries[0]).To(HaveKeyWithValue("msg", "Starting manager"))
			Expect(entries[0]).To(HaveKeyWithValue("process", "webhook"))
			Expect(entries[0]).To(HaveKeyWithValue("version", "3.0.0-alpha.3"))
			Expect(entries[0]).To(HaveKeyWithValue("revision", "252a096"))
			Expect(entries[0]).To(HaveKeyWithValue("built", "2026-10-06T09:00:00Z"))
			Expect(entries[0]).To(HaveKeyWithValue("goversion", runtime.Version()))
		})
	})
})
