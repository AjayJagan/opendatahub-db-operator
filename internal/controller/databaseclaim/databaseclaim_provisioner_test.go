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

package databaseclaim_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	"github.com/opendatahub-io/opendatahub-db-operator/internal/controller/databaseclaim"
	dbcontroller "github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/controller/claimerrors"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/postgres"
)

func TestEnsure_CreatesNamedDatabaseWhenAllowed(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("analytics")
	provider := internalProvider()
	pg := &trackingClient{cfg: providerConfig()}
	cli := fakeClient(t)

	secret, err := databaseclaim.DatabaseProvisioner{
		Client:         cli,
		Claim:          claim,
		Provider:       provider,
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(secret).NotTo(BeNil())
	g.Expect(pg.createdDatabases).To(ContainElement("analytics"))
	g.Expect(pg.createdRoles).NotTo(BeEmpty())
	g.Expect(string(secret.Data[postgres.SecretKeyDatabase])).To(Equal("analytics"))
	g.Expect(secret.Annotations[postgres.AnnotationConnectionTypeProtocol]).
		To(Equal(postgres.ConnectionTypeProtocolPostgres))
	g.Expect(string(secret.Data[postgres.SecretKeySSLMode])).To(Equal(postgres.SSLModeDisable))
	g.Expect(string(secret.Data[postgres.SecretKeyPassword])).To(MatchRegexp(`^[A-Za-z0-9]+$`))
}

func TestEnsure_DeniesCreateOnExternalWithoutCapability(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("analytics")
	provider := &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "external"},
		Spec: infraApi.DatabaseProviderSpec{
			Type:     infraApi.ProviderTypeExternal,
			External: &infraApi.ExternalProviderSpec{},
		},
	}
	pg := &trackingClient{cfg: providerConfig()}

	_, err := databaseclaim.DatabaseProvisioner{
		Client:         fakeClient(t),
		Claim:          claim,
		Provider:       provider,
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).To(MatchError(claimerrors.DatabaseCreateNotAllowed{Database: "analytics"}))
	g.Expect(pg.createdDatabases).To(BeEmpty())
}

func TestEnsure_DefaultDatabaseMissingIsNotCreated(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("")
	pg := &trackingClient{cfg: providerConfig()}

	_, err := databaseclaim.DatabaseProvisioner{
		Client:         fakeClient(t),
		Claim:          claim,
		Provider:       internalProvider(),
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).To(MatchError(claimerrors.DatabaseNotFound{Database: "postgres"}))
	g.Expect(pg.createdDatabases).To(BeEmpty())
}

func TestEnsure_DistinctClaimsGetDistinctRoles(t *testing.T) {
	g := NewWithT(t)
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}
	cli := fakeClient(t)

	a := sampleClaim("analytics")
	a.Name = "my-db"
	b := sampleClaim("analytics")
	b.Name = "my.db"

	secretA, err := databaseclaim.DatabaseProvisioner{
		Client: cli, Claim: a, Provider: internalProvider(), Postgres: pg, ProviderConfig: providerConfig(),
	}.Ensure(context.Background())
	g.Expect(err).NotTo(HaveOccurred())

	secretB, err := databaseclaim.DatabaseProvisioner{
		Client: cli, Claim: b, Provider: internalProvider(), Postgres: pg, ProviderConfig: providerConfig(),
	}.Ensure(context.Background())
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(string(secretA.Data[postgres.SecretKeyUser])).
		NotTo(Equal(string(secretB.Data[postgres.SecretKeyUser])))
	g.Expect(pg.createdRoles).To(HaveLen(2))
}

func TestEnsure_RotatesPasswordWhenSecretMissingButRoleExists(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("analytics")
	role := dbcontroller.RoleNameFor(claim.Namespace, claim.Name)
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
		roles:     map[string]bool{role: true},
	}

	secret, err := databaseclaim.DatabaseProvisioner{
		Client:         fakeClient(t),
		Claim:          claim,
		Provider:       internalProvider(),
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.createdRoles).To(BeEmpty())
	g.Expect(pg.passwordSets).To(Equal(1))
	g.Expect(string(secret.Data[postgres.SecretKeyPassword])).To(MatchRegexp(`^[A-Za-z0-9]+$`))
	g.Expect(string(secret.Data[postgres.SecretKeyUser])).To(Equal(role))
}

func TestEnsure_RotatesPasswordWhenSecretPasswordEmpty(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("analytics")
	role := dbcontroller.RoleNameFor(claim.Namespace, claim.Name)
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
		roles:     map[string]bool{role: true},
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: claim.Name, Namespace: claim.Namespace},
		Data: map[string][]byte{
			postgres.SecretKeyHost:     []byte("stale.example"),
			postgres.SecretKeyPassword: []byte(""),
		},
	}

	secret, err := databaseclaim.DatabaseProvisioner{
		Client:         fakeClient(t, existing),
		Claim:          claim,
		Provider:       internalProvider(),
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.createdRoles).To(BeEmpty())
	g.Expect(pg.passwordSets).To(Equal(1))
	g.Expect(string(secret.Data[postgres.SecretKeyPassword])).To(MatchRegexp(`^[A-Za-z0-9]+$`))
	g.Expect(string(secret.Data[postgres.SecretKeyHost])).To(Equal(providerConfig().Host))
}

