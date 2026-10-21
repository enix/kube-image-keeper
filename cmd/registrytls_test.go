package main

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var _ = Describe("The shared Secret cache", func() {
	It("starts on a replica that does not hold the lease", func() {
		// An API server that never answers: the replica never wins the lease, as a
		// reconciler replica waiting behind the leader.
		unreachable := &rest.Config{Host: "https://127.0.0.1:1", Timeout: time.Second}
		mgr, err := ctrl.NewManager(unreachable, ctrl.Options{
			Scheme:                  scheme,
			LeaderElection:          true,
			LeaderElectionID:        "kuik-e2e-test",
			LeaderElectionNamespace: "kuik-system",
			Metrics:                 metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress:  "0",
		})
		Expect(err).NotTo(HaveOccurred())
		secrets, err := newSecretCache(mgr, "kuik-system")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(ctx)).To(Succeed())
		}()

		// A started cache with no informer yet has nothing to sync; one never started waits.
		Eventually(func() bool {
			wait, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			defer stop()
			return secrets.WaitForCacheSync(wait)
		}).Should(BeTrue())
	})
})
