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

// Package envtest holds this module's envtest-backed reconciler suite: it
// runs the real manager (pkg/manager.New, the same entry point
// cmd/operator/operator.go uses) against envtest's fake apiserver with the
// DatabaseService CRD installed from config/crd/bases, and proves the
// two-action reconciler (UpgradeIfNeeded + reportStatus) drives a
// default-db-operator singleton CR to Ready.
//
// This is deliberately a separate package from test/integration, which (per
// its own doc comment) is reserved for tests that need a real, connected
// cluster -- envtest's fake apiserver would not exercise what those tests
// exist to catch (real RBAC enforcement, a real etcd, etc.), and conversely
// this suite doesn't need any of that, so it also doesn't need a live
// cluster or kubeconfig. Keeping it out of test/integration also keeps it
// out of `go test ./test/integration/...`, which test-integration-run uses
// against a real cluster -- an envtest suite living there would otherwise
// get swept into that invocation.
//
// Requires KUBEBUILDER_ASSETS to point at a directory containing etcd and
// kube-apiserver binaries (see `setup-envtest use -p path`); `make
// test-envtest` wires this up automatically.
package envtest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/reconciler"

	servicesv1alpha1 "github.com/opendatahub-io/opendatahub-db-operator/api/services/v1alpha1"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	modulemanager "github.com/opendatahub-io/opendatahub-db-operator/pkg/manager"
	"github.com/opendatahub-io/opendatahub-db-operator/test/support"
)

// TestDatabaseServiceReconcilesToReady proves the DatabaseService reconciler
// wired into pkg/manager.New drives the default-db-operator singleton to
// status.phase Ready with a True Ready condition, against envtest's fake
// apiserver with the generated CRD installed -- the envtest half of phase
// 2's verification ladder (see docs/plan.md phase 2, verification rung 2).
// The real-cluster half lives in test/integration.
func TestDatabaseServiceReconcilesToReady(t *testing.T) {
	g := NewWithT(t)

	ctrl.SetLogger(logr.Discard())

	gomegaCfg := support.LoadGomegaConfig()
	SetDefaultEventuallyTimeout(gomegaCfg.EventuallyTimeout)
	SetDefaultEventuallyPollingInterval(gomegaCfg.EventuallyPollingInterval)
	SetDefaultConsistentlyPollingInterval(gomegaCfg.ConsistentlyPollingInterval)

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
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

	// envtest is a single, short-lived process -- leader election (which
	// needs a Lease in an existing namespace) and the metrics/health/pprof
	// listeners aren't needed to exercise the reconciler.
	cfg.Controller.LeaderElection.Enabled = false
	cfg.Controller.Metrics.BindAddress = "0"
	cfg.Controller.Health.BindAddress = "0"
	cfg.Controller.Pprof.BindAddress = "0"

	mgr, err := modulemanager.New(ctx, restCfg, cfg)
	g.Expect(err).NotTo(HaveOccurred())

	managerErrCh := make(chan error, 1)
	go func() {
		managerErrCh <- mgr.Start(ctx)
	}()

	g.Expect(mgr.GetCache().WaitForCacheSync(ctx)).To(BeTrue(), "manager cache failed to sync")

	cli := mgr.GetClient()

	instance := &servicesv1alpha1.DatabaseService{
		ObjectMeta: metav1.ObjectMeta{
			Name: servicesv1alpha1.DatabaseServiceInstanceName,
		},
	}
	g.Expect(cli.Create(ctx, instance)).To(Succeed())

	g.Eventually(func(g Gomega) {
		select {
		case err := <-managerErrCh:
			g.Expect(err).NotTo(HaveOccurred(), "manager stopped unexpectedly")
		default:
		}

		got := &servicesv1alpha1.DatabaseService{}
		g.Expect(cli.Get(ctx, client.ObjectKeyFromObject(instance), got)).To(Succeed())
		g.Expect(got.Status.Phase).To(Equal(reconciler.DefaultPhaseReady))

		readyCond := readyCondition(got.Status.Conditions)
		g.Expect(readyCond).NotTo(BeNil(), "no Ready condition on status.conditions")
		g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
	}).Should(Succeed())
}

func readyCondition(conditions []fwapi.Condition) *fwapi.Condition {
	for i := range conditions {
		if conditions[i].Type == string(fwapi.ConditionTypeReady) {
			return &conditions[i]
		}
	}

	return nil
}
