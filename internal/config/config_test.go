package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// specExample is the global config of docs/v3/spec.md, "Global config", comments dropped.
const specExample = `
clusterID: cluster-a
metrics:
  copyDuration: false
mirror:
  destinationScan:
    interval: 1h
webhook:
  demoteMirrorWithPullPolicyAlways: true
  availabilityCheck:
    timeout: 2s
    activeCheckCache:
      ttl: 10s
    demoteKnownFailures: true
registries:
  default:
    check:
      interval: 1m
      timeout: 10s
    copy:
      interval: 3m
      timeout: 0
  private-registry.tld:
    copy:
      interval: 30s
  docker.io:
    copy:
      interval: 10m
  public.ecr.aws:
    check:
      interval: 5m
fallbackAuth:
- repositoryGroup: private-registry.tld/project1
  secretRef:
    name: project1-creds
- repositoryGroup: private-registry.tld/project2
  secretRef:
    name: project2-creds
- repositoryGroup: 123456.dkr.ecr.eu-west-3.amazonaws.com/acme
  provider:
    name: aws
    serviceAccountRef:
      name: kuik-ecr-access
- repositoryGroup: docker.io
  secretRef:
    name: dockerhub-creds
`

// minimal carries the one key the spec requires.
const minimal = "clusterID: cluster-a\n"

// writeFile writes content to a config file in a fresh directory and returns its path.
func writeFile(content string) string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

func mustLoad(content string) *Config {
	GinkgoHelper()
	cfg, err := Load(writeFile(content))
	Expect(err).NotTo(HaveOccurred())
	return cfg
}

var _ = Describe("Load", func() {
	It("accepts the full example of the spec", func() {
		cfg := mustLoad(specExample)
		Expect(cfg.ClusterID).To(Equal("cluster-a"))
		Expect(cfg.FallbackAuth).To(HaveLen(4))
		Expect(cfg.FallbackAuth[2].Provider.ServiceAccountRef.Name).To(Equal("kuik-ecr-access"))
		Expect(cfg.Registries.For("public.ecr.aws").Check.Interval).To(Equal(5 * time.Minute))
	})

	It("applies the defaults the spec states to a file carrying only clusterID", func() {
		cfg := mustLoad(minimal)
		Expect(cfg.Metrics.CopyDuration).To(BeFalse())
		Expect(cfg.Mirror.DestinationScan.Interval.Duration).To(Equal(time.Hour))
		Expect(cfg.Webhook.DemoteMirrorWithPullPolicyAlways).To(BeTrue())
		Expect(cfg.Webhook.AvailabilityCheck.Timeout.Duration).To(Equal(2 * time.Second))
		Expect(cfg.Webhook.AvailabilityCheck.ActiveCheckCache.TTL.Duration).To(Equal(10 * time.Second))
		Expect(cfg.Webhook.AvailabilityCheck.DemoteKnownFailures).To(BeTrue())
		Expect(cfg.Registries.For("quay.io")).To(Equal(Pacing{
			Check: ResolvedWindow{Interval: time.Minute, Timeout: 10 * time.Second},
			Copy:  ResolvedWindow{Interval: 3 * time.Minute, Timeout: 0},
		}))
		Expect(cfg.FallbackAuth).To(BeEmpty())
	})

	It("accepts a fallbackAuth list mixing repository and repositoryGroup entries", func() {
		cfg := mustLoad(minimal + `
fallbackAuth:
- repository: quay.io/acme/foo
  secretRef:
    name: foo-creds
- repositoryGroup: quay.io
  secretRef:
    name: quay-creds
`)
		Expect(cfg.FallbackAuth).To(HaveLen(2))
	})

	DescribeTable("rejects the whole file",
		func(content string) {
			cfg, err := Load(writeFile(content))
			Expect(err).To(HaveOccurred())
			Expect(cfg).To(BeNil())
		},
		Entry("when it is not YAML", "clusterID: [cluster-a\n"),
		Entry("when clusterID is missing", "metrics:\n  copyDuration: true\n"),
		Entry("when clusterID is outside ^[a-zA-Z0-9][a-zA-Z0-9.-]*$", "clusterID: cluster_a\n"),
		Entry("when a key the spec does not define appears, at any level", minimal+`
registries:
  docker.io:
    copy:
      interval: 10m
      maxPerInterval: 2
`),
		Entry("when a fallbackAuth entry carries insecure", minimal+`
fallbackAuth:
- repositoryGroup: registry.local:5000
  insecure: true
  secretRef:
    name: local-creds
`),
		Entry("when a fallbackAuth entry carries both repository and repositoryGroup, or neither", minimal+`
fallbackAuth:
- repository: quay.io/acme/foo
  repositoryGroup: quay.io/acme
  secretRef:
    name: foo-creds
- secretRef:
    name: bar-creds
`),
		Entry("when a fallbackAuth entry carries both secretRef and provider, or neither", minimal+`
fallbackAuth:
- repositoryGroup: quay.io/acme
  secretRef:
    name: acme-creds
  provider:
    name: aws
- repositoryGroup: quay.io/other
`),
		Entry("when a fallbackAuth path carries a tag or a digest, or is not fully qualified", minimal+`
fallbackAuth:
- repository: quay.io/acme/foo:v1
  secretRef:
    name: foo-creds
- repository: quay.io/acme/bar@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
  secretRef:
    name: bar-creds
- repository: library/nginx
  secretRef:
    name: nginx-creds
`),
		Entry("when a fallbackAuth provider is not aws, gcp or azure", minimal+`
fallbackAuth:
- repositoryGroup: quay.io/acme
  provider:
    name: digitalocean
`),
		Entry("when an interval is below 5s", minimal+`
registries:
  quay.io:
    check:
      interval: 4s
`),
		Entry("when a timeout other than copy.timeout is not positive", minimal+`
registries:
  default:
    check:
      timeout: 0
`),
	)
})

