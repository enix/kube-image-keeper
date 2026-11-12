package migration

import (
	"fmt"
	"io"
	"strings"

	"github.com/enix/kube-image-keeper/internal/migration/inventory"
)

// printer writes lines to an io.Writer; the run reports a failed write nowhere else.
type printer struct{ w io.Writer }

func (p printer) line(indent int, format string, args ...any) {
	_, _ = fmt.Fprintf(p.w, strings.Repeat("  ", indent)+format+"\n", args...)
}

// printInventory prints what the run found of v2 and v3.
func printInventory(w io.Writer, inv *inventory.Inventory) {
	p := printer{w}
	printV2(p, inv)
	printPods(p, inv.Pods)
	p.line(0, "v2 pull secrets: %d", len(inv.PullSecrets))
	for _, s := range inv.PullSecrets {
		p.line(1, "%s/%s (copied for %s %s)", s.Namespace, s.Name, s.Owner.Kind, s.Owner.Name)
	}
	printV3(p, inv.V3)
}

func printV2(p printer, inv *inventory.Inventory) {
	v2 := inv.V2
	p.line(0, "kuik v2")
	if v2.Running() {
		p.line(1, "manager: Deployment %s/%s, version %s", v2.Namespace, v2.Deployment, v2.Version)
	} else {
		p.line(1, "manager: not running")
	}
	if v2.WebhookConfiguration != "" {
		p.line(1, "webhook configuration: %s", v2.WebhookConfiguration)
	} else {
		p.line(1, "webhook configuration: none")
	}
	crds := make([]string, 0, len(inv.V2CRDs))
	for _, c := range inv.V2CRDs {
		name := c.Name
		if c.Prerelease {
			name += " (2.0 prerelease)"
		}
		crds = append(crds, name)
	}
	if len(crds) == 0 {
		crds = append(crds, "none")
	}
	p.line(1, "CRDs: %s", strings.Join(crds, ", "))

	p.line(0, "v2 resources: %d", len(inv.V2Resources))
	for _, r := range inv.V2Resources {
		name := r.Name
		if r.Namespace != "" {
			name = r.Namespace + "/" + r.Name
		}
		if len(r.Finalizers) > 0 {
			p.line(1, "%s %s (finalizers: %s)", r.Kind, name, strings.Join(r.Finalizers, ", "))
		} else {
			p.line(1, "%s %s", r.Kind, name)
		}
	}
}

func printPods(p printer, pods []inventory.Pod) {
	p.line(0, "Pods rewritten by v2 or using a v2 pull secret: %d", len(pods))
	for _, pod := range pods {
		owner := "no owner"
		if pod.Owner != nil {
			owner = pod.Owner.Kind + " " + pod.Owner.Name
		}
		p.line(1, "%s/%s (%s)", pod.Namespace, pod.Name, owner)
		for _, c := range pod.Containers {
			name := c.Name
			if c.Init {
				name = "init container " + c.Name
			}
			switch c.OriginFrom {
			case inventory.FromAnnotation:
				p.line(2, "%s: %s, origin %s", name, c.Image, c.Origin)
			case inventory.FromTemplate:
				p.line(2, "%s: %s, origin %s (from the owner's template)", name, c.Image, c.Origin)
			default:
				p.line(2, "%s: %s, origin unknown", name, c.Image)
			}
			if c.TemplateOrigin != "" {
				p.line(3, "warning: the owner's template runs %s, not the origin v2 recorded", c.TemplateOrigin)
			}
		}
		if len(pod.PullSecrets) > 0 {
			p.line(2, "v2 pull secrets: %s", strings.Join(pod.PullSecrets, ", "))
		}
	}
}

func printV3(p printer, v3 inventory.V3Installation) {
	p.line(0, "kuik v3 in namespace %s", v3.Namespace)
	switch {
	case v3.ClusterID != "":
		p.line(1, "cluster ID: %s", v3.ClusterID)
	case v3.ClusterIDError != "":
		p.line(1, "cluster ID: invalid, %s", v3.ClusterIDError)
	default:
		p.line(1, "cluster ID: not set")
	}
	for _, mode := range []struct {
		name string
		mode inventory.Mode
	}{{"webhook", v3.Webhook}, {"reconciler", v3.Reconciler}, {"secret-syncer", v3.SecretSyncer}} {
		switch {
		case mode.mode.Deployment == "":
			p.line(1, "%s: not deployed", mode.name)
		case mode.mode.Ready:
			p.line(1, "%s: running in Deployment %s", mode.name, mode.mode.Deployment)
		default:
			p.line(1, "%s: Deployment %s, no pod ready", mode.name, mode.mode.Deployment)
		}
	}
	if v3.Combined() {
		p.line(1, "several modes run combined in one Deployment")
	}
	if v3.WebhookConfiguration != "" {
		p.line(1, "webhook configuration: %s", v3.WebhookConfiguration)
	} else {
		p.line(1, "webhook configuration: none")
	}
	for _, r := range v3.Resources {
		ready := string(r.Ready)
		if ready == "" {
			ready = "not reported"
		}
		line := fmt.Sprintf("%s %s: Ready %s", r.Kind, r.Name, ready)
		if r.Reason != "" {
			line += " (" + r.Reason + ")"
		}
		if r.Message != "" {
			line += ": " + r.Message
		}
		if r.MissingImages {
			line += "; its destination misses images"
		}
		p.line(1, "%s", line)
	}
}
