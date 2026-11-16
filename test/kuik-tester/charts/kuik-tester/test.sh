#!/bin/sh
# Render-level tests of the Helm chart. POSIX sh; needs only helm and grep.
set -u

CHART=$(dirname "$0")
fail=0

render() { helm template t "$CHART" --set image.repository=x,image.tag=y "$@"; }

bad() {
	echo "FAIL [$CASE]: $1" >&2
	fail=1
}

# has <text> <pattern>: the fixed pattern appears in text.
has() { printf '%s\n' "$1" | grep -F -q -e "$2" || bad "missing: $2"; }
# lacks <text> <pattern>: the fixed pattern does not appear in text.
lacks() { printf '%s\n' "$1" | grep -F -q -e "$2" && bad "unexpected: $2"; return 0; }
# hasline <text> <line>: the line, leading spaces stripped, appears exactly.
hasline() { printf '%s\n' "$1" | sed 's/^ *//' | grep -F -x -q -e "$2" || bad "missing line: $2"; }
# count <text> <pattern> <n>: the fixed pattern appears on exactly n lines.
count() {
	n=$(printf '%s\n' "$1" | grep -F -c -e "$2")
	[ "$n" -eq "$3" ] || bad "expected $3 x '$2', got $n"
}

CASE="1 defaults"
out=$(render) || bad "render failed"
count "$out" "kind: Ingress" 1
count "$out" "kind: Service" 3
count "$out" "kind: Deployment" 2
count "$out" "kind: ConfigMap" 2
count "$out" "kind: PersistentVolumeClaim" 2
count "$out" "kind: NetworkPolicy" 0
has "$out" 'host: "*.kt.example.com"'
lacks "$out" 'admin.kt.example.com'
count "$out" 'backend: "http://t-kuik-tester-zot:5000"' 4
has "$out" 'name: "mirror-*"'
has "$out" 'name: "nodelete-*"'
has "$out" 'statePath: /var/lib/kuik-tester/state.json'
has "$out" 'fsGroup: 65532'
has "$out" 'runAsNonRoot: true'
has "$out" 'checksum/config:'
has "$out" 'trustedProxies: []'
has "$out" 'expireAfter: "7d"'
reg=$(render --show-only templates/ingress-registry.yaml) || bad "show-only failed"
has "$reg" 't-kuik-tester-registry'
lacks "$reg" 't-kuik-tester-admin'

CASE="1b admin ingress opted in"
out=$(render --set ingress.admin.enabled=true) || bad "render failed"
count "$out" "kind: Ingress" 2
has "$out" 'host: admin.kt.example.com'
adm=$(render --set ingress.admin.enabled=true --show-only templates/ingress-admin.yaml) || bad "show-only failed"
has "$adm" 't-kuik-tester-admin'

CASE="2 zot disabled with explicit backends"
out=$(render --set zot.enabled=false --set 'proxies[0].backend=http://z:5000' --set 'proxies[1].backend=http://z:5000') || bad "render failed"
lacks "$out" 't-kuik-tester-zot'
count "$out" 'backend: "http://z:5000"' 2

CASE="3 zot disabled, empty backend"
err=$(render --set zot.enabled=false 2>&1 >/dev/null) && bad "helm template should fail"
has "$err" 'proxies[0].backend is required when zot.enabled is false'

CASE="4 default image"
out=$(helm template t "$CHART" --show-only templates/deployment.yaml) || bad "render failed"
has "$out" 'image: "quay.io/enix/kuik-tester:0.0.0"'
out=$(helm template t "$CHART" --set image.repository=r.example/kt,image.tag=9 --show-only templates/deployment.yaml) || bad "render failed"
has "$out" 'image: "r.example/kt:9"'

CASE="5 plain HTTP"
out=$(render --set plainHTTP.enabled=true) || bad "render failed"
count "$out" "kind: Service" 4
has "$out" 'type: LoadBalancer'
has "$out" 'plainHTTPAddr: ":5000"'
has "$out" 'plainHTTPHost: "http"'

CASE="5b plain HTTP on a fixed node port"
svc=$(render --set plainHTTP.enabled=true,plainHTTP.service.type=NodePort,plainHTTP.service.nodePort=30500 --show-only templates/service-plainhttp.yaml) || bad "render failed"
has "$svc" 'type: NodePort'
has "$svc" 'nodePort: 30500'
svc=$(render --set plainHTTP.enabled=true,plainHTTP.service.type=NodePort --show-only templates/service-plainhttp.yaml) || bad "render failed"
lacks "$svc" 'nodePort:'
err=$(render --set plainHTTP.enabled=true,plainHTTP.service.type=ClusterIP,plainHTTP.service.nodePort=30500 2>&1 >/dev/null) && bad "helm template should fail"
has "$err" 'plainHTTP.service.nodePort needs plainHTTP.service.type NodePort or LoadBalancer'

CASE="6 TLS secret"
out=$(render --set tls.secretName=kt-tls,ingress.registry.enabled=false) || bad "render failed"
has "$out" 'secretName: kt-tls'
has "$out" 'tlsCertFile: /etc/kuik-tester-tls/tls.crt'
has "$out" 'tlsKeyFile: /etc/kuik-tester-tls/tls.key'

