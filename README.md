# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yml/badge.svg?branch=release)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

kube-image-keeper (a.k.a. *kuik*, which is pronounced /kwɪk/, like "quick") is a container image caching system for Kubernetes.
It saves the container images used by your pods in its own local registry so that these images remain available if the original becomes unavailable.

## Upgrading

### From 1.6.0 o 1.7.0

***ACTION REQUIRED***

To follow Helm3 best pratices, we moved `cachedimage` and `repository` custom resources definition from the helm templates directory to the dedicated `crds` directory.
This will cause the `cachedimage` CRD to be deleted during the 1.7.0 upgrade.

We advise you to uninstall your helm release, clean the remaining custom resources by removing their finalizer, and then reinstall kuik in 1.7.0

You may also recreate the custom resource definition right after the upgrade to 1.7.0 using
```
kubectl apply -f https://raw.githubusercontent.com/enix/kube-image-keeper/main/helm/kube-image-keeper/crds/cachedimage-crd.yaml
kubectl apply -f https://raw.githubusercontent.com/enix/kube-image-keeper/main/helm/kube-image-keeper/crds/repository-crd.yaml
```

## Why and when is it useful?

At [Enix](https://enix.io/), we manage production Kubernetes clusters both for our internal use and for various customers; sometimes on premises, sometimes in various clouds, public or private. We regularly run into image availability issues, for instance:

- the registry is unavailable or slow;
- a critical image was deleted from the registry (by accident or because of a misconfigured retention policy),
- the registry has pull quotas (or other rate-limiting mechanisms) and temporarily won't let us pull more images.

(The last point is a well-known challenge when pulling lots of images from the Docker Hub, and becomes particularly painful when private Kubernetes nodes access the registry through a single NAT gateway!)

We needed a solution that would:

- work across a wide range of Kubernetes versions, container engines, and image registries,
- preserve Kubernetes' out-of-the-box image caching behavior and [image pull policies](https://kubernetes.io/docs/concepts/containers/images/#image-pull-policy),
- have fairly minimal requirements,
- and be easy and quick to install.

We investigated other options, and we didn't find any that would quite fit our requirements, so we wrote kuik instead.

## Prerequisites

- A Kubernetes cluster¹ (duh!)
- Admin permissions²
- cert-manager³
- Helm⁴ >= 3.2.0
- CNI plugin with [port-mapper⁵](https://www.cni.dev/plugins/current/meta/portmap/) enabled
- In a production environment, we definitely recommend that you use persistent⁶ storage

¹A local development cluster like minikube or KinD is fine.
<br/>
²In addition to its own pods, kuik needs to register a MutatingWebhookConfiguration.
<br/>
³kuik uses cert-manager to issue and configure its webhook certificate. You don't need to configure cert-manager in a particular way (you don't even need to create an Issuer or ClusterIssuer). It's alright to just `kubectl apply` the YAML as shown in the [cert-manager installation instructions](https://cert-manager.io/docs/installation/).
<br/>
⁴If you prefer to install with "plain" YAML manifests, we'll tell you how to generate these manifests.
<br/>
⁵Most CNI plugins these days enable port-mapper out of the box, so this shouldn't be an issue, but we're mentioning it just in case.
<br/>
⁶You can use kuik without persistence, but if the pod running the registry gets deleted, you will lose your cached images. They will be automatically pulled again when needed, though.

## Supported Kubernetes versions

kuik has been developed for, and tested with, Kubernetes 1.24 to 1.30; but the code doesn't use any deprecated (or new) feature or API, and should work with newer versions as well.

## How it works

When a pod is created, kuik's **mutating webhook** rewrites its images on the fly to point to the local caching registry, adding a `localhost:{port}/` prefix (the `port` is 7439 by default, and is configurable). This means that you don't need to modify/rewrite the source registry url of your manifest/helm chart used to deploy your solution, kuik will take care of it.

On `localhost:{port}`, there is an **image proxy** that serves images from kuik's **caching registry** (when the images have been cached) or directly from the original registry (when the images haven't been cached yet).

One **controller** watches pods, and when it notices new images, it creates `CachedImage` custom resources for these images.

