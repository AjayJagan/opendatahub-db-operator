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

// Package databaseservice tests reconciliation against a real cluster in a separate test process.
package databaseservice

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/reconciler"

	servicesv1alpha1 "github.com/opendatahub-io/opendatahub-db-operator/api/services/v1alpha1"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	modulemanager "github.com/opendatahub-io/opendatahub-db-operator/pkg/manager"
	"github.com/opendatahub-io/opendatahub-db-operator/test/support"
)

const testNamespacePrefix = "opendatahub-db-operator-databaseservice-it"

// TestDatabaseServiceReconcilesToReady verifies status writes against a connected cluster.
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
			if err := support.DeleteNamespace(context.Background(), mgr.GetClient(), testNamespace, nsUID); err != nil {
				t.Errorf("cleaning up test namespace: %v", err)
			}
		})
	}

	cli := mgr.GetClient()

	// This cluster-scoped singleton requires exclusive use: no deployed
	// operator or concurrent test manager may reconcile it during this test.
	instance := &servicesv1alpha1.DatabaseService{
		ObjectMeta: metav1.ObjectMeta{
			Name: servicesv1alpha1.DatabaseServiceInstanceName,
		},
	}
	if err := cli.Create(ctx, instance); err != nil {
		t.Fatalf(
			"creating the singleton DatabaseService: %v; this test requires exclusive use of "+
				"default-db-operator and will not reuse or delete it",
			err,
		)
	}

	createdUID := instance.UID
	t.Cleanup(func() {
		err := cli.Delete(context.Background(), &servicesv1alpha1.DatabaseService{
			ObjectMeta: metav1.ObjectMeta{
				Name: servicesv1alpha1.DatabaseServiceInstanceName,
				UID:  createdUID,
			},
		}, client.Preconditions{UID: &createdUID})
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			t.Errorf("cleaning up test DatabaseService singleton: %v", err)
		}
	})

	managerDone := make(chan struct{})
	var managerErr error
	go func() {
		managerErr = mgr.Start(ctx)
		close(managerDone)
	}()
	t.Cleanup(func() {
		cancel()
		<-managerDone
		g.Expect(managerErr).NotTo(HaveOccurred(), "manager failed while stopping")
	})

	g.Expect(mgr.GetCache().WaitForCacheSync(ctx)).To(BeTrue(), "manager cache failed to sync")

	select {
	case <-mgr.Elected():
	case <-managerDone:
		t.Fatalf("manager stopped before being elected: %v", managerErr)
	case <-time.After(gomegaCfg.EventuallyTimeout):
		t.Fatal("timed out waiting for leader election")
	}

	g.Eventually(func(g Gomega) {
		select {
		case <-managerDone:
			g.Expect(managerErr).NotTo(HaveOccurred(), "manager stopped unexpectedly")
		default:
		}

		got := &servicesv1alpha1.DatabaseService{}
		g.Expect(cli.Get(ctx, client.ObjectKeyFromObject(instance), got)).To(Succeed())
		g.Expect(got.Status.Phase).To(Equal(reconciler.DefaultPhaseReady))

		readyCond := conditions.FindStatusCondition(got, string(fwapi.ConditionTypeReady))
		g.Expect(readyCond).NotTo(BeNil(), "no Ready condition on status.conditions")
		g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		release := got.Status.ComponentReleaseStatus.GetRelease(moduleconfig.ReleasePlatform)
		g.Expect(release).NotTo(BeNil(), "no platform release in status.releases")
		g.Expect(release.Version).To(Equal(cfg.ComponentRelease().Version))
	}).Should(Succeed())
}
