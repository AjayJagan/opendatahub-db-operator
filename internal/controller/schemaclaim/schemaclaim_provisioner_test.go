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

package schemaclaim_test

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
	"github.com/opendatahub-io/opendatahub-db-operator/internal/controller/schemaclaim"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/controller/claimerrors"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/postgres"
)

func TestEnsure_CreatesSchemaRoleAndSecret(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim()
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}

	secret, err := ensure(t, claim, internalProvider(), pg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.createdSchemas).To(ContainElement("myschema"))
	g.Expect(claim.Status.SchemaCreated).To(BeTrue())
	g.Expect(pg.createdRoles).NotTo(BeEmpty())
	g.Expect(pg.createdDatabases).To(BeEmpty())
	g.Expect(string(secret.Data[postgres.SecretKeySchema])).To(Equal("myschema"))
	g.Expect(string(secret.Data[postgres.SecretKeyDatabase])).To(Equal("analytics"))
	g.Expect(secret.Annotations[postgres.AnnotationConnectionTypeProtocol]).
		To(Equal(postgres.ConnectionTypeProtocolPostgres))
	g.Expect(string(secret.Data[postgres.SecretKeySSLMode])).To(Equal(postgres.SSLModeDisable))
}

func TestEnsure_RejectsPreexistingSchemaWithoutOwnership(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim()
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
		schemas:   map[string]bool{"myschema": true},
	}

	_, err := ensure(t, claim, internalProvider(), pg)
	g.Expect(err).To(MatchError(claimerrors.SchemaNotOwned{Schema: "myschema"}))
	g.Expect(pg.createdSchemas).To(BeEmpty())
	g.Expect(pg.createdRoles).To(BeEmpty())
	g.Expect(claim.Status.SchemaCreated).To(BeFalse())
}

func TestEnsure_AllowsPreexistingSchemaWhenOwned(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim()
	claim.Status.SchemaCreated = true
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
		schemas:   map[string]bool{"myschema": true},
	}

	secret, err := ensure(t, claim, internalProvider(), pg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.createdSchemas).To(BeEmpty())
	g.Expect(claim.Status.SchemaCreated).To(BeTrue())
	g.Expect(string(secret.Data[postgres.SecretKeySchema])).To(Equal("myschema"))
}

func TestEnsure_RejectsReservedSchema(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim()
	claim.Spec.Schema = "public"
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}

	_, err := ensure(t, claim, internalProvider(), pg)
	g.Expect(err).To(MatchError(claimerrors.SchemaReserved{Schema: "public"}))
	g.Expect(pg.createdSchemas).To(BeEmpty())
	g.Expect(pg.createdRoles).To(BeEmpty())
}

func TestEnsure_MissingDatabaseIsNotCreated(t *testing.T) {
	g := NewWithT(t)
	pg := &trackingClient{cfg: providerConfig()}

	_, err := ensure(t, sampleClaim(), internalProvider(), pg)
	g.Expect(err).To(MatchError(claimerrors.DatabaseNotFound{Database: "analytics"}))
	g.Expect(pg.createdDatabases).To(BeEmpty())
	g.Expect(pg.createdSchemas).To(BeEmpty())
}

func TestEnsure_DeniesSchemaCreateOnExternalWithoutCapability(t *testing.T) {
	g := NewWithT(t)
	provider := &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "external"},
		Spec: infraApi.DatabaseProviderSpec{
			Type:     infraApi.ProviderTypeExternal,
			External: &infraApi.ExternalProviderSpec{},
		},
	}
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}

	_, err := ensure(t, sampleClaim(), provider, pg)
	g.Expect(err).To(MatchError(claimerrors.SchemaCreateNotAllowed{Schema: "myschema"}))
	g.Expect(pg.createdSchemas).To(BeEmpty())
}

func TestEnsure_CreatesSchemaOnExternalWhenAllowed(t *testing.T) {
	g := NewWithT(t)
	provider := &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "external"},
		Spec: infraApi.DatabaseProviderSpec{
			Type: infraApi.ProviderTypeExternal,
			External: &infraApi.ExternalProviderSpec{
				Capabilities: []infraApi.ExternalCapability{
					infraApi.ExternalCapabilityCreateSchema,
				},
			},
		},
	}
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}

	claim := sampleClaim()
	secret, err := ensure(t, claim, provider, pg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.createdSchemas).To(ContainElement("myschema"))
	g.Expect(claim.Status.SchemaCreated).To(BeTrue())
	g.Expect(string(secret.Data[postgres.SecretKeySchema])).To(Equal("myschema"))
}

