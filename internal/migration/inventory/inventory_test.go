package inventory

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kit "github.com/enix/kube-image-keeper/internal/migration/inventory/inventorytest"
)

const (
	v2Namespace = "keeper"
	v3Namespace = "kuik-system"
	v3Release   = "kuik"
	keyImage    = "image"
	teamA       = "team-a"
	teamB       = "team-b"
	// The ClusterImageSetMirror "all" mirrors under mirror.example.com/all, everywhere; the
	// ImageSetMirror "team-a" under mirror.example.com/team-a, in team-a only.
	all           = "all"
	clusterMirror = "mirror.example.com/all"
	teamAMirror   = "mirror.example.com/team-a"

	originalImages = "kuik.enix.io/original-images"
	v2Secret       = "kuik-registry-credentials-0123456789abcdef"
	v3Secret       = "kuik-inject-imagemirror-all"
	acme           = "acme"
	nginx          = "docker.io/library/nginx:1.27"
	imageMirror    = "ImageMirror"
	isReady        = "IsReady"
	since          = "2026-10-01T10:00:00Z"

	// Container and workload names.
	app     = "app"
	web     = "web"
	sidecar = "sidecar"
	setup   = "setup"
	migrate = "migrate"
	backup  = "backup"
	agent   = "agent"
	proxy   = "proxy"
)

// mirrors are the v2 mirrors of the pod specs.
func mirrors() []client.Object {
	return append(kit.V2CRDs(),
		kit.Object("ClusterImageSetMirror", "", all, kit.Mirrors("mirror.example.com", all), nil),
		kit.Object("ImageSetMirror", teamA, teamA, kit.Mirrors("mirror.example.com", teamA), nil),
	)
}

func pod(namespace, name string, owner []metav1.OwnerReference, annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: name, OwnerReferences: owner, Annotations: annotations,
	}}
}

func read(objects ...client.Object) *Inventory {
	c, guard := kit.Reader(objects...)
	inv, err := Read(context.Background(), c, Options{Namespace: v3Namespace})
	Expect(err).NotTo(HaveOccurred())
	Expect(guard.Writes).To(BeZero())
	Expect(guard.SecretReads).To(BeZero())
	return inv
}

// podNamed returns the pod of inv named namespace/name, nil when inv has none.
func podNamed(inv *Inventory, namespace, name string) *Pod {
	for i := range inv.Pods {
		if inv.Pods[i].Namespace == namespace && inv.Pods[i].Name == name {
			return &inv.Pods[i]
		}
	}
	return nil
}