Another **controller** watches these `CachedImage` custom resources, and copies images from source registries to kuik's caching registry accordingly. When images come from a private registry, the controller will use the `imagePullSecrets` from the `CachedImage` spec, those are set from the pod that produced the `CachedImage`.

Here is what our images look like when using kuik:

```bash
$ kubectl get pods -o custom-columns=NAME:metadata.name,IMAGES:spec.containers[*].image
NAME                   IMAGES
debugger               localhost:7439/registrish.s3.amazonaws.com/alpine
factori-0              localhost:7439/factoriotools/factorio:1.1
nvidiactk-b5f7m        localhost:7439/nvcr.io/nvidia/k8s/container-toolkit:v1.12.0-ubuntu20.04
sshd-8b8c6cfb6-l2tc9   localhost:7439/ghcr.io/jpetazzo/shpod
web-8667899c97-2v88h   localhost:7439/nginx
web-8667899c97-89j2h   localhost:7439/nginx
web-8667899c97-fl54b   localhost:7439/nginx
```

The kuik controllers keep track of how many pods use a given image. When an image isn't used anymore, it is flagged for deletion and removed one month later. This expiration delay can be configured. You can see kuik's view of your images by looking at the `CachedImages` custom resource:

```bash
$ kubectl get cachedimages
NAME                                                       CACHED   EXPIRES AT             PODS COUNT   AGE
docker.io-dockercoins-hasher-v0.1                          true     2023-03-07T10:50:14Z                36m
docker.io-factoriotools-factorio-1.1                       true                            1            4m1s
docker.io-jpetazzo-shpod-latest                            true     2023-03-07T10:53:57Z                9m18s
docker.io-library-nginx-latest                             true                            3            36m
ghcr.io-jpetazzo-shpod-latest                              true                            1            36m
nvcr.io-nvidia-k8s-container-toolkit-v1.12.0-ubuntu20.04   true                            1            29m
registrish.s3.amazonaws.com-alpine-latest                                                  1            35m
```

## Architecture and components

In kuik's namespace, you will find:

- a `Deployment` to run kuik's controllers,
- a `DaemonSet` to run kuik's image proxy,
- a `StatefulSet` to run kuik's image cache, a `Deployment` is used instead when this component runs in HA mode.

