//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// kuik-tester (test/kuik-tester) plays the destination registries of the mirrors, one host each:
// it checks the write credential, refuses a tag deletion on refusingRegistry with 400, as Harbor
// does (goharbor/harbor#24213), and forwards everything else to the distribution registry of the
// host, without auth. Its request log shows what kuik sent. Its admin API is reached through
// kubectl proxy; the suite never imports its Go packages, a nested module the root one does not
// require.

const (
	// testerImageRepository and testerImageTag name the kuik-tester image the suite builds and
	// loads into Kind, as testerImage. The e2e workflow builds it under the same name: keep them in
	// sync.
	testerImageRepository = "example.com/kuik-tester"
	testerImageTag        = "e2e"
	testerImage           = testerImageRepository + ":" + testerImageTag

	// taskBinary is the task the make shim installs on its first run, which the manager image
	// build always is: the shim cannot forward a goal holding a colon (tester:image).
	taskBinary = "bin/task"

	// testerRelease is the Helm release of kuik-tester in registryNamespace: its name holds the
	// chart's, so every object of the chart is named after it.
	testerRelease = "kuik-tester"
	// testerChart is the kuik-tester chart, relative to the project directory.
	testerChart = "test/kuik-tester/charts/kuik-tester"
	// testerInstallTimeout bounds the install, the --wait for the tester pod included.
	testerInstallTimeout = 5 * time.Minute
	// testerAdminPort is the port of the admin Service of the release.
	testerAdminPort = 8081
	// testerRegistryPort is the registry listener of the tester pod, which every
	// <label>.<baseDomain> host reaches.
	testerRegistryPort = 8080

	// testerRefuseTagDelete is the ID of the rule refusing tag deletion on refusingRegistry.
	testerRefuseTagDelete = "refuse-tag-delete"

	// testerKindDelete is the kind of request the specs read in the request log of kuik-tester.
	testerKindDelete = "delete"
)

// testerValues are the values of the kuik-tester release: the image the suite loaded, the hosts
// of registryNamespace, and each destination registry as a proxy host, forwarding to its backend.
// Without zot, an Ingress or a PVC: the backend holds the content, the Service of each host is
// the suite's, and the state is pushed once by the Image mirroring container: it lives in memory,
// so a restart of the tester pod loses it.
func testerValues() string {
	return fmt.Sprintf(`
image:
  repository: %s
  tag: %s
  pullPolicy: Never
baseDomain: %s.svc.cluster.local
proxies:
  - name: %[4]s
    backend: http://%[5]s:5000
  - name: %[6]s
    backend: http://%[7]s:5000
zot:
  enabled: false
ingress:
  registry:
    enabled: false
persistence:
  enabled: false
`, testerImageRepository, testerImageTag, registryNamespace, deletingRegistry.name, deletingRegistry.backend(),
		refusingRegistry.name, refusingRegistry.backend())
}

// testerState is the state of kuik-tester: the write credential of the destination registries is
// the one account allowed on each, and a DELETE naming a tag on refusingRegistry is refused with
// 400. A DELETE by digest names no tag: the rule leaves it, and it reaches the backend.
func testerState() string {
	return fmt.Sprintf(`
users:
  - name: %[1]s
    password: %[2]s
grants:
  - id: e2e-registry
    host: %[3]s
    repo: "**"
    actions: [pull, push, delete]
    users: [%[1]s]
  - id: e2e-nodelete
    host: %[4]s
    repo: "**"
    actions: [pull, push, delete]
    users: [%[1]s]
rules:
  - id: %[5]s
    enabled: true
    target:
      host: %[4]s
      kinds: [delete]
      tag: "?*"
    effects:
      status: 400
generations: {}
`, registryUser, registryPassword, deletingRegistry.name, refusingRegistry.name, testerRefuseTagDelete)
}

// installTester installs kuik-tester in registryNamespace, which must exist, and waits for its
// pod. Deleting the namespace uninstalls it: the release and every object of the chart live
// there.
func installTester() {
	GinkgoHelper()
	_, err := runBounded(testerInstallTimeout, strings.NewReader(testerValues()), "helm", "upgrade", "--install",
		testerRelease, testerChart, "-n", registryNamespace, "--wait", "--timeout", "4m", "-f", "-")
	Expect(err).NotTo(HaveOccurred(), "Failed to install kuik-tester")
}

// testerServiceManifest is the Service of the host label name: the chart routes each
// <label>.<baseDomain> host by its Host header and creates no Service per host, so the cluster
// DNS name of name reaches the registry listener of the tester pod on port 5000.
func testerServiceManifest(name string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    app.kubernetes.io/name: kuik-tester
    app.kubernetes.io/instance: %s
  ports:
    - port: 5000
      targetPort: %d
`, name, registryNamespace, testerRelease, testerRegistryPort)
}

// testerAdminURL is the URL of path under the admin API of kuik-tester, through kubectl proxy:
// startAPIProxy must have run.
func testerAdminURL(path string) string {
	return fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s-admin:%d/proxy/api/v1%s",
		apiProxy, registryNamespace, testerRelease, testerAdminPort, path)
}

// importTesterState replaces the whole state of kuik-tester with the YAML document state.
func importTesterState(state string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, testerAdminURL("/import"),
		strings.NewReader(state))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/yaml")
	resp, err := proxyClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("importing the kuik-tester state: %s: %s", resp.Status, body)
	}
	return nil
}

// testerRequest is an entry of the request log of kuik-tester, the fields the specs read.
type testerRequest struct {
	Method    string `json:"method"`
	Reference string `json:"reference"`
	Status    int    `json:"status"`
	RuleID    string `json:"ruleId"`
	Forwarded bool   `json:"forwarded"`
}

// testerRequests lists the requests of kind kuik-tester logged for repository on the host label
// host.
func testerRequests(host, kind, repository string) ([]testerRequest, error) {
	query := url.Values{"host": {host}, "kind": {kind}, "repo": {repository}}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		testerAdminURL("/requests?"+query.Encode()), nil)
	if err != nil {
		return nil, err
	}
	resp, err := proxyClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading the kuik-tester request log: %s: %s", resp.Status, body)
	}
	var requests []testerRequest
	if err := json.Unmarshal(body, &requests); err != nil {
		return nil, fmt.Errorf("decoding the kuik-tester request log: %w (%s)", err, body)
	}
	return requests, nil
}
