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

// Package providernetworkpolicy runs in a separate test process because
// controller-runtime rejects duplicate controller names across managers in
// one process. Only the DatabaseProvider reconciler is registered so the
// claim controller cannot overwrite the synthetic Provisioned status.
package providernetworkpolicy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	"github.com/opendatahub-io/opendatahub-db-operator/internal/controller/databaseprovider"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	dbcontroller "github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
	modulemanager "github.com/opendatahub-io/opendatahub-db-operator/pkg/manager"
	"github.com/opendatahub-io/opendatahub-db-operator/test/support"
)

// TestInternalNetworkPolicyUpdatesWhenClaimProvisioned verifies claim watches
// enqueue the provider so NetworkPolicy ingress namespaces update as soon as
// a DatabaseClaim becomes Provisioned (not only on periodic retry).
func TestInternalNetworkPolicyUpdatesWhenClaimProvisioned(t *testing.T) {
	ctrl.SetLogger(logr.Discard())

	gomegaCfg := support.LoadGomegaConfig()
	SetDefaultEventuallyTimeout(gomegaCfg.EventuallyTimeout)
	SetDefaultEventuallyPollingInterval(gomegaCfg.EventuallyPollingInterval)
	SetDefaultConsistentlyPollingInterval(gomegaCfg.ConsistentlyPollingInterval)
	g := NewWithT(t)

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	restCfg, err := env.Start()
	g.Expect(err).NotTo(HaveOccurred(),
		"starting envtest -- KUBEBUILDER_ASSETS must point at a directory with etcd/kube-apiserver "+
			"(see `setup-envtest use -p path`); `make test-envtest` sets this up automatically")
	t.Cleanup(func() {
		g.Expect(env.Stop()).To(Succeed())
	})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cfg, err := moduleconfig.Load()
	g.Expect(err).NotTo(HaveOccurred())
	// Keep periodic requeue longer than Eventually so a pass proves the watch fired.
	cfg.DatabaseProvider.RetryInterval = gomegaCfg.EventuallyTimeout * 2

	scheme, err := modulemanager.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(databaseprovider.NewReconciler(ctx, mgr, cfg, databaseprovider.Options{
		Recorder: mgr.GetEventRecorder(infraApi.DatabaseProviderResource),
	})).To(Succeed())

	managerDone := make(chan struct{})
	var managerErr error
	go func() {
		managerErr = mgr.Start(ctx)
		close(managerDone)
	}()
	t.Cleanup(func() {
		cancel()
		<-managerDone
		if managerErr != nil {
			t.Errorf("manager failed while stopping: %v", managerErr)
		}
	})
	g.Expect(mgr.GetCache().WaitForCacheSync(ctx)).To(BeTrue())

	cli := mgr.GetClient()
	g.Expect(cli.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "database-system"}})).To(Succeed())
	g.Expect(cli.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "claim-workload"}})).To(Succeed())

	provider := &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "internal"},
		Spec: infraApi.DatabaseProviderSpec{
			Type: infraApi.ProviderTypeInternal,
			Internal: &infraApi.InternalProviderSpec{
				Namespace: "database-system",
				Storage:   infraApi.StorageSpec{Size: resource.MustParse("1Gi")},
			},
		},
	}
	g.Expect(cli.Create(ctx, provider)).To(Succeed())

	internalKey := client.ObjectKey{Namespace: "database-system", Name: "internal"}
	g.Eventually(func(g Gomega) {
		sts := &appsv1.StatefulSet{}
		g.Expect(cli.Get(ctx, internalKey, sts)).To(Succeed())
		policy := &networkingv1.NetworkPolicy{}
		g.Expect(cli.Get(ctx, internalKey, policy)).To(Succeed())
		g.Expect(policy.Spec.Ingress).To(HaveLen(1))
		g.Expect(policy.Spec.Ingress[0].From).To(HaveLen(1))
	}).Should(Succeed())

	claim := &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "analytics", Namespace: "claim-workload"},
		Spec: infraApi.DatabaseClaimSpec{
			Provider: infraApi.ProviderRef{Name: "internal"},
			Database: "analytics",
		},
	}
	g.Expect(cli.Create(ctx, claim)).To(Succeed())
	claim.Status.Provider = "internal"
	claim.Status.Conditions = []fwapi.Condition{{
		Type:               dbcontroller.ConditionProvisioned,
		Status:             metav1.ConditionTrue,
		Reason:             "UserProvisioned",
		LastTransitionTime: metav1.Now(),
	}}
	g.Expect(cli.Status().Update(ctx, claim)).To(Succeed())

	g.Eventually(func(g Gomega) {
		policy := &networkingv1.NetworkPolicy{}
		g.Expect(cli.Get(ctx, internalKey, policy)).To(Succeed())
		g.Expect(policy.Spec.Ingress).To(HaveLen(1))
		g.Expect(policy.Spec.Ingress[0].From).To(ContainElement(
			networkingv1.NetworkPolicyPeer{
				NamespaceSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key:      "kubernetes.io/metadata.name",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"claim-workload"},
					}},
				},
			},
		))
	}).Should(Succeed())
}
