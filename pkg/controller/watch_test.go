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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestClaimToProviderMapper_EnqueuesBoundProviderOnly(t *testing.T) {
	g := NewWithT(t)
	mapper := controller.ClaimToProviderMapper()

	named := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-a", Namespace: "app"},
		Spec:       infraApi.DatabaseClaimSpec{Provider: infraApi.ProviderRef{Name: "rhai-db"}},
	}
	g.Expect(mapper(context.Background(), named)).To(Equal([]reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: "rhai-db"},
	}}))

	statusBound := &infraApi.SchemaClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-b", Namespace: "app"},
		Status:     infraApi.SchemaClaimStatus{Provider: "external-pg"},
	}
	g.Expect(mapper(context.Background(), statusBound)).To(Equal([]reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: "external-pg"},
	}}))

	unbound := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-c", Namespace: "app"},
	}
	g.Expect(mapper(context.Background(), unbound)).To(BeEmpty())

	other := &infraApi.DatabaseProvider{ObjectMeta: metav1.ObjectMeta{Name: "not-a-claim"}}
	g.Expect(mapper(context.Background(), other)).To(BeEmpty())

	// Framework claim watches deliver unstructured events.
	u := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"provider": map[string]any{"name": "from-unstructured"},
		},
	}}
	g.Expect(mapper(context.Background(), u)).To(Equal([]reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: "from-unstructured"},
	}}))
}