func TestEnsure_CreatesNamedDatabaseOnExternalWhenAllowed(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("analytics")
	provider := &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "external"},
		Spec: infraApi.DatabaseProviderSpec{
			Type: infraApi.ProviderTypeExternal,
			External: &infraApi.ExternalProviderSpec{
				Capabilities: []infraApi.ExternalCapability{
					infraApi.ExternalCapabilityCreateDatabase,
				},
			},
		},
	}
	pg := &trackingClient{cfg: providerConfig()}

	secret, err := databaseclaim.DatabaseProvisioner{
		Client:         fakeClient(t),
		Claim:          claim,
		Provider:       provider,
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.createdDatabases).To(ContainElement("analytics"))
	g.Expect(claim.Status.DatabaseCreated).To(BeTrue())
	g.Expect(secret).NotTo(BeNil())
	g.Expect(string(secret.Data[postgres.SecretKeyDatabase])).To(Equal("analytics"))
}

func TestEnsure_DoesNotMarkDatabaseCreatedWhenPreexisting(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("analytics")
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}

	_, err := databaseclaim.DatabaseProvisioner{
		Client:         fakeClient(t),
		Claim:          claim,
		Provider:       internalProvider(),
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.createdDatabases).To(BeEmpty())
	g.Expect(claim.Status.DatabaseCreated).To(BeFalse())
}

func TestEnsure_ReadOnlyGrantsConnectWithoutCreate(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim("analytics")
	claim.Spec.Access = infraApi.AccessModeReadOnly
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}

	_, err := databaseclaim.DatabaseProvisioner{
		Client:         fakeClient(t),
		Claim:          claim,
		Provider:       internalProvider(),
		Postgres:       pg,
		ProviderConfig: providerConfig(),
	}.Ensure(context.Background())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.grantedConnect).To(Equal(1))
	g.Expect(pg.grantedCreate).To(Equal(0))
}

func sampleClaim(database string) *infraApi.DatabaseClaim {
	return &infraApi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-a", Namespace: "app-ns"},
		Spec: infraApi.DatabaseClaimSpec{
			Provider: infraApi.ProviderRef{Name: "rhai-db"},
			Database: database,
			Access:   infraApi.AccessModeReadWrite,
		},
	}
}

func internalProvider() *infraApi.DatabaseProvider {
	return &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "rhai-db"},
		Spec:       infraApi.DatabaseProviderSpec{Type: infraApi.ProviderTypeInternal},
	}
}

func providerConfig() postgres.Config {
	return postgres.Config{
		Host:   "rhai-db.database-system.svc",
		Port:   5432,
		User:   "postgres",
		DBName: "postgres",
	}
}

func fakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	g := NewWithT(t)
	g.Expect(infraApi.AddToScheme(scheme)).To(Succeed())
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

type trackingClient struct {
	cfg              postgres.Config
	databases        map[string]bool
	roles            map[string]bool
	createdDatabases []string
	createdRoles     []string
	passwordSets     int
	grantedConnect   int
	grantedCreate    int
}

func (t *trackingClient) Config() postgres.Config { return t.cfg }
func (t *trackingClient) Close()                  {}
func (t *trackingClient) Ping(context.Context) error {
	return nil
}

func (t *trackingClient) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if t.databases == nil {
		t.databases = map[string]bool{}
	}
	if t.roles == nil {
		t.roles = map[string]bool{}
	}
	switch {
	case strings.Contains(sql, "CREATE DATABASE"):
		name := lastQuotedIdent(sql)
		t.databases[name] = true
		t.createdDatabases = append(t.createdDatabases, name)
	case strings.Contains(sql, "CREATE ROLE"):
		name := firstQuotedIdent(sql)
		t.roles[name] = true
		t.createdRoles = append(t.createdRoles, name)
	case strings.Contains(sql, "ALTER ROLE") && strings.Contains(sql, "PASSWORD"):
		t.passwordSets++
	case strings.Contains(sql, "GRANT CONNECT ON DATABASE"):
		t.grantedConnect++
	case strings.Contains(sql, "GRANT CREATE ON DATABASE"):
		t.grantedCreate++
	}
	return pgconn.CommandTag{}, nil
}

func (t *trackingClient) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query")
}

func (t *trackingClient) QueryRow(_ context.Context, sql string, args ...any) (pgx.Row, error) {
	if t.databases == nil {
		t.databases = map[string]bool{}
	}
	if t.roles == nil {
		t.roles = map[string]bool{}
	}
	switch {
	case strings.Contains(sql, "pg_database"):
		name, _ := args[0].(string)
		return boolRow{t.databases[name]}, nil
	case strings.Contains(sql, "pg_roles"):
		name, _ := args[0].(string)
		return boolRow{t.roles[name]}, nil
	case strings.Contains(sql, "HAS_DATABASE_PRIVILEGE"):
		return boolRow{false}, nil
	default:
		return boolRow{false}, nil
	}
}

type boolRow struct{ v bool }

func (r boolRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("expected 1 dest")
	}
	ptr, ok := dest[0].(*bool)
	if !ok {
		return fmt.Errorf("expected *bool")
	}
	*ptr = r.v
	return nil
}

func lastQuotedIdent(sql string) string {
	start := -1
	end := -1
	for i, r := range sql {
		if r == '"' {
			if start < 0 {
				start = i + 1
			} else {
				end = i
			}
		}
	}
	if start >= 0 && end > start {
		return sql[start:end]
	}
	return ""
}

func firstQuotedIdent(sql string) string {
	start := -1
	for i, r := range sql {
		if r == '"' {
			if start < 0 {
				start = i + 1
			} else {
				return sql[start:i]
			}
		}
	}
	return ""
}