func TestEnsure_ReadOnlyGrantsConnectWithoutDatabaseCreate(t *testing.T) {
	g := NewWithT(t)
	claim := sampleClaim()
	claim.Spec.Access = infraApi.AccessModeReadOnly
	pg := &trackingClient{
		cfg:       providerConfig(),
		databases: map[string]bool{"analytics": true},
	}

	_, err := ensure(t, claim, internalProvider(), pg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pg.grantedConnect).To(Equal(1))
	g.Expect(pg.grantedDBCreate).To(Equal(0))
}

func ensure(
	t *testing.T,
	claim *infraApi.SchemaClaim,
	provider *infraApi.DatabaseProvider,
	pg *trackingClient,
) (*corev1.Secret, error) {
	t.Helper()
	return schemaclaim.SchemaProvisioner{
		Client:         fakeClient(t),
		Claim:          claim,
		Provider:       provider,
		Postgres:       pg,
		ProviderConfig: providerConfig(),
		ClientFactory: func(_ context.Context, cfg postgres.Config) (postgres.Client, error) {
			return &trackingClient{
				cfg:       cfg,
				databases: pg.databases,
				schemas:   pg.schemas,
				roles:     pg.roles,
				parent:    pg,
			}, nil
		},
	}.Ensure(context.Background())
}

func sampleClaim() *infraApi.SchemaClaim {
	return &infraApi.SchemaClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-a", Namespace: "app-ns"},
		Spec: infraApi.SchemaClaimSpec{
			Provider: infraApi.ProviderRef{Name: "rhai-db"},
			Schema:   "myschema",
			Database: "analytics",
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
	schemas          map[string]bool
	roles            map[string]bool
	createdDatabases []string
	createdSchemas   []string
	createdRoles     []string
	grantedConnect   int
	grantedDBCreate  int
	parent           *trackingClient
}

func (t *trackingClient) root() *trackingClient {
	if t.parent != nil {
		return t.parent
	}
	return t
}

func (t *trackingClient) Config() postgres.Config { return t.cfg }
func (t *trackingClient) Close()                  {}
func (t *trackingClient) Ping(context.Context) error {
	return nil
}

func (t *trackingClient) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	root := t.root()
	if root.databases == nil {
		root.databases = map[string]bool{}
	}
	if root.schemas == nil {
		root.schemas = map[string]bool{}
	}
	if root.roles == nil {
		root.roles = map[string]bool{}
	}
	switch {
	case strings.Contains(sql, "CREATE SCHEMA"):
		name := firstQuotedIdent(sql)
		root.schemas[name] = true
		root.createdSchemas = append(root.createdSchemas, name)
	case strings.Contains(sql, "CREATE ROLE"):
		name := firstQuotedIdent(sql)
		root.roles[name] = true
		root.createdRoles = append(root.createdRoles, name)
	case strings.Contains(sql, "GRANT CONNECT ON DATABASE"):
		root.grantedConnect++
	case strings.Contains(sql, "GRANT CREATE ON DATABASE"):
		root.grantedDBCreate++
	}
	return pgconn.CommandTag{}, nil
}

func (t *trackingClient) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query")
}

func (t *trackingClient) QueryRow(_ context.Context, sql string, args ...any) (pgx.Row, error) {
	root := t.root()
	if root.databases == nil {
		root.databases = map[string]bool{}
	}
	if root.schemas == nil {
		root.schemas = map[string]bool{}
	}
	if root.roles == nil {
		root.roles = map[string]bool{}
	}
	switch {
	case strings.Contains(sql, "pg_database"):
		name, _ := args[0].(string)
		return boolRow{root.databases[name]}, nil
	case strings.Contains(sql, "pg_namespace"):
		name, _ := args[0].(string)
		return boolRow{root.schemas[name]}, nil
	case strings.Contains(sql, "pg_roles"):
		name, _ := args[0].(string)
		return boolRow{root.roles[name]}, nil
	case strings.Contains(sql, "HAS_DATABASE_PRIVILEGE"), strings.Contains(sql, "HAS_SCHEMA_PRIVILEGE"):
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