var _ = Describe("Registries.For", func() {
	It("inherits from default every field a host block does not name", func() {
		cfg := mustLoad(specExample)
		Expect(cfg.Registries.For("private-registry.tld")).To(Equal(Pacing{
			Check: ResolvedWindow{Interval: time.Minute, Timeout: 10 * time.Second},
			Copy:  ResolvedWindow{Interval: 30 * time.Second, Timeout: 0},
		}))
	})

	It("overrides a non-zero default copy.timeout with an explicit 0", func() {
		cfg := mustLoad(minimal + `
registries:
  default:
    copy:
      timeout: 1h
  quay.io:
    copy:
      timeout: 0
`)
		Expect(cfg.Registries.For("quay.io").Copy.Timeout).To(BeZero())
		Expect(cfg.Registries.For("ghcr.io").Copy.Timeout).To(Equal(time.Hour))
	})

	It("applies default to a host without a block", func() {
		cfg := mustLoad(minimal + `
registries:
  default:
    check:
      interval: 2m
`)
		Expect(cfg.Registries.For("ghcr.io")).To(Equal(Pacing{
			Check: ResolvedWindow{Interval: 2 * time.Minute, Timeout: 10 * time.Second},
			Copy:  ResolvedWindow{Interval: 3 * time.Minute, Timeout: 0},
		}))
	})

	It("applies a docker.io block to index.docker.io", func() {
		cfg := mustLoad(specExample)
		Expect(cfg.Registries.For("index.docker.io").Copy.Interval).To(Equal(10 * time.Minute))
	})
})

// configMapMount lays out a directory the way the kubelet mounts a ConfigMap: the file is a
// symlink through `..data`, which points at a timestamped directory swapped on every update.
type configMapMount struct {
	dir     string
	version int
}

func newConfigMapMount(content string) *configMapMount {
	GinkgoHelper()
	m := &configMapMount{dir: GinkgoT().TempDir()}
	m.swap(content)
	Expect(os.Symlink(filepath.Join("..data", "config.yaml"), m.path())).To(Succeed())
	return m
}

func (m *configMapMount) path() string {
	return filepath.Join(m.dir, "config.yaml")
}

// swap writes content in a new timestamped directory and repoints `..data` at it atomically.
func (m *configMapMount) swap(content string) {
	GinkgoHelper()
	m.version++
	version := filepath.Join(m.dir, "..v"+strings.Repeat("0", m.version))
	Expect(os.Mkdir(version, 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(version, "config.yaml"), []byte(content), 0o600)).To(Succeed())
	tmp := filepath.Join(m.dir, "..data_tmp")
	Expect(os.Symlink(filepath.Base(version), tmp)).To(Succeed())
	Expect(os.Rename(tmp, filepath.Join(m.dir, "..data"))).To(Succeed())
}

var _ = Describe("Watch", func() {
	var (
		mount   *configMapMount
		changes chan *Config
	)

	// startWatch loads the mounted file and watches it until the spec ends.
	startWatch := func(content string) {
		GinkgoHelper()
		mount = newConfigMapMount(content)
		current, err := Load(mount.path())
		Expect(err).NotTo(HaveOccurred())
		changes = make(chan *Config, 10)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() {
			defer GinkgoRecover()
			Expect(Watch(ctx, mount.path(), current, func(c *Config) { changes <- c })).To(Succeed())
		}()
		// Let the watcher register before the spec rewrites the file.
		time.Sleep(100 * time.Millisecond)
	}

	It("delivers the new config when the ConfigMap mount swaps in a valid file", func() {
		startWatch(minimal)
		mount.swap(minimal + "mirror:\n  destinationScan:\n    interval: 2h\n")
		var next *Config
		Eventually(changes).Should(Receive(&next))
		Expect(next.Mirror.DestinationScan.Interval.Duration).To(Equal(2 * time.Hour))
	})

	It("keeps the previous config when the new file does not validate", func() {
		startWatch(minimal)
		mount.swap(minimal + "mirror:\n  destinationScan:\n    interval: 1s\n")
		Consistently(changes, "500ms").ShouldNot(Receive())

		By("still delivering the next valid file")
		mount.swap(minimal + "mirror:\n  destinationScan:\n    interval: 2h\n")
		Eventually(changes).Should(Receive())
	})

	It("counts a rejected reload in kuik_config_reload_errors_total", func() {
		startWatch(minimal)
		before := testutil.ToFloat64(reloadErrors)
		mount.swap("clusterID: [\n")
		Eventually(func() float64 { return testutil.ToFloat64(reloadErrors) }).Should(BeNumerically(">", before))
	})

	It("keeps clusterID and metrics until restart and applies the other keys", func() {
		startWatch(minimal)
		mount.swap("clusterID: cluster-b\nmetrics:\n  copyDuration: true\nmirror:\n  destinationScan:\n    interval: 2h\n")
		var next *Config
		Eventually(changes).Should(Receive(&next))
		Expect(next.ClusterID).To(Equal("cluster-a"))
		Expect(next.Metrics.CopyDuration).To(BeFalse())
		Expect(next.Mirror.DestinationScan.Interval.Duration).To(Equal(2 * time.Hour))
	})
})
