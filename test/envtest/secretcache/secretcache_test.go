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

// Package secretcache verifies the label-scoped Secret informer
// (RHOAIENG-96275): Secrets without the operator's managed label never enter
// the cache, while reads that gate decisions go through the manager's API
// reader. Runs in a separate test process because controller-runtime rejects
// duplicate controller names across managers in one process; only the
// DatabaseProvider reconciler is registered.
package secretcache

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	resourcelabels "github.com/opendatahub-io/opendatahub-db-operator/pkg/resources/labels"
	"github.com/opendatahub-io/opendatahub-db-operator/test/support"
)

func TestSecretCacheScope(t *testing.T) {
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
	cfg.DatabaseProvider.RetryInterval = gomegaCfg.EventuallyTimeout * 2

	scheme, err := modulemanager.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	// The manager is built with the same scoped cache and Secret-bypassing
	// client the operator uses; an unscoped test manager could not observe
	// the scoping at all.
	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:    scheme,
		Cache:     modulemanager.SecretScopedCache(),
		NewClient: modulemanager.NewClientWithUncachedSecrets,
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
	reader := mgr.GetAPIReader()

	adminSecretData := func(host, port string) map[string][]byte {
		return map[string][]byte{
			"host":     []byte(host),
			"port":     []byte(port),
			"user":     []byte("postgres"),
			"password": []byte("admin-password"),
			"dbname":   []byte("postgres"),
		}
	}

	ensureNamespace := func(g Gomega, name string) {
		err := cli.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
		if err != nil {
			g.Expect(apierrors.IsAlreadyExists(err)).To(BeTrue(), "creating namespace %s: %v", name, err)
		}
	}

	t.Run("unrelated Secret does not enter the cache", func(t *testing.T) {
		g := NewWithT(t)
		ensureNamespace(g, "cache-scope")
		unrelated := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "not-ours", Namespace: "cache-scope"},
			Data:       adminSecretData("unused", "5432"),
		}
		managed := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ours",
				Namespace: "cache-scope",
				Labels:    map[string]string{resourcelabels.ManagedBy: resourcelabels.ManagedByValue},
			},
			Data: adminSecretData("unused", "5432"),
		}
		g.Expect(cli.Create(ctx, unrelated)).To(Succeed())
		g.Expect(cli.Create(ctx, managed)).To(Succeed())

		// The scoped informer itself must admit the managed Secret and not
		// the unlabeled one; the client bypasses the cache for Secret reads
		// and serves both fresh from the API server, which is what keeps
		// reconcilers reading administrator Secrets that carry no operator
		// label.
		g.Eventually(func(g Gomega) {
			g.Expect(mgr.GetCache().Get(ctx, client.ObjectKeyFromObject(managed), &corev1.Secret{})).To(Succeed())
		}).Should(Succeed(), "managed Secret should be admitted by the scoped cache")

		err := mgr.GetCache().Get(ctx, client.ObjectKeyFromObject(unrelated), &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"unlabeled Secret must not be served by the scoped cache, got: %v", err)

		g.Expect(cli.Get(ctx, client.ObjectKeyFromObject(unrelated), &corev1.Secret{})).To(Succeed(),
			"the client must read unlabeled Secrets directly, bypassing the cache")
		g.Expect(reader.Get(ctx, client.ObjectKeyFromObject(unrelated), &corev1.Secret{})).To(Succeed(),
			"the API reader must still see unlabeled Secrets")
	})

	t.Run("external admin Secret is read through the API reader", func(t *testing.T) {
		g := NewWithT(t)
		ensureNamespace(g, "external-admin")
		adminSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "external-admin", Namespace: "external-admin"},
			// Port 1 on the loopback interface: connection refused without
			// any network dependency.
			Data: adminSecretData("127.0.0.1", "1"),
		}
		g.Expect(cli.Create(ctx, adminSecret)).To(Succeed())
		provider := &infraApi.DatabaseProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "external-scoped"},
			Spec: infraApi.DatabaseProviderSpec{
				Type: infraApi.ProviderTypeExternal,
				External: &infraApi.ExternalProviderSpec{
					ConnectionSecretRef: corev1.SecretReference{
						Namespace: "external-admin",
						Name:      "external-admin",
					},
				},
			},
		}
		g.Expect(cli.Create(ctx, provider)).To(Succeed())

		// The client and the API reader both bypass the cache for Secret
		// reads, so both must load the administrator Secret that carries no
		// operator label; the scoped cache alone would report it missing.
		_, err = dbcontroller.LoadProviderConfig(ctx, cli, provider, cfg.OperatorNamespace)
		g.Expect(err).NotTo(HaveOccurred(), "client Secret reads bypass the cache")
		_, err = dbcontroller.LoadProviderConfig(ctx, reader, provider, cfg.OperatorNamespace)
		g.Expect(err).NotTo(HaveOccurred(), "API reader must load the administrator Secret")

		// The provider reconcile gets past the Secret read and fails on the
		// (unreachable) connection instead of reporting the Secret missing.
		g.Eventually(func(g Gomega) {
			current := &infraApi.DatabaseProvider{}
			g.Expect(cli.Get(ctx, client.ObjectKeyFromObject(provider), current)).To(Succeed())
			reachable := conditions.FindStatusCondition(current, "Reachable")
			g.Expect(reachable).NotTo(BeNil())
			g.Expect(reachable.Reason).NotTo(Equal("ConnectionSecretUnavailable"),
				"reconcile must read the admin Secret through the API reader: %s", reachable.Message)
		}).Should(Succeed())
	})

	t.Run("unlabeled pre-existing internal admin Secret upgrades without losing its password", func(t *testing.T) {
		g := NewWithT(t)
		ensureNamespace(g, "database-upgrade")
		key := client.ObjectKey{Namespace: "database-upgrade", Name: "internal-upgrade-admin"}
		// A PVC plus a password-bearing admin Secret (at its derived name,
		// <provider>-admin) is the upgrade state from before the scoped
		// cache: a stale cached read would report the Secret missing, and
		// the password-preservation guard would turn that into a hard error
		// before the instance reconcile could deploy anything.
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "internal-upgrade", Namespace: "database-upgrade"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			},
		}
		adminSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "internal-upgrade-admin", Namespace: "database-upgrade"},
			Data:       adminSecretData("internal.database-upgrade.svc", "5432"),
		}
		g.Expect(cli.Create(ctx, pvc)).To(Succeed())
		g.Expect(cli.Create(ctx, adminSecret)).To(Succeed())

		provider := &infraApi.DatabaseProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "internal-upgrade"},
			Spec: infraApi.DatabaseProviderSpec{
				Type: infraApi.ProviderTypeInternal,
				Internal: &infraApi.InternalProviderSpec{
					Namespace: "database-upgrade",
					Storage:   infraApi.StorageSpec{Size: resource.MustParse("1Gi")},
				},
			},
		}
		g.Expect(cli.Create(ctx, provider)).To(Succeed())

		g.Eventually(func(g Gomega) {
			// The instance rendering proves the reconcile ran past the admin
			// Secret read: a stale cache miss would wedge it on the
			// password-preservation guard before deploying anything.
			sts := &appsv1.StatefulSet{}
			g.Expect(cli.Get(ctx, client.ObjectKey{Namespace: "database-upgrade", Name: "internal-upgrade"}, sts)).To(Succeed())
			current := &corev1.Secret{}
			g.Expect(reader.Get(ctx, key, current)).To(Succeed())
			g.Expect(current.Data).To(HaveKeyWithValue("password", []byte("admin-password")),
				"the pre-existing password must be preserved")
		}).Should(Succeed())
	})
}