var _ = Describe("Read", func() {
	Context("the v2 installation", func() {
		It("finds v2 and its version from the manager Deployment of its chart", func() {
			// Another operator scaffolded by kubebuilder carries the same component and
			// control-plane labels, not the kuik chart's.
			other := kit.V2Manager("other", "operator", "1.0.0")
			other.Labels["helm.sh/chart"] = "other-operator-1.0.0"
			other.Labels["app.kubernetes.io/name"] = "other-operator"
			other.Spec.Template.Spec.Containers[0].Image = "registry.example.com/other/operator:1.0.0"

			inv := read(other,
				kit.V3Deployment(v3Namespace, "kuik-webhook", true, "webhook"),
				kit.V2Manager(v2Namespace, "keeper", "2.3.1"),
			)

			Expect(inv.V2.Running()).To(BeTrue())
			Expect(inv.V2.Namespace).To(Equal(v2Namespace))
			Expect(inv.V2.Deployment).To(Equal("keeper-manager"))
			Expect(inv.V2.Version).To(Equal("2.3.1"))
		})

		It("reports v2 stopped when its manager Deployment is gone but its webhook configuration remains", func() {
			inv := read(
				kit.V3Webhook(v3Namespace, v3Release),
				kit.V2Webhook(v2Namespace, "keeper"),
			)

			Expect(inv.V2.Running()).To(BeFalse())
			Expect(inv.V2.WebhookConfiguration).To(Equal("keeper-mutating-webhook"))
			Expect(inv.V2.Namespace).To(Equal(v2Namespace))
		})

		It("lists the v2 CRDs left, prerelease ones included", func() {
			unrelated := kit.CRD("widgets", "Widget", false, "size")
			unrelated.Object["spec"].(map[string]any)["group"] = "example.com"
			unrelated.SetName("widgets.example.com")

			inv := read(
				kit.CRD("clusterimagesetmirrors", "ClusterImageSetMirror", false, "mirrors"),
				kit.CRD("imagesetmirrors", "ImageSetMirror", true, "mirrors"),
				kit.CRD("images", "Image", false, "sourceImage"),
				kit.CRD("registrymonitors", "RegistryMonitor", false, "registry"),
				kit.CRD("imagealternatives", "ImageAlternative", false, "alternatives"),
				unrelated,
			)

			Expect(inv.V2CRDs).To(ConsistOf(
				CRD{Name: "clusterimagesetmirrors.kuik.enix.io", Kind: "ClusterImageSetMirror"},
				CRD{Name: "imagesetmirrors.kuik.enix.io", Kind: "ImageSetMirror", Namespaced: true},
				CRD{Name: "images.kuik.enix.io", Kind: "Image", Prerelease: true},
				CRD{Name: "registrymonitors.kuik.enix.io", Kind: "RegistryMonitor", Prerelease: true},
			))
		})

		It("tells a prerelease ImageMirror or ImageMonitor CRD from the v3 one", func() {
			// 2.0 prereleases: ImageMirror spec {registry, path, targetRegistry}, ImageMonitor
			// spec {registry, path}.
			prereleaseMirror := kit.CRD("imagemirrors", imageMirror, false, "registry", "path", "targetRegistry")
			prereleaseMonitor := kit.CRD("imagemonitors", "ImageMonitor", false, "registry", "path")
			v3 := kit.V3CRDs()
			v3Mirror, v3Monitor := v3[0], v3[2]

			inv := read(prereleaseMirror, v3Monitor)
			Expect(inv.V2CRDs).To(ConsistOf(
				CRD{Name: "imagemirrors.kuik.enix.io", Kind: imageMirror, Prerelease: true},
			))

			inv = read(v3Mirror, prereleaseMonitor)
			Expect(inv.V2CRDs).To(ConsistOf(
				CRD{Name: "imagemonitors.kuik.enix.io", Kind: "ImageMonitor", Prerelease: true},
			))
		})
	})

	Context("the v2 resources", func() {
		It("reads every v2 resource with its spec and status, namespaced ones included", func() {
			cismSpec := map[string]any{
				"priority": int64(10),
				"imageFilter": map[string]any{
					"include": []any{".*"},
					"exclude": []any{"^registry\\.example\\.com/.*"},
				},
				"cleanup": map[string]any{"enabled": true, "retention": "720h"},
				"mirrors": []any{map[string]any{
					"registry": "mirror.example.com",
					"path":     all,
					"credentialSecret": map[string]any{
						"name": "mirror-credentials", "namespace": v2Namespace,
					},
				}},
			}
			cismStatus := map[string]any{"matchingImages": []any{map[string]any{
				keyImage: nginx,
				"mirrors": []any{map[string]any{
					keyImage:     clusterMirror + "/library/nginx:1.27",
					"mirroredAt": since,
				}},
			}}}
			cism := kit.Object("ClusterImageSetMirror", "", all, cismSpec, cismStatus)
			cism.SetFinalizers([]string{"kuik.enix.io/mirror-cleanup", "kuik.enix.io/secret-cleanup"})
			ismSpec := kit.Mirrors("mirror.example.com", teamA)
			risSpec := map[string]any{"upstreams": []any{map[string]any{"registry": "registry.example.com", "path": acme}}}
			cisaSpec := map[string]any{"unusedImageExpiry": "720h"}
			cisaStatus := map[string]any{"images": []any{map[string]any{keyImage: nginx, "status": "Available"}}}
			imageSpec := map[string]any{"sourceImage": nginx}

			inv := read(append(kit.V2CRDs(),
				kit.CRD("images", "Image", false, "sourceImage"),
				cism,
				kit.Object("ImageSetMirror", teamA, teamA, ismSpec, nil),
				kit.Object("ClusterReplicatedImageSet", "", acme, risSpec, nil),
				kit.Object("ReplicatedImageSet", teamB, acme, risSpec, nil),
				kit.Object("ClusterImageSetAvailability", "", all, cisaSpec, cisaStatus),
				kit.Object("Image", "", "nginx", imageSpec, nil),
			)...)

			Expect(inv.V2Resources).To(ConsistOf(
				Resource{
					Kind: "ClusterImageSetMirror", Name: all, Spec: cismSpec, Status: cismStatus,
					Finalizers: []string{"kuik.enix.io/mirror-cleanup", "kuik.enix.io/secret-cleanup"},
				},
				Resource{Kind: "ImageSetMirror", Namespace: teamA, Name: teamA, Spec: ismSpec},
				Resource{Kind: "ClusterReplicatedImageSet", Name: acme, Spec: risSpec},
				Resource{Kind: "ReplicatedImageSet", Namespace: teamB, Name: acme, Spec: risSpec},
				Resource{Kind: "ClusterImageSetAvailability", Name: all, Spec: cisaSpec, Status: cisaStatus},
				Resource{Kind: "Image", Name: "nginx", Spec: imageSpec},
			))
		})
	})

	Context("a pod rewritten by v2", func() {
		It("finds the containers whose image sits under a v2 mirror path, with their origin from kuik.enix.io/original-images", func() {
			rewritten := pod(teamB, web, nil, map[string]string{
				originalImages: `{"app":"nginx:1.27","sidecar":"busybox:1.36"}`,
			})
			rewritten.Spec.Containers = []corev1.Container{
				{Name: app, Image: clusterMirror + "/library/nginx:1.27"},
				// Not rewritten: v2 had no copy yet when the pod was created.
				{Name: sidecar, Image: "busybox:1.36"},
			}
			// A path that only starts like a mirror path is not under it.
			lookalike := pod(teamB, "lookalike", nil, nil)
			lookalike.Spec.Containers = []corev1.Container{{Name: app, Image: clusterMirror + "-old/library/nginx:1.27"}}
			untouched := pod(teamB, "untouched", nil, nil)
			untouched.Spec.Containers = []corev1.Container{{Name: app, Image: "nginx:1.27"}}

			inv := read(append(mirrors(), rewritten, lookalike, untouched)...)

			Expect(inv.Pods).To(ConsistOf(Pod{
				Namespace: teamB, Name: web,
				Containers: []Container{{
					Name: app, Image: clusterMirror + "/library/nginx:1.27",
					Origin: nginx, OriginFrom: FromAnnotation,
				}},
			}))
		})

		It("applies the mirror path of an ImageSetMirror only in its own namespace", func() {
			annotations := map[string]string{originalImages: `{"app":"registry.example.com/acme/api:2"}`}
			inTeamA := pod(teamA, "api", nil, annotations)
			inTeamA.Spec.Containers = []corev1.Container{{Name: app, Image: teamAMirror + "/acme/api:2"}}
			inTeamB := pod(teamB, "api", nil, annotations)
			inTeamB.Spec.Containers = []corev1.Container{{Name: app, Image: teamAMirror + "/acme/api:2"}}

			inv := read(append(mirrors(), inTeamA, inTeamB)...)

			Expect(podNamed(inv, teamA, "api")).NotTo(BeNil())
			Expect(podNamed(inv, teamB, "api")).To(BeNil())
		})

		It("takes the origin from the owner's template when the annotation does not record the container", func() {
			db := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: teamB, Name: "db"},
				Spec: appsv1.StatefulSetSpec{Template: kit.Template(
					map[string]string{migrate: "registry.example.com/acme/migrate:1"},
					map[string]string{"db": "registry.example.com/acme/db:16"},
				)},
			}
			// v2 rewrites init containers but never records them.
			rewritten := pod(teamB, "db-0", kit.Controller("apps/v1", "StatefulSet", "db"), map[string]string{
				originalImages: `{"db":"registry.example.com/acme/db:16"}`,
			})
			rewritten.Spec.InitContainers = []corev1.Container{{Name: migrate, Image: clusterMirror + "/acme/migrate:1"}}
			rewritten.Spec.Containers = []corev1.Container{{Name: "db", Image: clusterMirror + "/acme/db:16"}}
			// 2.0 and 2.1 record no original image at all.
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Namespace: teamB, Name: backup},
				Spec: batchv1.JobSpec{Template: kit.Template(nil,
					map[string]string{backup: "registry.example.com/acme/backup:3"},
				)},
			}
			older := pod(teamB, "backup-x", kit.Controller("batch/v1", "Job", backup), map[string]string{
				"kuik.enix.io/processed-containers": backup,
			})
			older.Spec.Containers = []corev1.Container{{Name: backup, Image: clusterMirror + "/acme/backup:3"}}

			inv := read(append(mirrors(), db, rewritten, job, older)...)

			Expect(podNamed(inv, teamB, "db-0")).To(Equal(&Pod{
				Namespace: teamB, Name: "db-0", Owner: &Owner{Kind: "StatefulSet", Name: "db"},
				Containers: []Container{
					{
						Name: migrate, Init: true, Image: clusterMirror + "/acme/migrate:1",
						Origin: "registry.example.com/acme/migrate:1", OriginFrom: FromTemplate,
					},
					{
						Name: "db", Image: clusterMirror + "/acme/db:16",
						Origin: "registry.example.com/acme/db:16", OriginFrom: FromAnnotation,
					},
				},
			}))
			Expect(podNamed(inv, teamB, "backup-x")).To(Equal(&Pod{
				Namespace: teamB, Name: "backup-x", Owner: &Owner{Kind: "Job", Name: backup},
				Containers: []Container{{
					Name: backup, Image: clusterMirror + "/acme/backup:3",
					Origin: "registry.example.com/acme/backup:3", OriginFrom: FromTemplate,
				}},
			}))
		})

		It("reports a container whose annotation and owner's template disagree", func() {
			daemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: teamB, Name: agent},
				Spec: appsv1.DaemonSetSpec{Template: kit.Template(nil,
					map[string]string{agent: "registry.example.com/acme/agent:2", proxy: "envoy:1.30"},
				)},
			}
			rewritten := pod(teamB, "agent-x", kit.Controller("apps/v1", "DaemonSet", agent), map[string]string{
				originalImages: `{"agent":"registry.example.com/acme/agent:1","proxy":"docker.io/envoy:1.30"}`,
			})
			rewritten.Spec.Containers = []corev1.Container{
				{Name: agent, Image: clusterMirror + "/acme/agent:1"},
				// The same image, written differently: no disagreement.
				{Name: proxy, Image: clusterMirror + "/library/envoy:1.30"},
			}

			inv := read(append(mirrors(), daemonSet, rewritten)...)

			Expect(podNamed(inv, teamB, "agent-x")).NotTo(BeNil())
			Expect(podNamed(inv, teamB, "agent-x").Containers).To(Equal([]Container{
				{
					Name: agent, Image: clusterMirror + "/acme/agent:1",
					Origin: "registry.example.com/acme/agent:1", OriginFrom: FromAnnotation,
					TemplateOrigin: "registry.example.com/acme/agent:2",
				},
				{
					Name: proxy, Image: clusterMirror + "/library/envoy:1.30",
					Origin: "docker.io/library/envoy:1.30", OriginFrom: FromAnnotation,
				},
			}))
		})

		It("resolves its owner through a ReplicaSet to the Deployment", func() {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Namespace: teamB, Name: web},
				// The Deployment rolled on since: the pod comes from its ReplicaSet's template.
				Spec: appsv1.DeploymentSpec{Template: kit.Template(
					map[string]string{setup: "registry.example.com/acme/setup:2"}, nil,
				)},
			}
			replicaSet := &appsv1.ReplicaSet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: teamB, Name: "web-5d4f",
					OwnerReferences: kit.Controller("apps/v1", "Deployment", web),
				},
				Spec: appsv1.ReplicaSetSpec{Template: kit.Template(
					map[string]string{setup: "registry.example.com/acme/setup:1"}, nil,
				)},
			}
			rewritten := pod(teamB, "web-5d4f-x", kit.Controller("apps/v1", "ReplicaSet", "web-5d4f"), nil)
			rewritten.Spec.InitContainers = []corev1.Container{{Name: setup, Image: clusterMirror + "/acme/setup:1"}}

			inv := read(append(mirrors(), deployment, replicaSet, rewritten)...)

			Expect(podNamed(inv, teamB, "web-5d4f-x")).To(Equal(&Pod{
				Namespace: teamB, Name: "web-5d4f-x", Owner: &Owner{Kind: "Deployment", Name: web},
				Containers: []Container{{
					Name: setup, Init: true, Image: clusterMirror + "/acme/setup:1",
					Origin: "registry.example.com/acme/setup:1", OriginFrom: FromTemplate,
				}},
			}))
		})

		It("finds a pod that references a v2 pull secret without running a v2 image", func() {
			secret := kit.V2PullSecret(teamB, v2Secret, "ClusterReplicatedImageSet", acme, "uid-acme")
			withSecret := pod(teamB, "worker", nil, nil)
			withSecret.Spec.Containers = []corev1.Container{{Name: app, Image: "registry.example.com/acme/worker:1"}}
			withSecret.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: v2Secret}, {Name: "team-credentials"}}
			// A Secret of the same name in another namespace is not this pod's.
			elsewhere := pod("team-c", "worker", nil, nil)
			elsewhere.Spec.Containers = []corev1.Container{{Name: app, Image: "registry.example.com/acme/worker:1"}}
			elsewhere.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: v2Secret}}

			inv := read(append(mirrors(), secret, withSecret, elsewhere)...)

			Expect(inv.Pods).To(ConsistOf(Pod{Namespace: teamB, Name: "worker", PullSecrets: []string{v2Secret}}))
		})

		It("leaves out the containers v3 rewrote and the pull secrets v3 injected", func() {
			// The v3 ImageMirror writes under the same path as the v2 one.
			v3Ref := clusterMirror + "/docker.io/library/nginx:1.27_blue-cluster"
			rewritten := pod(teamB, web, nil, map[string]string{
				"kuik.enix.io/rewrites": `{"app":{"by":"ImageMirror/all","origin":"docker.io/library/nginx:1.27","rewrittenTo":"` +
					v3Ref + `","policy":"Always"}}`,
				originalImages: `{"sidecar":"busybox:1.36"}`,
			})
			rewritten.Spec.Containers = []corev1.Container{
				{Name: app, Image: v3Ref},
				// Unrecorded, but tagged with the cluster ID: v3's own output.
				{Name: "cache", Image: clusterMirror + "/docker.io/library/redis:7_blue-cluster"},
				// Rewritten by v2 before v3 came: still v2's.
				{Name: sidecar, Image: clusterMirror + "/library/busybox:1.36"},
			}
			rewritten.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: v3Secret}}
			injected := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: teamB, Name: v3Secret}}

			inv := read(append(mirrors(), kit.ClusterIDConfigMap(v3Namespace, "blue-cluster"), injected, rewritten)...)

			Expect(inv.Pods).To(ConsistOf(Pod{
				Namespace: teamB, Name: web,
				Containers: []Container{{
					Name: sidecar, Image: clusterMirror + "/library/busybox:1.36",
					Origin: "docker.io/library/busybox:1.36", OriginFrom: FromAnnotation,
				}},
			}))
		})
	})

	Context("the v2 pull secrets", func() {
		It("lists the Secrets labelled kuik.enix.io/owner-uid without reading their data", func() {
			inv := read(
				kit.V2PullSecret(teamA, v2Secret, "ClusterReplicatedImageSet", acme, "uid-acme"),
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: teamA, Name: v3Secret}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: teamA, Name: "team-credentials"}},
			)

			Expect(inv.PullSecrets).To(ConsistOf(PullSecret{
				Namespace: teamA, Name: v2Secret,
				Owner: PullSecretOwner{UID: "uid-acme", Kind: "ClusterReplicatedImageSet", Name: acme},
			}))
		})
	})

	Context("v3", func() {
		It("reads the cluster ID from its ConfigMap and reports an invalid one", func() {
			inv := read(kit.ClusterIDConfigMap(v3Namespace, "blue-cluster"))
			Expect(inv.V3.ClusterID).To(Equal("blue-cluster"))
			Expect(inv.V3.ClusterIDError).To(BeEmpty())

			// A ConfigMap of the same name elsewhere is not kuik's.
			inv = read(kit.ClusterIDConfigMap("default", "blue-cluster"))
			Expect(inv.V3.ClusterID).To(BeEmpty())

			inv = read(kit.ClusterIDConfigMap(v3Namespace, "blue_cluster"))
			Expect(inv.V3.ClusterID).To(BeEmpty())
			Expect(inv.V3.ClusterIDError).To(ContainSubstring(`clusterID "blue_cluster" must match`))
		})

		It("reports whether the webhook, reconciler and secret-syncer modes run, combined in one process or not, and the v3 webhook configuration exists", func() {
			inv := read(
				kit.V3Deployment(v3Namespace, "kuik-webhook", true, "webhook"),
				kit.V3Deployment(v3Namespace, "kuik-reconciler", false, "reconciler"),
				kit.V3Webhook(v3Namespace, v3Release),
				// v2's manager runs no mode, and its webhook configuration is not v3's.
				kit.V2Manager(v3Namespace, "keeper", "2.3.1"),
				kit.V2Webhook(v3Namespace, "keeper"),
			)
			Expect(inv.V3.Webhook).To(Equal(Mode{Deployment: "kuik-webhook", Ready: true}))
			Expect(inv.V3.Reconciler).To(Equal(Mode{Deployment: "kuik-reconciler"}))
			Expect(inv.V3.SecretSyncer).To(Equal(Mode{}))
			Expect(inv.V3.Combined()).To(BeFalse())
			Expect(inv.V3.WebhookConfiguration).To(Equal("kuik-mutating-webhook"))

			inv = read(
				kit.V3Deployment(v3Namespace, v3Release, true, "webhook", "reconciler", "secret-syncer"),
				// Another release in another namespace is not the one read.
				kit.V3Deployment("elsewhere", "kuik-reconciler", true, "reconciler"),
				kit.V3Webhook("elsewhere", v3Release),
			)
			Expect(inv.V3.Webhook).To(Equal(Mode{Deployment: v3Release, Ready: true}))
			Expect(inv.V3.Reconciler).To(Equal(Mode{Deployment: v3Release, Ready: true}))
			Expect(inv.V3.SecretSyncer).To(Equal(Mode{Deployment: v3Release, Ready: true}))
			Expect(inv.V3.Combined()).To(BeTrue())
			Expect(inv.V3.WebhookConfiguration).To(BeEmpty())
		})

		It("reports the Ready condition of each v3 resource and each ImageMirror whose destination misses images", func() {
			condition := func(conditionType, status, reason, message string) any {
				return map[string]any{
					"type": conditionType, "status": status, "reason": reason, "message": message,
					"lastTransitionTime": since,
				}
			}
			conditions := func(c ...any) map[string]any { return map[string]any{"conditions": c} }
			ready := condition("Ready", "True", isReady, "")

			inv := read(append(kit.V3CRDs(),
				kit.Object(imageMirror, "", "complete", nil, conditions(ready)),
				kit.Object(imageMirror, "", "missing", nil, conditions(ready,
					condition("DestinationOutOfSync", "True", "MissingImages", "3 images are missing"))),
				kit.Object(imageMirror, "", "broken", nil, conditions(
					condition("Ready", "False", "SecretNotFound", "secret kuik-system/mirror-credentials not found"))),
				kit.Object("ImageAlternative", "", acme, nil, conditions(ready)),
				kit.Object("ImageMonitor", "", all, nil, nil),
			)...)

			Expect(inv.V3.Resources).To(ConsistOf(
				V3Resource{Kind: imageMirror, Name: "complete", Ready: metav1.ConditionTrue, Reason: isReady},
				V3Resource{Kind: imageMirror, Name: "missing", Ready: metav1.ConditionTrue, Reason: isReady, MissingImages: true},
				V3Resource{
					Kind: imageMirror, Name: "broken", Ready: metav1.ConditionFalse, Reason: "SecretNotFound",
					Message: "secret kuik-system/mirror-credentials not found",
				},
				V3Resource{Kind: "ImageAlternative", Name: acme, Ready: metav1.ConditionTrue, Reason: isReady},
				V3Resource{Kind: "ImageMonitor", Name: all},
			))
		})
	})

	It("writes nothing to the cluster", func() {
		rewritten := pod(teamB, web, nil, map[string]string{originalImages: `{"app":"nginx:1.27"}`})
		rewritten.Spec.Containers = []corev1.Container{{Name: app, Image: clusterMirror + "/library/nginx:1.27"}}
		c, guard := kit.Reader(append(append(mirrors(), kit.V3CRDs()...),
			kit.V2Manager(v2Namespace, "keeper", "2.3.1"),
			kit.V2Webhook(v2Namespace, "keeper"),
			kit.V3Deployment(v3Namespace, "kuik-reconciler", true, "reconciler"),
			kit.V3Webhook(v3Namespace, v3Release),
			kit.ClusterIDConfigMap(v3Namespace, "blue-cluster"),
			kit.V2PullSecret(teamB, v2Secret, "ClusterImageSetMirror", all, "uid-all"),
			kit.Object(imageMirror, "", all, nil, nil),
			rewritten,
		)...)

		inv, err := Read(context.Background(), c, Options{Namespace: v3Namespace})

		Expect(err).NotTo(HaveOccurred())
		// The cluster was read, not left alone.
		Expect(inv.V2.Running()).To(BeTrue())
		Expect(inv.Pods).NotTo(BeEmpty())
		Expect(guard.Writes).To(BeZero())
	})
})