CASE="7 persistence off"
out=$(render --set persistence.enabled=false) || bad "render failed"
lacks "$out" 'statePath'
lacks "$out" 'claimName: t-kuik-tester-state'
lacks "$out" 'mountPath: /var/lib/kuik-tester'
count "$out" "kind: PersistentVolumeClaim" 1

CASE="8 ingresses off"
out=$(render --set ingress.registry.enabled=false,ingress.admin.enabled=false) || bad "render failed"
count "$out" "kind: Ingress" 0
out=$(render --set ingress.registry.enabled=false,ingress.admin.enabled=true) || bad "render failed"
count "$out" "kind: Ingress" 1
has "$out" 'host: admin.kt.example.com'
lacks "$out" 'host: "*.kt.example.com"'

CASE="11 TLS secret with the registry Ingress"
err=$(render --set tls.secretName=kt-tls 2>&1 >/dev/null) && bad "helm template should fail"
has "$err" 'tls.secretName cannot be combined with ingress.registry.enabled'

CASE="9 checksum follows the config"
a=$(render --set baseDomain=a.test | grep 'checksum/config:')
b=$(render --set baseDomain=b.test | grep 'checksum/config:')
[ -n "$a" ] && [ -n "$b" ] || bad "checksum/config missing"
[ "$a" != "$b" ] || bad "checksum/config unchanged by a config change"

CASE="10 zot delays from values"
out=$(render --set zot.gcDelay=1m,zot.retentionDelay=2m) || bad "render failed"
has "$out" '"gcDelay":"1m"'
has "$out" '"delay":"2m"'

CASE="12 image pull secrets"
out=$(render) || bad "render failed"
lacks "$out" 'imagePullSecrets'
out=$(render --set 'image.pullSecrets={regcred,other}' --show-only templates/deployment.yaml) || bad "render failed"
count "$out" 'imagePullSecrets:' 1
has "$out" '- name: regcred'
has "$out" '- name: other'
zot=$(render --set 'image.pullSecrets={regcred}' --show-only templates/zot.yaml) || bad "show-only failed"
lacks "$zot" 'imagePullSecrets'

CASE="13 trusted proxies and Kubernetes floor"
out=$(render --set 'trustedProxies={10.42.0.0/16}' --show-only templates/configmap.yaml) || bad "render failed"
has "$out" '- "10.42.0.0/16"'
lacks "$out" 'trustedProxies: []'
err=$(render --kube-version 1.18.0 2>&1 >/dev/null) && bad "helm template should fail"
has "$err" 'incompatible with Kubernetes v1.18.0'

CASE="14 network policy"
np=$(render --set networkPolicy.enabled=true --show-only templates/networkpolicy.yaml) || bad "show-only failed"
count "$np" "kind: NetworkPolicy" 1
has "$np" 'port: 8080'
lacks "$np" 'port: 8081'
lacks "$np" 'port: 5000'
np=$(render --set networkPolicy.enabled=true,plainHTTP.enabled=true --show-only templates/networkpolicy.yaml) || bad "show-only failed"
has "$np" 'port: 5000'
np=$(render --set networkPolicy.enabled=true --set-json 'networkPolicy.adminFrom=[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"ingress-nginx"}}}]' --show-only templates/networkpolicy.yaml) || bad "show-only failed"
has "$np" 'port: 8081'
has "$np" 'kubernetes.io/metadata.name: ingress-nginx'
zot=$(render --set networkPolicy.enabled=true --show-only templates/zot.yaml) || bad "show-only failed"
count "$zot" 'kind: NetworkPolicy' 1
zotnp=$(printf '%s\n' "$zot" | awk 'BEGIN{d="\n"} /^---$/{if(d~/\nkind: NetworkPolicy\n/)printf "%s",d; d="\n"; next} {d=d $0 "\n"} END{if(d~/\nkind: NetworkPolicy\n/)printf "%s",d}')
# the peer (selector labels of the tester pod), not the policy's own metadata labels
zotfrom=$(printf '%s\n' "$zotnp" | sed -n '/- from:/,/ports:/p')
hasline "$zotfrom" 'app.kubernetes.io/name: kuik-tester'
has "$zotnp" 'port: 5000'

CASE="14b network policy without zot"
out=$(render --set networkPolicy.enabled=true,zot.enabled=false --set 'proxies[0].backend=http://z:5000' --set 'proxies[1].backend=http://z:5000' --set 'proxies[2].backend=http://z:5000' --set 'proxies[3].backend=http://z:5000') || bad "render failed"
count "$out" "kind: NetworkPolicy" 1

CASE="14c network policy with the admin Ingress"
err=$(render --set networkPolicy.enabled=true,ingress.admin.enabled=true 2>&1 >/dev/null) && bad "helm template should fail"
has "$err" 'networkPolicy.adminFrom must allow the Ingress controller'
render --set networkPolicy.enabled=true,ingress.admin.enabled=true --set-json 'networkPolicy.adminFrom=[{"podSelector":{}}]' >/dev/null || bad "render failed"

CASE="15 expiry"
out=$(render --set expireAfter=36h --show-only templates/configmap.yaml) || bad "render failed"
has "$out" 'expireAfter: "36h"'
lacks "$out" 'expireAfter: "7d"'
out=$(render --set expireAfter=0 --show-only templates/configmap.yaml) || bad "render failed"
has "$out" 'expireAfter: "0"'

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "chart-test: ok"
