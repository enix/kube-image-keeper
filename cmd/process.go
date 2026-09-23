package main

import (
	"fmt"
	"strings"
)

// A process is one of the three kuik runs, named by the subcommand that starts it. They
// are split by privilege and availability profile rather than by custom resource, they
// exchange nothing directly, and each one is deployed on its own. See
// docs/v3/architecture.md.
type process struct {
	// name is the subcommand that starts the process.
	name string
	// summary is the one-line description printed by the usage.
	summary string

	// servesWebhook registers the pod webhook and starts a server for it.
	servesWebhook bool
	// runsReconcilers registers the reconcilers of the three kinds.
	runsReconcilers bool
	// runsSecretSyncer runs the pull secret syncer.
	runsSecretSyncer bool

	// leaderElectionID names the lease the process competes for, empty when it elects
	// nothing. Every elected process gets its own: the reconciler and the syncer are
	// separate because they fail differently, and a shared lease would make one stand
	// down for the other.
	leaderElectionID string
}

// The leader election of the elected processes is done here, so its permissions are declared
// here: the lease lock gets, creates and updates the lease, and the elector records events.
// The syncer holds nothing else yet: its Secret writes ship with its loop, together with the
// ValidatingAdmissionPolicy that bounds them.
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;create;update,namespace=kuik-system,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch,roleName=reconciler
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;create;update,namespace=kuik-system,roleName=secret-syncer
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch,roleName=secret-syncer

// processes lists the three, in the order the usage prints them.
var processes = []process{
	{
		name:          "webhook",
		summary:       "Answer admission requests: route the images of the pods being created",
		servesWebhook: true,
		// No lease: the webhook writes nothing and scales with the API server's
		// traffic, so every replica answers and losing one changes nothing.
	},
	{
		name:             "reconciler",
		summary:          "Run the mirror, monitor and status loops",
		runsReconcilers:  true,
		leaderElectionID: "reconciler.kuik.enix.io",
	},
	{
		name:             "secret-syncer",
		summary:          "Materialise and renew the injected pull secrets",
		runsSecretSyncer: true,
		leaderElectionID: "secret-syncer.kuik.enix.io",
	},
}

// parseProcess reads the subcommand.
func parseProcess(name string) (process, error) {
	for _, p := range processes {
		if p.name == name {
			return p, nil
		}
	}

	return process{}, fmt.Errorf("unknown process %q, expected one of %s", name, strings.Join(processNames(), ", "))
}

// processNames lists the subcommands, for the usage and for error messages.
func processNames() []string {
	names := make([]string, 0, len(processes))
	for _, p := range processes {
		names = append(names, p.name)
	}

	return names
}

// usesLeaderElection reports whether the process may elect a leader.
func (p process) usesLeaderElection() bool {
	return p.leaderElectionID != ""
}

// usage lists the subcommands, printed when none is given or one is not recognised.
func usage(command string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "kuik runs as three separate processes. Start one of them:\n\n")
	for _, p := range processes {
		fmt.Fprintf(&b, "  %s %-14s %s\n", command, p.name, p.summary)
	}
	fmt.Fprintf(&b, "\nRun %s <process> -h for the flags of a process.\n", command)

	return b.String()
}
