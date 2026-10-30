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

package controller_test

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/predicates/hash"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
)

func TestBroadcastListMapper(t *testing.T) {
	g := NewWithT(t)

	claim1 := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-1", Namespace: "ns-1"},
	}
	claim2 := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-2", Namespace: "ns-2"},
	}

	cli := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(claim1, claim2).Build()
	mapper := controller.BroadcastListMapper(cli, &infraApi.DatabaseClaimList{})

	reqs := mapper(context.Background(), nil)
	g.Expect(reqs).To(ConsistOf(
		reconcile.Request{NamespacedName: client.ObjectKeyFromObject(claim1)},
		reconcile.Request{NamespacedName: client.ObjectKeyFromObject(claim2)},
	))
}

func TestClaimWatchEventHandler(t *testing.T) {
	newQueue := func() workqueue.TypedRateLimitingInterface[reconcile.Request] {
		return workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	}
	drain := func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) []reconcile.Request {
		var reqs []reconcile.Request
		for q.Len() > 0 {
			item, shutdown := q.Get()
			if shutdown {
				break
			}
			q.Done(item)
			reqs = append(reqs, item)
		}
		q.ShutDown()
		return reqs
	}
	requestFor := func(name string) reconcile.Request {
		return reconcile.Request{NamespacedName: client.ObjectKey{Name: name}}
	}

	namedDatabaseClaim := func(provider string) *infraApi.DatabaseClaim {
		return &infraApi.DatabaseClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "claim-a", Namespace: "app"},
			Spec:       infraApi.DatabaseClaimSpec{Provider: infraApi.ProviderRef{Name: provider}},
		}
	}
	selectorSchemaClaim := func(statusProvider string) *infraApi.SchemaClaim {
		return &infraApi.SchemaClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "claim-b", Namespace: "app"},
			Spec: infraApi.SchemaClaimSpec{Provider: infraApi.ProviderRef{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "shared"}},
			}},
			Status: infraApi.SchemaClaimStatus{Provider: statusProvider},
		}
	}
	unstructuredClaim := func(specName, statusName string) *unstructured.Unstructured {
		obj := map[string]any{}
		if specName != "" || statusName != "" {
			obj["spec"] = map[string]any{"provider": map[string]any{"name": specName}}
			obj["status"] = map[string]any{"provider": statusName}
		}
		return &unstructured.Unstructured{Object: obj}
	}

	t.Run("create enqueues the named provider", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: namedDatabaseClaim("rhai-db")}, q)
		g.Expect(drain(q)).To(Equal([]reconcile.Request{requestFor("rhai-db")}))
	})

	t.Run("create enqueues the status-bound provider of a selector claim", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: selectorSchemaClaim("external-pg")}, q)
		g.Expect(drain(q)).To(Equal([]reconcile.Request{requestFor("external-pg")}))
	})

	t.Run("create with no resolvable provider enqueues nothing", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: namedDatabaseClaim("")}, q)
		g.Expect(drain(q)).To(BeEmpty())
	})

	t.Run("create of a non-claim object enqueues nothing", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		notAClaim := &infraApi.DatabaseProvider{ObjectMeta: metav1.ObjectMeta{Name: "not-a-claim"}}
		h.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: notAClaim}, q)
		g.Expect(drain(q)).To(BeEmpty())
	})

	t.Run("update rebinding a selector claim enqueues both providers", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Update(context.Background(), event.TypedUpdateEvent[client.Object]{
			ObjectOld: selectorSchemaClaim("internal-a"),
			ObjectNew: selectorSchemaClaim("internal-b"),
		}, q)
		g.Expect(drain(q)).To(ConsistOf(requestFor("internal-a"), requestFor("internal-b")))
	})

	t.Run("update unbinding a selector claim enqueues the abandoned provider", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Update(context.Background(), event.TypedUpdateEvent[client.Object]{
			ObjectOld: selectorSchemaClaim("internal-a"),
			ObjectNew: selectorSchemaClaim(""),
		}, q)
		g.Expect(drain(q)).To(Equal([]reconcile.Request{requestFor("internal-a")}))
	})

	t.Run("update deduplicates an unchanged provider", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Update(context.Background(), event.TypedUpdateEvent[client.Object]{
			ObjectOld: namedDatabaseClaim("rhai-db"),
			ObjectNew: namedDatabaseClaim("rhai-db"),
		}, q)
		g.Expect(drain(q)).To(Equal([]reconcile.Request{requestFor("rhai-db")}))
	})

	t.Run("update with a nil old object maps the new object", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Update(context.Background(), event.TypedUpdateEvent[client.Object]{
			ObjectNew: selectorSchemaClaim("internal-a"),
		}, q)
		g.Expect(drain(q)).To(Equal([]reconcile.Request{requestFor("internal-a")}))
	})

	t.Run("delete enqueues the claim's provider", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Delete(context.Background(), event.TypedDeleteEvent[client.Object]{Object: selectorSchemaClaim("internal-a")}, q)
		g.Expect(drain(q)).To(Equal([]reconcile.Request{requestFor("internal-a")}))
	})

	t.Run("delete with no resolvable provider enqueues nothing", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Delete(context.Background(), event.TypedDeleteEvent[client.Object]{Object: namedDatabaseClaim("")}, q)
		g.Expect(drain(q)).To(BeEmpty())
	})

	t.Run("generic events enqueue nothing", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Generic(context.Background(), event.TypedGenericEvent[client.Object]{Object: namedDatabaseClaim("rhai-db")}, q)
		g.Expect(drain(q)).To(BeEmpty())
	})

	t.Run("unstructured create maps the spec provider", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		fromUnstructured := unstructuredClaim("from-unstructured", "")
		h.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: fromUnstructured}, q)
		g.Expect(drain(q)).To(Equal([]reconcile.Request{requestFor("from-unstructured")}))
	})

	t.Run("unstructured rebind enqueues both providers", func(t *testing.T) {
		g := NewWithT(t)
		h := controller.ClaimWatchEventHandler()
		q := newQueue()
		h.Update(context.Background(), event.TypedUpdateEvent[client.Object]{
			ObjectOld: unstructuredClaim("", "internal-a"),
			ObjectNew: unstructuredClaim("", "internal-b"),
		}, q)
		g.Expect(drain(q)).To(ConsistOf(requestFor("internal-a"), requestFor("internal-b")))
	})
}

