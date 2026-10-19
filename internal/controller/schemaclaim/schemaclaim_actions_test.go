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

package schemaclaim

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/gomega"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	odherrors "github.com/opendatahub-io/odh-platform-utilities/framework/controller/actions/errors"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	odhtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	dbcontroller "github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/postgres"
)

func TestProvisionAction_PersistsProviderBindingBeforeEnsure(t *testing.T) {
	g := NewWithT(t)

	provider := &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "rhai-db"},
		Spec: infraApi.DatabaseProviderSpec{
			Type: infraApi.ProviderTypeInternal,
			Internal: &infraApi.InternalProviderSpec{
				Namespace: "database-system",
			},
		},
		Status: infraApi.DatabaseProviderStatus{
			Status: fwapi.Status{
				Conditions: []fwapi.Condition{{
					Type:   "Reachable",
					Status: metav1.ConditionTrue,
				}},
			},
		},
	}
	claim := &infraApi.SchemaClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "analytics", Namespace: "app"},
		Spec: infraApi.SchemaClaimSpec{
			Provider: infraApi.ProviderRef{Name: "rhai-db"},
			Schema:   "app_schema",
		},
	}

	cli := fake.NewClientBuilder().WithScheme(claimTestScheme(t)).WithObjects(provider, claim).Build()
	rr := claimTestRequest(t, cli, claim)
	m := &Controller{Options: Options{cfg: &moduleconfig.Config{}}}

	err := m.provisionAction(context.Background(), rr)
	g.Expect(err).To(HaveOccurred())
	var stop odherrors.StopError
	g.Expect(errors.As(err, &stop)).To(BeTrue())
	g.Expect(stop.RequeueAfter()).To(Equal(providerBindingRequeueAfter))
	g.Expect(claim.Status.Provider).To(Equal("rhai-db"))
	provisioned := conditions.FindStatusCondition(claim, ConditionProvisioned)
	g.Expect(provisioned).NotTo(BeNil())
	g.Expect(provisioned.Reason).To(Equal("ProviderBound"))
}

func TestCleanupAction_MissingRoleSkipsRoleDrop(t *testing.T) {
	g := NewWithT(t)
	claim, provider, secret := cleanupFixtures()
	pg := &cleanupPG{databases: map[string]bool{"analytics": true}}

	err := newCleanupController(pg).cleanupAction(
		context.Background(),
		claimTestRequest(t, fakeCleanupClient(t, provider, secret, claim), claim),
	)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.droppedRoles).To(BeEmpty())
}

func TestCleanupAction_MissingDatabaseStillDropsRole(t *testing.T) {
	g := NewWithT(t)
	claim, provider, secret := cleanupFixtures()
	role := dbcontroller.RoleNameFor(claim.Namespace, claim.Name)
	pg := &cleanupPG{roles: map[string]bool{role: true}}

	err := newCleanupController(pg).cleanupAction(
		context.Background(),
		claimTestRequest(t, fakeCleanupClient(t, provider, secret, claim), claim),
	)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.droppedRoles).To(ContainElement(role))
}

func TestCleanupAction_DeletesCredentialsSecret(t *testing.T) {
	g := NewWithT(t)
	claim, provider, adminSecret := cleanupFixtures()
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbcontroller.SecretNameForSchemaClaim(claim),
			Namespace: claim.Namespace,
		},
	}
	cli := fakeCleanupClient(t, provider, adminSecret, claim, credSecret)

	err := newCleanupController(&cleanupPG{databases: map[string]bool{"analytics": true}}).cleanupAction(
		context.Background(),
		claimTestRequest(t, cli, claim),
	)
	g.Expect(err).NotTo(HaveOccurred())

	got := &corev1.Secret{}
	err = cli.Get(context.Background(), client.ObjectKeyFromObject(credSecret), got)
	g.Expect(err).To(HaveOccurred())
	g.Expect(client.IgnoreNotFound(err)).To(Succeed())
}

func TestCleanupAction_MissingDatabaseStillCompletes(t *testing.T) {
	g := NewWithT(t)
	claim, provider, adminSecret := cleanupFixtures()
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbcontroller.SecretNameForSchemaClaim(claim),
			Namespace: claim.Namespace,
		},
	}
	role := dbcontroller.RoleNameFor(claim.Namespace, claim.Name)
	pg := &cleanupPG{roles: map[string]bool{role: true}}
	cli := fakeCleanupClient(t, provider, adminSecret, claim, credSecret)

	err := newCleanupController(pg).cleanupAction(
		context.Background(),
		claimTestRequest(t, cli, claim),
	)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.droppedRoles).To(ContainElement(role))

	got := &corev1.Secret{}
	err = cli.Get(context.Background(), client.ObjectKeyFromObject(credSecret), got)
	g.Expect(client.IgnoreNotFound(err)).To(Succeed())
}

