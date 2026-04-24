# API Reference

## Packages
- [kuik.enix.io/v1alpha1](#kuikenixiov1alpha1)


## kuik.enix.io/v1alpha1

Package v1alpha1 contains API Schema definitions for the kuik v1alpha1 API group.

### Resource Types
- [ClusterImageSetAvailability](#clusterimagesetavailability)
- [ClusterImageSetAvailabilityList](#clusterimagesetavailabilitylist)
- [ClusterImageSetMirror](#clusterimagesetmirror)
- [ClusterImageSetMirrorList](#clusterimagesetmirrorlist)
- [ClusterReplicatedImageSet](#clusterreplicatedimageset)
- [ClusterReplicatedImageSetList](#clusterreplicatedimagesetlist)
- [ImageSetMirror](#imagesetmirror)
- [ImageSetMirrorList](#imagesetmirrorlist)
- [ReplicatedImageSet](#replicatedimageset)
- [ReplicatedImageSetList](#replicatedimagesetlist)



#### Cleanup



Cleanup defines a cleanup strategy



_Appears in:_
- [ImageSetMirrorSpec](#imagesetmirrorspec)
- [Mirror](#mirror)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ |  |  |  |
| `retention` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#duration-v1-meta)_ |  |  |  |


#### ClusterImageSetAvailability



ClusterImageSetAvailability is the Schema for the clusterimagesetavailabilities API.



_Appears in:_
- [ClusterImageSetAvailabilityList](#clusterimagesetavailabilitylist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ClusterImageSetAvailability` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ClusterImageSetAvailabilitySpec](#clusterimagesetavailabilityspec)_ |  |  |  |
| `status` _[ClusterImageSetAvailabilityStatus](#clusterimagesetavailabilitystatus)_ |  |  |  |


#### ClusterImageSetAvailabilityList



ClusterImageSetAvailabilityList contains a list of ClusterImageSetAvailability.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ClusterImageSetAvailabilityList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[ClusterImageSetAvailability](#clusterimagesetavailability) array_ |  |  |  |


#### ClusterImageSetAvailabilitySpec



ClusterImageSetAvailabilitySpec defines the desired monitoring configuration.



_Appears in:_
- [ClusterImageSetAvailability](#clusterimagesetavailability)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `unusedImageExpiry` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#duration-v1-meta)_ | UnusedImageExpiry is how long to keep tracking an image after no Pod uses it.<br />Once elapsed the image is removed from status. Example: "720h" (30 days).<br />Zero means unused images are never removed. |  | Optional: \{\} <br /> |
| `imageFilter` _[ImageFilterDefinition](#imagefilterdefinition)_ | ImageFilter selects which images to monitor. |  | Optional: \{\} <br /> |


#### ClusterImageSetAvailabilityStatus



ClusterImageSetAvailabilityStatus defines the observed state.



_Appears in:_
- [ClusterImageSetAvailability](#clusterimagesetavailability)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `imageCount` _integer_ | ImageCount is the total number of images currently being tracked. |  |  |
| `images` _[MonitoredImage](#monitoredimage) array_ |  |  |  |


#### ClusterImageSetMirror



ClusterImageSetMirror is the Schema for the clusterimagesetmirrors API.



_Appears in:_
- [ClusterImageSetMirrorList](#clusterimagesetmirrorlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ClusterImageSetMirror` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ClusterImageSetMirrorSpec](#clusterimagesetmirrorspec)_ |  |  |  |
| `status` _[ClusterImageSetMirrorStatus](#clusterimagesetmirrorstatus)_ |  |  |  |


#### ClusterImageSetMirrorList



ClusterImageSetMirrorList contains a list of ClusterImageSetMirror.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ClusterImageSetMirrorList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[ClusterImageSetMirror](#clusterimagesetmirror) array_ |  |  |  |


#### ClusterImageSetMirrorSpec

_Underlying type:_ _[ImageSetMirrorSpec](#imagesetmirrorspec)_

ClusterImageSetMirrorSpec defines the desired state of ClusterImageSetMirror.



_Appears in:_
- [ClusterImageSetMirror](#clusterimagesetmirror)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `priority` _integer_ | Priority controls the ordering of alternatives from this CR relative to the original image and other CRs.<br />Negative values place alternatives before the original image; positive values place them after.<br />Default is 0 (original image first, then alternatives in default type order). |  | Optional: \{\} <br /> |
| `imageFilter` _[ImageFilterDefinition](#imagefilterdefinition)_ |  |  | Optional: \{\} <br /> |
| `cleanup` _[Cleanup](#cleanup)_ |  |  |  |
| `mirrors` _[Mirrors](#mirrors)_ |  |  |  |


#### ClusterImageSetMirrorStatus

_Underlying type:_ _[ImageSetMirrorStatus](#imagesetmirrorstatus)_

ClusterImageSetMirrorStatus defines the observed state of ClusterImageSetMirror.



_Appears in:_
- [ClusterImageSetMirror](#clusterimagesetmirror)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `matchingImages` _[MatchingImage](#matchingimage) array_ |  |  |  |


#### ClusterReplicatedImageSet



ClusterReplicatedImageSet is the Schema for the clusterreplicatedimagesets API.



_Appears in:_
- [ClusterReplicatedImageSetList](#clusterreplicatedimagesetlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ClusterReplicatedImageSet` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ClusterReplicatedImageSetSpec](#clusterreplicatedimagesetspec)_ |  |  |  |
| `status` _[ClusterReplicatedImageSetStatus](#clusterreplicatedimagesetstatus)_ |  |  |  |


#### ClusterReplicatedImageSetList



ClusterReplicatedImageSetList contains a list of ClusterReplicatedImageSet.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ClusterReplicatedImageSetList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[ClusterReplicatedImageSet](#clusterreplicatedimageset) array_ |  |  |  |


#### ClusterReplicatedImageSetSpec

_Underlying type:_ _[ReplicatedImageSetSpec](#replicatedimagesetspec)_

ClusterReplicatedImageSetSpec defines the desired state of ClusterReplicatedImageSet.



_Appears in:_
- [ClusterReplicatedImageSet](#clusterreplicatedimageset)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `priority` _integer_ | Priority controls the ordering of alternatives from this CR relative to the original image and other CRs.<br />Negative values place alternatives before the original image; positive values place them after.<br />Default is 0 (original image first, then alternatives in default type order). |  | Optional: \{\} <br /> |
| `upstreams` _[ReplicatedUpstream](#replicatedupstream) array_ |  |  |  |


#### ClusterReplicatedImageSetStatus

_Underlying type:_ _[ReplicatedImageSetStatus](#replicatedimagesetstatus)_

ClusterReplicatedImageSetStatus defines the observed state of ClusterReplicatedImageSet.



_Appears in:_
- [ClusterReplicatedImageSet](#clusterreplicatedimageset)



#### CredentialSecret







_Appears in:_
- [Mirror](#mirror)
- [ReplicatedUpstream](#replicatedupstream)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the secret |  |  |
| `namespace` _string_ | Namespace is the namespace where the secret is located.<br />This value is ignored for namespaced resources and the namespace of the parent object is used instead. |  |  |


#### ImageAvailabilityStatus

_Underlying type:_ _string_

ImageAvailabilityStatus represents the result of an image availability check.

_Validation:_
- Enum: [Scheduled Available NotFound Unreachable InvalidAuth UnavailableSecret QuotaExceeded]

_Appears in:_
- [MonitoredImage](#monitoredimage)

| Field | Description |
| --- | --- |
| `Scheduled` |  |
| `Available` |  |
| `NotFound` |  |
| `Unreachable` |  |
| `InvalidAuth` |  |
| `UnavailableSecret` |  |
| `QuotaExceeded` |  |


#### ImageFilterDefinition



ImageFilterDefinition is the definition of an image filter



_Appears in:_
- [ClusterImageSetAvailabilitySpec](#clusterimagesetavailabilityspec)
- [ImageSetMirrorSpec](#imagesetmirrorspec)
- [ReplicatedUpstream](#replicatedupstream)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `include` _string array_ |  |  |  |
| `exclude` _string array_ |  |  |  |


#### ImageReference







_Appears in:_
- [ReplicatedUpstream](#replicatedupstream)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `registry` _string_ | Registry is the registry where the image is located |  |  |
| `path` _string_ | Path is a string identifying the image in a registry |  |  |


#### ImageSetMirror



ImageSetMirror is the Schema for the imagesetmirrors API.



_Appears in:_
- [ImageSetMirrorList](#imagesetmirrorlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ImageSetMirror` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ImageSetMirrorSpec](#imagesetmirrorspec)_ |  |  |  |
| `status` _[ImageSetMirrorStatus](#imagesetmirrorstatus)_ |  |  |  |


#### ImageSetMirrorList



ImageSetMirrorList contains a list of ImageSetMirror.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ImageSetMirrorList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[ImageSetMirror](#imagesetmirror) array_ |  |  |  |


#### ImageSetMirrorSpec



ImageSetMirrorSpec defines the desired state of ImageSetMirror.



_Appears in:_
- [ClusterImageSetMirrorSpec](#clusterimagesetmirrorspec)
- [ImageSetMirror](#imagesetmirror)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `priority` _integer_ | Priority controls the ordering of alternatives from this CR relative to the original image and other CRs.<br />Negative values place alternatives before the original image; positive values place them after.<br />Default is 0 (original image first, then alternatives in default type order). |  | Optional: \{\} <br /> |
| `imageFilter` _[ImageFilterDefinition](#imagefilterdefinition)_ |  |  | Optional: \{\} <br /> |
| `cleanup` _[Cleanup](#cleanup)_ |  |  |  |
| `mirrors` _[Mirrors](#mirrors)_ |  |  |  |


#### ImageSetMirrorStatus



ImageSetMirrorStatus defines the observed state of ImageSetMirror.



_Appears in:_
- [ClusterImageSetMirrorStatus](#clusterimagesetmirrorstatus)
- [ImageSetMirror](#imagesetmirror)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `matchingImages` _[MatchingImage](#matchingimage) array_ |  |  |  |


#### MatchingImage







_Appears in:_
- [ImageSetMirrorStatus](#imagesetmirrorstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ |  |  |  |
| `mirrors` _[MirrorStatus](#mirrorstatus) array_ |  |  |  |
| `unusedSince` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ |  |  |  |


#### Mirror







_Appears in:_
- [Mirrors](#mirrors)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `priority` _integer_ | Priority controls the ordering of this mirror in comparaison to similar alternatives (mirrors with same parent priority) when re-routing images.<br />0 means no specific ordering (YAML declaration order is preserved).<br />Positive values are sorted ascending: lower value = higher priority. |  | Optional: \{\} <br /> |
| `registry` _string_ |  |  |  |
| `path` _string_ |  |  |  |
| `credentialSecret` _[CredentialSecret](#credentialsecret)_ |  |  |  |
| `cleanup` _[Cleanup](#cleanup)_ |  |  |  |


#### MirrorStatus







_Appears in:_
- [MatchingImage](#matchingimage)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ |  |  |  |
| `mirroredAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ |  |  |  |
| `lastError` _string_ |  |  |  |


#### Mirrors

_Underlying type:_ _[Mirror](#mirror)_





_Appears in:_
- [ImageSetMirrorSpec](#imagesetmirrorspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `priority` _integer_ | Priority controls the ordering of this mirror in comparaison to similar alternatives (mirrors with same parent priority) when re-routing images.<br />0 means no specific ordering (YAML declaration order is preserved).<br />Positive values are sorted ascending: lower value = higher priority. |  | Optional: \{\} <br /> |
| `registry` _string_ |  |  |  |
| `path` _string_ |  |  |  |
| `credentialSecret` _[CredentialSecret](#credentialsecret)_ |  |  |  |
| `cleanup` _[Cleanup](#cleanup)_ |  |  |  |


#### MonitoredImage



MonitoredImage holds the current availability state for a single image.



_Appears in:_
- [ClusterImageSetAvailabilityStatus](#clusterimagesetavailabilitystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image is the full normalised image reference, e.g. "docker.io/library/nginx:1.27". |  |  |
| `status` _[ImageAvailabilityStatus](#imageavailabilitystatus)_ | Status is the result of the last availability check. | Scheduled | Enum: [Scheduled Available NotFound Unreachable InvalidAuth UnavailableSecret QuotaExceeded] <br /> |
| `unusedSince` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | UnusedSince is the timestamp when the last Pod referencing this image disappeared.<br />Nil means at least one Pod currently uses this image. |  | Optional: \{\} <br /> |
| `lastError` _string_ | LastError contains the error message from the last failed check, if any. |  | Optional: \{\} <br /> |
| `lastMonitor` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | Lastmonitor is the timestamp of the last availability check.<br />nil means the image has not been checked yet. |  | Optional: \{\} <br /> |
| `original` _boolean_ | Original is a flag that indicate whether if this MonitoredImage has been<br />created from an original or a re-routed image. |  | Optional: \{\} <br /> |


#### ReplicatedImageSet



ReplicatedImageSet is the Schema for the replicatedimagesets API.



_Appears in:_
- [ReplicatedImageSetList](#replicatedimagesetlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ReplicatedImageSet` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ReplicatedImageSetSpec](#replicatedimagesetspec)_ |  |  |  |
| `status` _[ReplicatedImageSetStatus](#replicatedimagesetstatus)_ |  |  |  |


#### ReplicatedImageSetList



ReplicatedImageSetList contains a list of ReplicatedImageSet.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kuik.enix.io/v1alpha1` | | |
| `kind` _string_ | `ReplicatedImageSetList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  | Optional: \{\} <br /> |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  | Optional: \{\} <br /> |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[ReplicatedImageSet](#replicatedimageset) array_ |  |  |  |


#### ReplicatedImageSetSpec



ReplicatedImageSetSpec defines the desired state of ReplicatedImageSet.



_Appears in:_
- [ClusterReplicatedImageSetSpec](#clusterreplicatedimagesetspec)
- [ReplicatedImageSet](#replicatedimageset)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `priority` _integer_ | Priority controls the ordering of alternatives from this CR relative to the original image and other CRs.<br />Negative values place alternatives before the original image; positive values place them after.<br />Default is 0 (original image first, then alternatives in default type order). |  | Optional: \{\} <br /> |
| `upstreams` _[ReplicatedUpstream](#replicatedupstream) array_ |  |  |  |


#### ReplicatedImageSetStatus



ReplicatedImageSetStatus defines the observed state of ReplicatedImageSet.



_Appears in:_
- [ClusterReplicatedImageSetStatus](#clusterreplicatedimagesetstatus)
- [ReplicatedImageSet](#replicatedimageset)



#### ReplicatedUpstream







_Appears in:_
- [ReplicatedImageSetSpec](#replicatedimagesetspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `registry` _string_ | Registry is the registry where the image is located |  |  |
| `path` _string_ | Path is a string identifying the image in a registry |  |  |
| `priority` _integer_ | Priority controls the ordering of this mirror in comparaison to similar alternatives (replicated upstream with same parent priority) when re-routing images.<br />0 means no specific ordering (YAML declaration order is preserved).<br />Positive values are sorted ascending: lower value = higher priority. |  | Optional: \{\} <br /> |
| `imageFilter` _[ImageFilterDefinition](#imagefilterdefinition)_ | ImageFilter defines the rules used to select replicated images. |  | Optional: \{\} <br /> |
| `credentialSecret` _[CredentialSecret](#credentialsecret)_ | CredentialSecret is a reference to the secret used to pull matching images. |  |  |


