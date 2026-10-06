# kuik smoke test manifests

Run from this directory, with `KUBECONFIG` set to the cluster under test. Apply in order,
one file at a time, and check before moving on.

```sh
NS=kuik-system   # the kuik namespace

# 1. kuik runs
kubectl -n "$NS" get pods
kubectl -n "$NS" logs -l app.kubernetes.io/name=kube-image-keeper --prefix | grep -i error

# 2. no CR, nothing changes
kubectl apply -f 00-namespaces.yaml
kubectl apply -f 10-pod-no-cr.yaml

# 3, 4 and 5. routes when needed, ignores otherwise
kubectl apply -f 20-imagealternative.yaml
kubectl apply -f 30-pods-routing.yaml

# 6. ImageMirror, routing only
kubectl apply -f 40-imagemirror.yaml

# 7. status of kuik-test-nginx after test 3, and the event on the routed pod
kubectl get imagealternative kuik-test-nginx -o yaml
kubectl get events -n kuik-test --field-selector reason=ImageFallback

# 8 and 9. the secret syncer, without any pod
kubectl apply -f 50-pull-auth.yaml
kubectl get secret -n kuik-test kuik-inject-imagealternative-kuik-test-pull-secret -o jsonpath='{.type}'
kubectl get secret -n kuik-test-unlabeled
kubectl get imagealternatives kuik-test-pull-secret kuik-test-missing-secret -o custom-columns='NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].reason'
kubectl get events.events.k8s.io -A --field-selector reason=PullSecretInjectionFailed

# 10. the pull Secret goes with its ImageAlternative
kubectl delete imagealternative kuik-test-pull-secret
kubectl get secret -n kuik-test kuik-inject-imagealternative-kuik-test-pull-secret --ignore-not-found

# Check every test pod: image, kuik annotations, phase
kubectl get pods -n kuik-test -o custom-columns='NAME:.metadata.name,IMAGE:.spec.containers[0].image,REWRITES:.metadata.annotations.kuik\.enix\.io/rewrites,PHASE:.status.phase'
kubectl get pods -n kuik-test-unlabeled -o custom-columns='NAME:.metadata.name,IMAGE:.spec.containers[0].image,REWRITES:.metadata.annotations.kuik\.enix\.io/rewrites,PHASE:.status.phase'

# kuik_routing_rewrites_total: 1 for ImageAlternative/kuik-test-nginx, OnFailure, summed
# over the webhook pods. List them, then read each one's metrics.
for POD in $(kubectl -n "$NS" get pods -l app.kubernetes.io/component=webhook -o jsonpath='{.items[*].metadata.name}'); do
  echo "== $POD"
  kubectl get --raw "/api/v1/namespaces/$NS/pods/$POD:8080/proxy/metrics" | grep -E '^kuik_(routing|build_info)'
done

# Cleanup: the cluster-scoped CRs first, then the namespaces
kubectl delete --ignore-not-found -f 50-pull-auth.yaml -f 40-imagemirror.yaml -f 30-pods-routing.yaml -f 20-imagealternative.yaml -f 10-pod-no-cr.yaml
kubectl delete -f 00-namespaces.yaml
```

The webhook runs 2 replicas and each keeps its own counters: only the replica that served
the admission counts the rewrite, so read every webhook pod and sum.

| Pod | Namespace | Expected image | Annotation | Phase |
| --- | --------- | -------------- | ---------- | ----- |
| `no-cr` | kuik-test | unchanged (Quay) | none | Running |
| `fallback` | kuik-test | `quay.io/nginx/nginx-unprivileged:1.31.6-alpine` | `rewrites`, by `ImageAlternative/kuik-test-nginx` | Running |
| `origin-up` | kuik-test | unchanged (Quay) | none | Running |
| `out-of-scope` | kuik-test-unlabeled | unchanged (`kuik-test.invalid/...`) | none | ImagePullBackOff |
| `mirror` | kuik-test | unchanged (Quay) | none | Running |

| Resource | Expected |
| -------- | -------- |
| ImageAlternative `kuik-test-nginx` | `activeFallbacks` holds the `fallback` origin, rewritten to Quay; `FallbackActive` True; `Ready` reason `IsReady`; `ImageFallback` event on `fallback` |
| ImageAlternative `kuik-test-pull-secret` | `Ready` reason `IsReady`; Secret `kuik-inject-imagealternative-kuik-test-pull-secret` of type `kubernetes.io/dockerconfigjson` in kuik-test only, gone once the resource is deleted |
| ImageAlternative `kuik-test-missing-secret` | `Ready` reason `SecretNotFound`; `PullSecretInjectionFailed` event on the resource |

Run every kuik process at debug level for the test (see the skill, step 2). If a pod is not
rewritten as expected, the webhook logs name the candidate that failed its check and why
(`Candidate failed its check`); the secret syncer logs each pull Secret it applies.