func newCleanupController(pg *cleanupPG) *Controller {
	return &Controller{Options: Options{
		cfg: &moduleconfig.Config{OperatorNamespace: "odh-db-operator-system"},
		PostgresClientFactory: func(_ context.Context, _ postgres.Config) (postgres.Client, error) {
			return pg, nil
		},
	}}
}

func cleanupFixtures() (*infraApi.SchemaClaim, *infraApi.DatabaseProvider, *corev1.Secret) {
	provider := &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "rhai-db"},
		Spec: infraApi.DatabaseProviderSpec{
			Type:     infraApi.ProviderTypeInternal,
			Internal: &infraApi.InternalProviderSpec{Namespace: "database-system"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbcontroller.InternalAdminSecretName(provider.Name),
			Namespace: "database-system",
		},
		Data: map[string][]byte{
			postgres.SecretKeyHost:     []byte("rhai-db.database-system.svc"),
			postgres.SecretKeyPort:     []byte("5432"),
			postgres.SecretKeyUser:     []byte("postgres"),
			postgres.SecretKeyPassword: []byte("postgres"),
			postgres.SecretKeyDatabase: []byte("postgres"),
		},
	}
	claim := &infraApi.SchemaClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "analytics", Namespace: "app"},
		Spec: infraApi.SchemaClaimSpec{
			Provider:       infraApi.ProviderRef{Name: "rhai-db"},
			Schema:         "app_schema",
			Database:       "analytics",
			DeletionPolicy: infraApi.DeletionPolicyRetain,
		},
		Status: infraApi.SchemaClaimStatus{
			Provider: "rhai-db",
			Schema:   "app_schema",
			Connection: infraApi.SchemaConnectionStatus{
				Database: "analytics",
			},
		},
	}
	return claim, provider, secret
}

func fakeCleanupClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(claimTestScheme(t)).WithObjects(objs...).Build()
}

// cleanupPG is a minimal postgres.Client fake for cleanupAction paths.
type cleanupPG struct {
	databases    map[string]bool
	roles        map[string]bool
	droppedRoles []string
}

func (t *cleanupPG) Config() postgres.Config { return postgres.Config{} }
func (t *cleanupPG) Close()                  {}
func (t *cleanupPG) Ping(context.Context) error {
	return nil
}

func (t *cleanupPG) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "DROP ROLE") {
		start := strings.Index(sql, `"`)
		end := strings.LastIndex(sql, `"`)
		if start >= 0 && end > start {
			name := sql[start+1 : end]
			delete(t.roles, name)
			t.droppedRoles = append(t.droppedRoles, name)
		}
	}
	return pgconn.CommandTag{}, nil
}

func (t *cleanupPG) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query")
}

func (t *cleanupPG) QueryRow(_ context.Context, sql string, args ...any) (pgx.Row, error) {
	if t.databases == nil {
		t.databases = map[string]bool{}
	}
	if t.roles == nil {
		t.roles = map[string]bool{}
	}
	name, _ := args[0].(string)
	switch {
	case strings.Contains(sql, "pg_database"):
		return cleanupBoolRow{t.databases[name]}, nil
	case strings.Contains(sql, "pg_roles"):
		return cleanupBoolRow{t.roles[name]}, nil
	default:
		return cleanupBoolRow{false}, nil
	}
}

type cleanupBoolRow struct{ v bool }

func (r cleanupBoolRow) Scan(dest ...any) error {
	ptr, ok := dest[0].(*bool)
	if !ok {
		return fmt.Errorf("expected *bool")
	}
	*ptr = r.v
	return nil
}

func claimTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	g := NewWithT(t)
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(infraApi.AddToScheme(scheme)).To(Succeed())
	return scheme
}

func claimTestRequest(t *testing.T, cli client.Client, claim *infraApi.SchemaClaim) *odhtypes.ReconciliationRequest {
	t.Helper()
	aggregator, err := conditions.NewAggregator(
		fwapi.ConditionTypeReady,
		conditions.Dependent(fwapi.ConditionType(ConditionProvisioned), conditions.HealthyWhenTrue),
		conditions.Dependent(fwapi.ConditionType(ConditionTLSConfiguration), conditions.HealthyWhenTrue),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &odhtypes.ReconciliationRequest{
		Client:     cli,
		Conditions: conditions.NewManager(claim, aggregator),
		Instance:   claim,
	}
}