// TestSecretDriftPredicate pins the predicate the claim controllers use for
// Owns(&Secret): hash.Updated() must pass content changes and filter
// resourceVersion-only bumps. Secrets never increment metadata.generation, so
// the framework's default (generation-based) predicate would pass every
// update; a regression to it makes this test fail.

// TestOwnerReferencesChangedPredicate pins the second Owns(Secret) predicate:
// ownerReference changes must enqueue (resources.Hash strips them, so
// hash.Updated() is blind to the controller owner being removed or swapped).
func ptrTrue() *bool {
	v := true
	return &v
}

func TestOwnerReferencesChangedPredicate(t *testing.T) {
	g := NewWithT(t)
	pred := controller.OwnerReferencesChanged()

	owner := func(uid string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{Kind: "DatabaseClaim", Name: "c", UID: types.UID(uid), Controller: ptrTrue()}}
	}
	secret := func(uid string, rv string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "creds",
				Namespace:       "app",
				OwnerReferences: owner(uid),
				ResourceVersion: rv,
			},
		}
	}

	g.Expect(pred.Update(event.TypedUpdateEvent[client.Object]{
		ObjectOld: secret("claim-uid", "1"),
		ObjectNew: secret("claim-uid", "2"),
	})).To(BeFalse(), "resourceVersion-only change must not enqueue")

	g.Expect(pred.Update(event.TypedUpdateEvent[client.Object]{
		ObjectOld: secret("claim-uid", "1"),
		ObjectNew: secret("other-uid", "2"),
	})).To(BeTrue(), "a swapped controller owner must enqueue")

	g.Expect(pred.Update(event.TypedUpdateEvent[client.Object]{
		ObjectOld: secret("claim-uid", "1"),
		ObjectNew: func() *corev1.Secret {
			s := secret("claim-uid", "2")
			s.OwnerReferences = nil
			return s
		}(),
	})).To(BeTrue(), "a removed controller owner must enqueue")
}

func TestSecretDriftPredicate(t *testing.T) {
	g := NewWithT(t)
	pred := hash.Updated()

	secret := func(password, resourceVersion string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "creds",
				Namespace:       "app",
				ResourceVersion: resourceVersion,
			},
			Data: map[string][]byte{"password": []byte(password)},
		}
	}

	g.Expect(pred.Create(event.TypedCreateEvent[client.Object]{Object: secret("pw", "1")})).To(BeTrue(),
		"secret creation must enqueue")

	g.Expect(pred.Delete(event.TypedDeleteEvent[client.Object]{Object: secret("pw", "1")})).To(BeTrue(),
		"secret deletion must enqueue")

	g.Expect(pred.Update(event.TypedUpdateEvent[client.Object]{
		ObjectOld: secret("pw", "1"),
		ObjectNew: secret("changed", "2"),
	})).To(BeTrue(), "a password change must enqueue")

	g.Expect(pred.Update(event.TypedUpdateEvent[client.Object]{
		ObjectOld: secret("pw", "1"),
		ObjectNew: secret("pw", "2"),
	})).To(BeFalse(), "a resourceVersion-only bump must not enqueue")
}

func TestEffectiveProviderName(t *testing.T) {
	g := NewWithT(t)

	named := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-a", Namespace: "app"},
		Spec:       infraApi.DatabaseClaimSpec{Provider: infraApi.ProviderRef{Name: "by-name"}},
		Status:     infraApi.DatabaseClaimStatus{Provider: "by-status"},
	}
	g.Expect(controller.EffectiveProviderName(named)).To(Equal("by-name"))

	statusBound := &infraApi.SchemaClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-b", Namespace: "app"},
		Status:     infraApi.SchemaClaimStatus{Provider: "external-pg"},
	}
	g.Expect(controller.EffectiveProviderName(statusBound)).To(Equal("external-pg"))

	unbound := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-c", Namespace: "app"},
	}
	g.Expect(controller.EffectiveProviderName(unbound)).To(BeEmpty())
}
