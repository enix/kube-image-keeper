# Detect missing images before outage
This documentation will help you configure Kuik in order to monitor image availability, enable supervision and alerting, and therefore avoid the typical `ImagePullBackoff` error.

## Best suited for
- You plan a maintenance which will reschedule a lot of pods on new workers
- You plan a Kubernetes upgrade
- You have a lot of legacy images deployed on your cluster

## Benefits
You will have an exhaustive list of missing images.
You will be able to rebuild your registry in advance, and avoid `ImagePullBackoff` which is usually a synonym of a service outage

## Implementation
### Kuik custom resource to use
- [ImageSetAvailability](/docs/crds.md#clusterimagesetavailability)

### Configuration example
```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetAvailability
metadata:
  name: monitor-critical-images
spec:
  unusedImageExpiry: 720h
  imageFilter:
    include:
      - ".*nginx:.+"
      - ".*redis:.+"
    exclude:
      - "localhost[^/]*/.+"
```
