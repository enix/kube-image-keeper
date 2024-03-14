package controllers

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func forceName(c client.Client, ctx context.Context, newName string, obj client.Object, finalizerName string) error {
	log := log.FromContext(ctx)
	kind := obj.GetObjectKind().GroupVersionKind().Kind

	if newName != obj.GetName() {
		newObj := obj.DeepCopyObject().(client.Object)
		oldObj := obj.DeepCopyObject().(client.Object)
		newObj.SetName(newName)

		if err := c.Get(ctx, types.NamespacedName{Name: newName}, oldObj); err != nil {
			if apierrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("recreating %s with an appropriate name", kind), "newName", newName)
				newObj.SetResourceVersion("")
				newObj.SetUID("")
				if err := c.Create(ctx, newObj); err != nil {
					return err
				}
			} else {
				return err
			}
		} else {
			log.Info(fmt.Sprintf("updating %s from %s with an invalid name", kind, kind), "newName", newName)
			newObj.SetResourceVersion(oldObj.GetResourceVersion())
			newObj.SetUID(oldObj.GetUID())
			newObj.SetDeletionGracePeriodSeconds(oldObj.GetDeletionGracePeriodSeconds())
			newObj.SetDeletionTimestamp(oldObj.GetDeletionTimestamp())
			if err := c.Patch(ctx, newObj, client.MergeFrom(oldObj)); err != nil {
				return err
			}
		}
		log.Info(fmt.Sprintf("removing finalizer and deleting %s with an invalid name", kind))
		controllerutil.RemoveFinalizer(obj, finalizerName)
		if err := c.Update(ctx, obj); err != nil {
			return err
		}
		if err := c.Delete(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}
