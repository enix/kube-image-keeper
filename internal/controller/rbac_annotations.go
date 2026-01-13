package controller

// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors/finalizers,verbs=update
//
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagesetmirrors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagesetmirrors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagesetmirrors/finalizers,verbs=update
//
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterreplicatedimagesets,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterreplicatedimagesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterreplicatedimagesets/finalizers,verbs=update
//
// +kubebuilder:rbac:groups=kuik.enix.io,resources=replicatedimagesets,verbs=get;list;watch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=replicatedimagesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=replicatedimagesets/finalizers,verbs=update
//
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
