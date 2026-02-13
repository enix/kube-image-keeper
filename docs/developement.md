# 🔧 Development

```bash
# generate CRDs definitions from go code and install them on the cluster you're connected to
make install
# run the manager locally against the cluster you're connected to and export metrics to :8080
make run
```

## Makefile options

The way kuik is run using the Makefile can be configured through environment variables:

- `RUN_FLAG_DEVEL`: sets the `-zap-devel` flag, defaults to `true`
- `RUN_FLAG_LOG_LEVEL`: sets the `-zap-log-level` flag if present
- `RUN_FLAG_ZAP_ENCODER`: sets the `-zap-encoder` flag if present
- `METRICS_PORT`: sets the port to bind for the metrics, defaults to `8080`
- `RUN_ADDITIONAL_ARGS`: add any additional argument to the `go run ./cmd/main.go` command (you can even `| grep` here)
- `RUN_ARGS`: default arguments to the `go run ./cmd/main.go` command, it combines all previous variables together. Don't touch it if you don't need to.

I highly suggest that you try [github.com/pamburus/hl](https://github.com/pamburus/hl), an awesome tool to make json logs human readable. It can be setup with kuik like this:

```bash
export RUN_FLAG_ZAP_ENCODER=json RUN_ADDITIONAL_ARGS="2>&1 | hl --paging=never"
make run
```

## Local webhook for remote cluster

There are several ways of developing a webhook for kubernetes and depending on your situation you may prefer one over another. One of them consist in running your webhook locally (using `make run` command) and expose it as a service in your kubernetes cluster using a tool like [https://github.com/omrikiei/ktunnel](omrikiei/ktunnel) for instance. Since `MutatingWebhookConfiguration` requires a certificate for authentication, you will need to create one using cert-manager.

You will need:

- To [install ktunnel](https://github.com/omrikiei/ktunnel#installation)
- To install [cert-manager](https://cert-manager.io/docs/installation/)
- To create a tunnel with ktunnel (see script below)
- To issue a `Certificate` for your mutating webhook
- To copy this certificate locally for you dev instance of kuik to use it
- To create a `MutatingWebhookConfiguration` using the service that ktunnel create for you

Here is a helper script that reads your mutating webhook certificate and place it somewhere kuik will find. It also create a service and setup the tunnel using ktunnel:

```bash
#!/bin/bash

NAMESPACE=kuik-system
SECRET=webhook-server-cert
SERVICE=webhook-service
PORTMAP=9443:9443

kubectl -n "$NAMESPACE" get secret "$SECRET" -o jsonpath="{.data['tls\.key']}" | base64 --decode > tls.key
kubectl -n "$NAMESPACE" get secret "$SECRET" -o jsonpath="{.data['tls\.crt']}" | base64 --decode > tls.crt

mkdir -p /tmp/k8s-webhook-server/serving-certs
mv tls.* /tmp/k8s-webhook-server/serving-certs/

kubectl tunnel expose -n "$NAMESPACE" "$SERVICE" "$PORTMAP" -r
```

And here is the `MutatingWebhookConfiguration` with required `CertificateRequest` et `CertificateIssuer`:

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  annotations:
    cert-manager.io/inject-ca-from: kuik-system/webhook-server-cert
  name: mutating-webhook-configuration
webhooks:
- admissionReviewVersions:
  - v1
  clientConfig:
    service:
      name: webhook-service
      namespace: kuik-system
      path: /mutate--v1-pod
      port: 9443
  failurePolicy: Ignore
  reinvocationPolicy: IfNeeded
  name: mpod-v1.kb.io
  rules:
  - apiGroups:
    - ""
    apiVersions:
    - v1
    operations:
    - CREATE
    - UPDATE
    resources:
    - pods
  sideEffects: None

---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: webhook-server-cert
  namespace: kuik-system
spec:
  dnsNames:
  - webhook-service.kuik-system.svc
  - webhook-service.kuik-system.svc.cluster.local
  issuerRef:
    kind: Issuer
    name: selfsigned-issuer
  secretName: webhook-server-cert

---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned-issuer
  namespace: kuik-system
spec:
  selfSigned: {}
```
