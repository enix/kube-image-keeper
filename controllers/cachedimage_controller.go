/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	dcrenixiov1alpha1 "gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
)

// CachedImageReconciler reconciles a CachedImage object
type CachedImageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=dcr.enix.io,resources=cachedimages,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=dcr.enix.io,resources=cachedimages/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=dcr.enix.io,resources=cachedimages/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CachedImage object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *CachedImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.
		FromContext(ctx)

	log.Info("reconciling cachedimage")

	var cachedImage dcrenixiov1alpha1.CachedImage
	if err := r.Get(ctx, req.NamespacedName, &cachedImage); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// https://book.kubebuilder.io/reference/using-finalizers.html
	finalizerName := "cachedimage.dcr.enix.io/finalizer"
	// Remove image from registry when CachedImage is beeing deleted, finalizer is removed after it
	if !cachedImage.ObjectMeta.DeletionTimestamp.IsZero() {
		if containsString(cachedImage.GetFinalizers(), finalizerName) {
			log.Info("deleting image cache")
			if err := r.deleteExternalResources(&cachedImage); err != nil {
				return ctrl.Result{}, err
			}

			log.Info("removing finalizer")
			controllerutil.RemoveFinalizer(&cachedImage, finalizerName)
			if err := r.Update(ctx, &cachedImage); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// Add finalizer to keep the CachedImage during image removal from registry on deletion
	if !containsString(cachedImage.GetFinalizers(), finalizerName) {
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(&cachedImage, finalizerName)
		if err := r.Update(ctx, &cachedImage); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Update CachedImage UsedBy status
	var podsList corev1.PodList
	if err := r.List(ctx, &podsList, client.MatchingFields{cachedImageOwnerKey: cachedImage.Name}); err != nil {
		return ctrl.Result{}, err
	}
	pods := []v1alpha1.PodReference{}
	for _, pod := range podsList.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		pods = append(pods, v1alpha1.PodReference{NamespacedName: pod.Namespace + "/" + pod.Name})
	}
	cachedImage.Status.UsedBy = v1alpha1.UsedBy{
		Pods:  pods,
		Count: len(pods),
	}
	err := r.Status().Update(context.Background(), &cachedImage)
	if err != nil {
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.Status().Code == http.StatusConflict {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	log = log.WithValues("sourceImage", cachedImage.Spec.SourceImage)

	// Delete expired CachedImage and schedule deletion for expiring ones
	expiresAt := cachedImage.Spec.ExpiresAt
	if !expiresAt.IsZero() {
		if time.Now().After(expiresAt.Time) {
			log.Info("cachedimage expired, deleting it", "now", time.Now(), "expiresAt", expiresAt)
			err := r.Delete(ctx, &cachedImage)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			return ctrl.Result{RequeueAfter: expiresAt.Sub(time.Now())}, nil
		}
	}

	// Adding image to registry
	log.Info("caching image")
	keychain := registry.NewKubernetesKeychain(r.Client, cachedImage.Spec.PullSecretsNamespace, cachedImage.Spec.PullSecretNames)
	if cacheUpdated, err := registry.CacheImage(cachedImage.Spec.SourceImage, keychain); err != nil {
		log.Error(err, "failed to cache image")
		return ctrl.Result{}, err
	} else if cacheUpdated {
		log.Info("image cached")
		if err := r.Get(ctx, req.NamespacedName, &cachedImage); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	} else {
		log.Info("image already cached, cache not updated")
	}

	// Update CachedImage IsCached status
	cachedImage.Status.IsCached = true
	err = r.Status().Update(context.Background(), &cachedImage)
	if err != nil {
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.Status().Code == http.StatusConflict {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("reconciled cachedimage")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CachedImageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create an index to list Pods by CachedImage
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, cachedImageOwnerKey, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)
		if _, ok := pod.Labels[LabelImageRewrittenName]; !ok {
			return []string{}
		}

		logger := mgr.GetLogger().
			WithName("indexer.cachedimage.pods").
			WithValues("pod", klog.KObj(pod))
		ctx := logr.NewContext(context.Background(), logger)

		cachedImages := desiredCachedImages(ctx, pod)

		cachedImageNames := make([]string, len(cachedImages))
		for _, cachedImage := range cachedImages {
			cachedImageNames = append(cachedImageNames, cachedImage.Name)
		}

		return cachedImageNames
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&dcrenixiov1alpha1.CachedImage{}).
		Complete(r)
}

func (r *CachedImageReconciler) deleteExternalResources(cachedImage *dcrenixiov1alpha1.CachedImage) error {
	err := registry.DeleteImage(cachedImage.Spec.SourceImage)
	if err, ok := err.(*transport.Error); ok {
		if err.StatusCode == http.StatusNotFound {
			return nil
		}
	}
	return err
}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
