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
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
)

func TestShouldDropClaimDatabase_RequiresCreatedAndDelete(t *testing.T) {
	g := NewWithT(t)
	claim := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns", UID: types.UID("uid-a")},
		Spec: infraApi.DatabaseClaimSpec{
			Provider:       infraApi.ProviderRef{Name: "rhai-db"},
			Database:       "analytics",
			DeletionPolicy: infraApi.DeletionPolicyDelete,
		},
		Status: infraApi.DatabaseClaimStatus{
			Provider:        "rhai-db",
			Database:        "analytics",
			DatabaseCreated: true,
		},
	}

	g.Expect(controller.ShouldDropClaimDatabase(claim, "postgres", nil)).To(BeTrue())

	retain := claim.DeepCopy()
	retain.Spec.DeletionPolicy = infraApi.DeletionPolicyRetain
	g.Expect(controller.ShouldDropClaimDatabase(retain, "postgres", nil)).To(BeFalse())

	notCreated := claim.DeepCopy()
	notCreated.Status.DatabaseCreated = false
	g.Expect(controller.ShouldDropClaimDatabase(notCreated, "postgres", nil)).To(BeFalse())

	defaultDB := claim.DeepCopy()
	defaultDB.Spec.Database = "postgres"
	defaultDB.Status.Database = "postgres"
	g.Expect(controller.ShouldDropClaimDatabase(defaultDB, "postgres", nil)).To(BeFalse())
}

func TestShouldDropClaimDatabase_BlocksWhenShared(t *testing.T) {
	g := NewWithT(t)
	claim := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns", UID: types.UID("uid-a")},
		Spec: infraApi.DatabaseClaimSpec{
			Provider:       infraApi.ProviderRef{Name: "rhai-db"},
			Database:       "analytics",
			DeletionPolicy: infraApi.DeletionPolicyDelete,
		},
		Status: infraApi.DatabaseClaimStatus{
			Provider:        "rhai-db",
			Database:        "analytics",
			DatabaseCreated: true,
		},
	}
	other := infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "other-ns", UID: types.UID("uid-b")},
		Spec: infraApi.DatabaseClaimSpec{
			Provider: infraApi.ProviderRef{Name: "rhai-db"},
			Database: "analytics",
		},
		Status: infraApi.DatabaseClaimStatus{
			Provider: "rhai-db",
			Database: "analytics",
		},
	}

	g.Expect(controller.ShouldDropClaimDatabase(claim, "postgres", []infraApi.DatabaseClaim{other})).To(BeFalse())
}