The image cache will obviously require a bit of disk space to run (see [Garbage collection and limitations](#garbage-collection-and-limitations) below). Otherwise, kuik's components are fairly lightweight in terms of compute resources. This shows CPU and RAM usage with the default setup, featuring two controllers in HA mode:

```bash
$ kubectl top pods
NAME                                             CPU(cores)   MEMORY(bytes)
kube-image-keeper-0                              1m           86Mi
kube-image-keeper-controllers-5b5cc9fcc6-bv6cp   1m           16Mi
kube-image-keeper-controllers-5b5cc9fcc6-tjl7t   3m           24Mi
kube-image-keeper-proxy-54lzk                    1m           19Mi
```

![Architecture](https://raw.githubusercontent.com/enix/kube-image-keeper/main/docs/architecture.jpg)

### Metrics

Refer to the [dedicated documentation](https://github.com/enix/kube-image-keeper/blob/main/docs/metrics.md).

## Installation

1. Make sure that you have cert-manager installed. If not, check its [installation page](https://cert-manager.io/docs/installation/) (it's fine to use the `kubectl apply` one-liner, and no further configuration is required).
1. Install kuik's Helm chart from our [charts](https://charts.enix.io) repository:

```bash
helm upgrade --install \
     --create-namespace --namespace kuik-system \
     kube-image-keeper kube-image-keeper \
     --repo https://charts.enix.io/
```

That's it!

Our container images are available across multiple registries for reliability. You can find them on [Github Container Registry](https://github.com/enix/kube-image-keeper/pkgs/container/kube-image-keeper), [Quay](https://quay.io/repository/enix/kube-image-keeper) and [DockerHub](https://hub.docker.com/r/enix/kube-image-keeper).

CAUTION: If you use a storage backend that runs in the same cluster as kuik but in a different namespace, be sure to filter the storage backend's pods. Failure to do so may lead to interdependency issues, making it impossible to start both kuik and its storage backend if either encounters an issue.

<!-- VALUES -->

## Installation with plain YAML files

You can use Helm to generate plain YAML files and then deploy these YAML files with `kubectl apply` or whatever you want:

```bash
helm template --namespace kuik-system \
     kube-image-keeper kube-image-keeper \
     --repo https://charts.enix.io/ \
     > /tmp/kuik.yaml
kubectl create namespace kuik-system
kubectl apply -f /tmp/kuik.yaml --namespace kuik-system
```

## Uninstall kuik (whyyyy? 😢)

We are very proud of kube-image-keeper and we believe that it is an awesome project that should be used as often as possible. However, we understand that it may not fit your needs, that it may contain a bug that occurs only in some very peculiar circumstances or even that you're not sure about how and why to use it. In the 2 first cases, please [open an issue](https://github.com/enix/kube-image-keeper/issues/new), we will be very happy to address your issue or implement a new feature if we think it can make kuik better! In the case you're not sure how and why to use it, and assuming that you've already read the corresponding section of the readme, you can contact us at [contact@enix.fr](mailto:contact@enix.fr). If none of those solution made you happy, we're sad to let you go but here is the uninstall procedure:

- Disable rewriting of the pods by deleting the kuik mutating webhook.
- Restart pods using cached images, or manually rewrite them, in order to stop using images from the kuik cache.
- Delete kuik custom resources (`CachedImages` and `Repositories`).
- Uninstall kuik helm chart.

It is very important to stop using images from kuik before uninstalling. Indeed, if some pods are configured with the `imagePullPolicy: Always` and `.controllers.webhook.ignorePullPolicyAlways` value of the helm chart is set to `false`, then, in a case of a restart of a container (for example in an OOM scenario), the pod would not be able to pull its image anymore and will go in the `ImagePullBackOff` error state until someone manually fix its image.

## Configuration and customization

If you want to change e.g. the expiration delay, the port number used by the proxy, enable persistence (with a PVC) for the registry cache... You can do that with standard Helm values.

You can see the full list of parameters (along with their meaning and default values) in the chart's [values.yaml](https://github.com/enix/kube-image-keeper/blob/main/helm/kube-image-keeper/values.yaml) file, or on [kuik's page on the Artifact Hub](https://artifacthub.io/packages/helm/enix/kube-image-keeper).

For instance, to extend the expiration delay to 3 months (90 days), you can deploy kuik like this:

```bash
helm upgrade --install \
     --create-namespace --namespace kuik-system \
     kube-image-keeper kube-image-keeper \
     --repo https://charts.enix.io/ \
     --set cachedImagesExpiryDelay=90
```

## Advanced usage

### Pod filtering

There are 3 ways to tell kuik which pods it should manage (or, conversely, which ones it should ignore).

- If a pod has the label `kube-image-keeper.enix.io/image-caching-policy=ignore`, kuik will ignore the pod (it will not rewrite its image references).
- If a pod is in an ignored Namespace, it will also be ignored. Namespaces can be ignored by setting the Helm value `controllers.webhook.ignoredNamespaces` (`kube-system` and the kuik namespace will be ignored whatever the value of this parameter). (Note: this feature relies on the [NamespaceDefaultLabelName](https://kubernetes.io/docs/concepts/services-networking/network-policies/#targeting-a-namespace-by-its-name) feature gate to work.)
- Finally, kuik will only work on pods matching a specific selector. By default, the selector is empty, which means "match all the pods". The selector can be set with the Helm value `controllers.webhook.objectSelector.matchExpressions`.

This logic isn't implemented by the kuik controllers or webhook directly, but through Kubernetes' standard webhook object selectors. In other words, these parameters end up in the `MutatingWebhookConfiguration` template to filter which pods get presented to kuik's webhook. When the webhook rewrites the images for a pod, it adds a label to that pod, and the kuik controllers then rely on that label to know which `CachedImages` resources to create.

Keep in mind that kuik will ignore pods scheduled into its own namespace or in the `kube-system` namespace as recommended in the kubernetes documentation ([Avoiding operating on the kube-system namespace](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#avoiding-operating-on-the-kube-system-namespace)).

> It is recommended to exclude the namespace where your webhook is running with a namespaceSelector.
> [...]
> Accidentally mutating or rejecting requests in the kube-system namespace may cause the control plane components to stop functioning or introduce unknown behavior.

#### Image pull policy

In the case of a container configured with `imagePullPolicy: Never`, the container will always be filtered out as it makes no sense to cache an image that would never be cached and always read from the disk.

In the case of a container configured with `imagePullPolicy: Always`, or with the tag `latest`, or with no tag (defaulting to `latest`), by default, the container will be filtered out in order to keep the default behavior of kubernetes, which is to always pull the new version of the image (thus not using the cache of kuik). This can be disabled by setting the value `controllers.webhook.ignorePullPolicyAlways` to `false`.

### Cache persistence

Persistence is disabled by default. You can enable it by setting the Helm value `registry.persistence.enabled=true`. This will create a PersistentVolumeClaim with a default size of 20 GiB. You can change that size by setting the value `registry.persistence.size`. Keep in mind that enabling persistence isn't enough to provide high availability of the registry! If you want kuik to be highly available, please refer to the [high availability guide](https://github.com/enix/kube-image-keeper/blob/main/docs/high-availability.md).

Note that persistence requires your cluster to have some PersistentVolumes. If you don't have PersistentVolumes, kuik's registry Pod will remain `Pending` and your images won't be cached (but they will still be served transparently by kuik's image proxy).

### Retain policy

Sometimes, you want images to stay cached even when they are not used anymore (for instance when you run a workload for a fixed amount of time, stop it, and run it again later). You can choose to prevent `CachedImages` from expiring by manually setting the `spec.retain` flag to `true` like shown below:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: CachedImage
metadata:
  name: docker.io-library-nginx-1.25
spec:
  retain: true # here
  sourceImage: nginx:1.25
```

### Multi-arch cluster / Non-amd64 architectures

By default, kuik only caches the `amd64` variant of an image. To cache more/other architectures, you need to set the `architectures` field in your helm values.

Example:

```yaml
architectures: [amd64, arm]
```

Kuik will only cache available architectures for an image, but will not crash if the architecture doesn't exist.

No manual action is required when migrating an amd64-only cluster from v1.3.0 to v1.4.0.

### Corporate proxy

To configure kuik to work behind a corporate proxy, you can set the well-known `http_proxy` and `https_proxy` environment variables (upper and lowercase variant both works) through helm values `proxy.env` and `controllers.env` like shown below:

```yaml
controllers:
  env:
    - name: http_proxy
      value: https://proxy.mycompany.org:3128
    - name: https_proxy
      value: https://proxy.mycompany.org:3128
proxy:
  env:
    - name: http_proxy
      value: https://proxy.mycompany.org:3128
    - name: https_proxy
      value: https://proxy.mycompany.org:3128
```

Be careful that both the proxy and the controllers need to access the kubernetes API, so you might need to define the `no_proxy` variable as well to ignore the kubernetes API in case it is not reachable from your proxy (which is true most of the time).

### Insecure registries & self-signed certificates

In some cases, you may want to use images from self-hosted registries that are insecure (without TLS or with an invalid certificate for instance) or using a self-signed certificate. By default, kuik will not allow to cache images from those registries for security reasons, even though you configured your container runtime (e.g. Docker, containerd) to do so. However, you can choose to trust a list of insecure registries to pull from using the helm value `insecureRegistries`. If you use a self-signed certificate you can store the root certificate authority in a secret and reference it with the helm value `rootCertificateAuthorities`. Here is an example of the use of those two values:

```yaml
insecureRegistries:
  - http://some-registry.com
  - https://some-other-registry.com

rootCertificateAuthorities:
  secretName: some-secret
  keys:
    - root.pem
```

You can of course use as many insecure registries or root certificate authorities as you want. In the case of a self-signed certificate, you can either use the `insecureRegistries` or the `rootCertificateAuthorities` value, but trusting the root certificate will always be more secure than allowing insecure registries.

### Registry UI

For debugging reasons, it may be useful to be able to access the registry through an UI. This can be achieved by enabling the registry UI with the value `docker-registry-ui.enabled=true`. The UI will not be publicly available through an ingress, you will need to open a port-forward from port `80`. For more information about the UI and how to configure it, please see https://artifacthub.io/packages/helm/joxit/docker-registry-ui.

## Garbage collection and limitations

When a CachedImage expires because it is not used anymore by the cluster, the image is deleted from the registry. However, since kuik uses [Docker's registry](https://docs.docker.com/registry/), this only deletes **reference files** like tags. It doesn't delete blobs, which account for most of the used disk space. [Garbage collection](https://docs.docker.com/registry/garbage-collection/) allows removing those blobs, freeing up space. The garbage collecting job can be configured to run thanks to the `registry.garbageCollection.schedule` configuration in a cron-like format. It is disabled by default, because running garbage collection without persistence would just wipe out the cache registry.

Garbage collection can only run when the registry is read-only (or stopped), otherwise image corruption may happen. (This is described in the [registry documentation](https://docs.docker.com/registry/garbage-collection/).) Before running garbage collection, kuik stops the registry. During that time, all image pulls are automatically proxified to the source registry so that garbage collection is mostly transparent for cluster nodes.

Reminder: since garbage collection recreates the cache registry pod, if you run garbage collection without persistence, this will wipe out the cache registry. It is not recommended for production setups!

Currently, if the cache gets deleted, the `status.isCached` field of `CachedImages` isn't updated automatically, which means that `kubectl get cachedimages` will incorrectly report that images are cached. However, you can trigger a controller reconciliation with the following command, which will pull all images again:

```bash
kubectl annotate cachedimages --all --overwrite "timestamp=$(date +%s)"
```

## Known issues

### Conflicts with other mutating webhooks

Kuik's core functionality intercepts pod creation events to modify the definition of container images, facilitating image caching. However, some Kubernetes operators create pods autonomously and don't expect modifications to the image definitions (for example cloudnative-pg), the unexpected rewriting of the `pod.specs.containers.image` field can lead to inifinite reconciliation loop because the operator's expected target container image will be endlessly rewritten by the kuik `MutatingWebhookConfiguration`. In that case, you may want to disable kuik for specific pods using the following Helm values:

```bash
controllers:
  webhook:
    objectSelector:
      matchExpressions:
        - key: cnpg.io/podRole
          operator: NotIn
          values:
            - instance
```

### Private images are a bit less private

Imagine the following scenario:

- pods A and B use a private image, `example.com/myimage:latest`
- pod A correctly references `imagePullSecrets, but pod B does not

On a normal Kubernetes cluster (without kuik), if pods A and B are on the same node, then pod B will run correctly, even though it doesn't reference `imagePullSecrets`, because the image gets pulled when starting pod A, and once it's available on the node, any other pod can use it. However, if pods A and B are on different nodes, pod B won't start, because it won't be able to pull the private image. Some folks may use that to segregate sensitive images to specific nodes using a combination of taints, tolerations, or node selectors.

However, when using kuik, once an image has been pulled and stored in kuik's registry, it becomes available for any node on the cluster. This means that using taints, tolerations, etc. to limit sensitive images to specific nodes won't work anymore.

### Cluster autoscaling delays

With kuik, all image pulls (except in the namespaces excluded from kuik) go through kuik's registry proxy, which runs on each node thanks to a DaemonSet. When a node gets added to a Kubernetes cluster (for instance, by the cluster autoscaler), a kuik registry proxy Pod gets scheduled on that node, but it will take a brief moment to start. During that time, all other image pulls will fail. Thanks to Kubernetes automatic retry mechanisms, they will eventually succeed, but on new nodes, you may see Pods in `ErrImagePull` or `ImagePullBackOff` status for a minute before everything works correctly. If you are using cluster autoscaling trying to achieve very fast scale-up times, this is something that you might want to keep in mind.

### Garbage collection issue

We use Docker Distribution in Kuik, along with the integrated garbage collection tool. There is a bug that occurs when untagged images are pushed into the registry, causing it to crash. It's possible to end up in a situation where the registry is in read-only mode and becomes unusable. Until a permanent solution is found, we advise keeping the value `registry.garbageCollection.deleteUntagged` set to false.

### Images with digest

As of today, there is no way to manage container images based on a digest. The rationale behind this limitation is that a digest is an image manifest hash, and the manifest contains the registry URL associated with the image. Thus, pushing the image to another registry (our cache registry) changes its digest and as a consequence, it is no longer referenced by its original digest. Digest validation prevents from pushing a manifest with an invalid digest. Therefore, we currently ignore all images based on a digest. Those images will not be rewritten nor put into the cache to prevent kuik from malfunctioning.