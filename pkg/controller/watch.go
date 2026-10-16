/*
Copyright 2026.

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

package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
)

// BroadcastListMapper returns a map function that enqueues every object in the
// provided list type whenever the watched dependency changes.
//
// List failures cannot be returned from MapFunc; they are logged and the
// event is dropped. Dependents still heal via their default requeue.
func BroadcastListMapper(
	cli client.Client,
	prototype client.ObjectList,
) handler.MapFunc {
	log := ctrl.Log.WithName("broadcast-list-mapper").WithValues("listType", fmt.Sprintf("%T", prototype))
	return func(ctx context.Context, _ client.Object) []reconcile.Request {
		list, ok := prototype.DeepCopyObject().(client.ObjectList)
		if !ok {
			log.Error(nil, "prototype is not a client.ObjectList")
			return nil
		}
		if err := cli.List(ctx, list); err != nil {
			log.Error(err, "failed to list dependents for broadcast enqueue")
			return nil
		}

		items, err := metav1.ExtractList(list)
		if err != nil {
			log.Error(err, "failed to extract dependents for broadcast enqueue")
			return nil
		}

		reqs := make([]reconcile.Request, 0, len(items))
		for _, item := range items {
			obj, ok := item.(client.Object)
			if !ok {
				continue
			}
			reqs = append(reqs, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(obj),
			})
		}
		return reqs
	}
}

// EffectiveProviderName returns the DatabaseProvider a claim is bound to or
// requesting: spec.provider.name when set, otherwise status.provider.
// Handles typed claims and unstructured watch events (framework watches use
// unstructured objects).
func EffectiveProviderName(obj client.Object) string {
	switch claim := obj.(type) {
	case *infraApi.DatabaseClaim:
		if claim.Spec.Provider.Name != "" {
			return claim.Spec.Provider.Name
		}
		return claim.Status.Provider
	case *infraApi.SchemaClaim:
		if claim.Spec.Provider.Name != "" {
			return claim.Spec.Provider.Name
		}
		return claim.Status.Provider
	case *unstructured.Unstructured:
		if name, _, _ := unstructured.NestedString(claim.Object, "spec", "provider", "name"); name != "" {
			return name
		}
		name, _, _ := unstructured.NestedString(claim.Object, "status", "provider")
		return name
	default:
		return ""
	}
}

// ClaimToProviderMapper enqueues the cluster-scoped DatabaseProvider named by
// a SchemaClaim or DatabaseClaim. Claims with no resolvable provider are
// ignored so unrelated events do not fan out to every provider.
func ClaimToProviderMapper() handler.MapFunc {
	return func(_ context.Context, obj client.Object) []reconcile.Request {
		name := EffectiveProviderName(obj)
		if name == "" {
			return nil
		}
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{Name: name},
		}}
	}
}
