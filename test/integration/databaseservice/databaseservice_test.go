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

// Package databaseservice holds the real-cluster reconciler test for
// DatabaseService. It's a separate package (and therefore a separate `go
// test` binary/process) from test/integration's manager-startup suite: both
// tests build a manager via pkg/manager.New, which registers a
// controller-runtime controller named "databaseservice" in a
// process-global registry -- running both in the same test binary trips
// controller-runtime's duplicate-controller-name validation on the second
// manager. Separate packages give each its own process, avoiding that
// collision without needing SkipNameValidation or any change to
// pkg/manager itself.
package databaseservice

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/reconciler"

	servicesv1alpha1 "github.com/opendatahub-io/opendatahub-db-operator/api/services/v1alpha1"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	modulemanager "github.com/opendatahub-io/opendatahub-db-operator/pkg/manager"
	"github.com/opendatahub-io/opendatahub-db-operator/test/support"
)

const testNamespacePrefix = "opendatahub-db-operator-databaseservice-it"

// TestDatabaseServiceReconcilesToReady is the real-cluster half of phase 2's
// verification ladder (see docs/plan.md phase 2, verification rung 2): the
// envtest suite in test/envtest proves the reconciler against envtest's fake
// apiserver, but per the PoC's own convention that's not sufficient on its
// own -- envtest wouldn't catch anything that only breaks against a real
// apiserver (real RBAC enforcement, a real etcd, real CRD conversion). This
// test applies the generated CRD (via `make test-integration-setup`, run
// before this suite), starts a real manager against the current kubeconfig
// context exactly like TestManagerStartsAndBecomesHealthy does, creates the
// default-db-operator singleton, and asserts it reaches Ready.
func TestDatabaseServiceReconcilesToReady(t *testing.T) {
	g := NewWithT(t)

	ctrl.SetLogger(logr.Discard())

	gomegaCfg := support.LoadGomegaConfig()
	SetDefaultEventuallyTimeout(gomegaCfg.EventuallyTimeout)
	SetDefaultEventuallyPollingInterval(gomegaCfg.EventuallyPollingInterval)
	SetDefaultConsistentlyPollingInterval(gomegaCfg.ConsistentlyPollingInterval)

	restCfg, err := ctrl.GetConfig()
	g.Expect(err).NotTo(HaveOccurred(), "no kubeconfig / current context -- this test needs a real, connected cluster")

	// Fail clearly, rather than hanging in Eventually(), if the CRD this
	// test depends on isn't installed -- `make test-integration-setup`
	// applies it; running this test file directly (e.g. `go test
	// ./test/integration/...`) without that step first would otherwise
	// surface as a confusing not-ready timeout instead of a clear cause.
	scheme, err := modulemanager.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	discoveryCli, err := client.New(restCfg, client.Options{Scheme: scheme})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(discoveryCli.List(context.Background(), &servicesv1alpha1.DatabaseServiceList{})).
		To(Succeed(), "listing DatabaseService failed -- is the CRD installed? run `make test-integration-setup` first")

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	testNamespace := fmt.Sprintf("%s-%d", testNamespacePrefix, time.Now().UnixNano())

	cfg, err := moduleconfig.Load()
	g.Expect(err).NotTo(HaveOccurred())

	cfg.OperatorNamespace = testNamespace
	cfg.Controller.Metrics.BindAddress = "0"
	cfg.Controller.Health.BindAddress = "0"
	cfg.Controller.Pprof.BindAddress = "0"
	cfg.Controller.LeaderElection.Enabled = true
	cfg.Controller.LeaderElection.ID = "opendatahub-db-operator-databaseservice-it-lock"

	mgr, err := modulemanager.New(ctx, restCfg, cfg)
	g.Expect(err).NotTo(HaveOccurred())

	nsUID, created, err := support.EnsureNamespace(ctx, mgr.GetClient(), testNamespace)
	g.Expect(err).NotTo(HaveOccurred())
	if created {
		t.Cleanup(func() {
			_ = support.DeleteNamespace(context.Background(), mgr.GetClient(), testNamespace, nsUID)
		})
	}

	managerErrCh := make(chan error, 1)
	go func() {
		managerErrCh <- mgr.Start(ctx)
	}()

	g.Expect(mgr.GetCache().WaitForCacheSync(ctx)).To(BeTrue(), "manager cache failed to sync")

	select {
	case <-mgr.Elected():
	case err := <-managerErrCh:
		t.Fatalf("manager stopped before being elected: %v", err)
	case <-time.After(gomegaCfg.EventuallyTimeout):
		t.Fatal("timed out waiting for leader election")
	}

	cli := mgr.GetClient()

	// DatabaseService is a cluster-scoped singleton -- CEL-enforced to the
	// name below -- so, unlike the per-run namespace, this test can't create
	// a uniquely-named instance of its own. Fail clearly rather than
	// clobbering state this run doesn't own if one already exists (e.g. a
	// concurrent run, or an operator already deployed on this cluster).
	instance := &servicesv1alpha1.DatabaseService{
		ObjectMeta: metav1.ObjectMeta{
			Name: servicesv1alpha1.DatabaseServiceInstanceName,
		},
	}
	g.Expect(cli.Create(ctx, instance)).To(Succeed(),
		"creating the singleton DatabaseService -- if this fails with AlreadyExists, "+
			"another default-db-operator CR is already on this cluster (e.g. a deployed operator "+
			"or a concurrent test run); this test does not attempt to reuse or delete it")

	createdUID := instance.UID
	t.Cleanup(func() {
		_ = cli.Delete(context.Background(), &servicesv1alpha1.DatabaseService{
			ObjectMeta: metav1.ObjectMeta{
				Name: servicesv1alpha1.DatabaseServiceInstanceName,
				UID:  createdUID,
			},
		}, client.Preconditions{UID: &createdUID})
	})

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
