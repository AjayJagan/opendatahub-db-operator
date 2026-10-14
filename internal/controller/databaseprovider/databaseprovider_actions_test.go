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

package databaseprovider

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/gomega"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	odhtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/postgres"
	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileExternalActionReportsUnavailableSecret(t *testing.T) {
	g := NewWithT(t)
	provider := externalProvider()
	provider.Status.ServerVersion = "16.4"
	cli := testClient(t)
	rr := testRequest(t, cli, provider)

	err := (&Controller{}).reconcileExternalAction(context.Background(), rr)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(provider.Status.ServerVersion).To(BeEmpty())
	reachable := conditions.FindStatusCondition(provider, ConditionReachable)
	g.Expect(reachable).NotTo(BeNil())
	g.Expect(reachable.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(reachable.Reason).To(Equal("ConnectionSecretUnavailable"))
	g.Expect(reachable.Message).To(ContainSubstring("external-admin"))
	g.Expect(reachable.Message).To(ContainSubstring("Verify"))
	ready := conditions.FindStatusCondition(provider, string(fwapi.ConditionTypeReady))
	g.Expect(ready).NotTo(BeNil())
	g.Expect(ready.Status).NotTo(Equal(metav1.ConditionTrue))
}

func TestReconcileExternalActionReportsInvalidSecretWithoutReturningError(t *testing.T) {
	g := NewWithT(t)
	provider := externalProvider()
	provider.Status.ServerVersion = "16.4"
	secret := externalAdminSecret()
	secret.Data[postgres.SecretKeyPort] = []byte("not-a-port")
	cli := testClient(t, secret)
	rr := testRequest(t, cli, provider)

	g.Expect((&Controller{}).reconcileExternalAction(context.Background(), rr)).To(Succeed())
	g.Expect(provider.Status.ServerVersion).To(BeEmpty())
	reachable := conditions.FindStatusCondition(provider, ConditionReachable)
	g.Expect(reachable).NotTo(BeNil())
	g.Expect(reachable.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(reachable.Reason).To(Equal("ConnectionSecretInvalid"))
	g.Expect(reachable.Message).To(ContainSubstring("Correct the admin Secret"))
	g.Expect(reachable.Message).NotTo(ContainSubstring("secret"))
	tlsCondition := conditions.FindStatusCondition(provider, ConditionTLSConfiguration)
	g.Expect(tlsCondition).NotTo(BeNil())
	g.Expect(tlsCondition.Status).To(Equal(metav1.ConditionUnknown))
	ready := conditions.FindStatusCondition(provider, string(fwapi.ConditionTypeReady))
	g.Expect(ready).NotTo(BeNil())
	g.Expect(ready.Status).NotTo(Equal(metav1.ConditionTrue))
}

func TestReconcileExternalActionFollowsChangedTLSProfileWithoutCertManager(t *testing.T) {
	g := NewWithT(t)
	cli := testClient(t,
		externalAdminSecret(),
		&configv1.APIServer{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			Spec: configv1.APIServerSpec{TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			}},
		},
	)
	var minimumVersions []uint16
	var cipherSuites [][]uint16
	factory := func(_ context.Context, cfg postgres.Config) (postgres.Client, error) {
		tlsConfig := &tls.Config{}
		cfg.TLSConfigMutator(tlsConfig)
		minimumVersions = append(minimumVersions, tlsConfig.MinVersion)
		cipherSuites = append(cipherSuites, append([]uint16(nil), tlsConfig.CipherSuites...))
		return &fakePostgresClient{version: 160004}, nil
	}
	m := &Controller{Options: Options{PostgresClientFactory: factory}}
	provider := externalProvider()

	profiles := []*configv1.TLSSecurityProfile{
		{Type: configv1.TLSProfileIntermediateType},
		{
			Type: configv1.TLSProfileCustomType,
			Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
			}},
		},
	}
	for index, profile := range profiles {
		if index > 0 {
			apiServer := &configv1.APIServer{}
			g.Expect(cli.Get(context.Background(), client.ObjectKey{Name: "cluster"}, apiServer)).To(Succeed())
			apiServer.Spec.TLSSecurityProfile = profile
			g.Expect(cli.Update(context.Background(), apiServer)).To(Succeed())
		}
		rr := testRequest(t, cli, provider)
		g.Expect(m.reconcileExternalAction(context.Background(), rr)).To(Succeed())
	}

	g.Expect(minimumVersions).To(Equal([]uint16{tls.VersionTLS12, tls.VersionTLS12}))
	g.Expect(cipherSuites[0]).NotTo(Equal(cipherSuites[1]))
	g.Expect(cipherSuites[1]).To(Equal([]uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}))
	g.Expect(provider.Status.ServerVersion).To(Equal("16.4"))
	g.Expect(provider.Status.Connection).To(Equal(infraApi.ProviderConnectionStatus{
		Host:     "postgres.example.test",
		Port:     5432,
		Database: "postgres",
	}))
	g.Expect(provider.Status.TLS).To(Equal(&infraApi.ProviderTLSStatus{Enabled: true, Ready: true}))
	reachable := conditions.FindStatusCondition(provider, ConditionReachable)
	g.Expect(reachable.Status).To(Equal(metav1.ConditionTrue))
	version := conditions.FindStatusCondition(provider, ConditionServerVersion)
	g.Expect(version.Status).To(Equal(metav1.ConditionTrue))
	tlsCondition := conditions.FindStatusCondition(provider, ConditionTLSConfiguration)
	g.Expect(tlsCondition.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(tlsCondition.Reason).To(Equal(reasonTLSConfigured))
}

func TestReconcileExternalActionReportsUnsupportedTLSProfileCiphers(t *testing.T) {
	g := NewWithT(t)
	cli := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(
			externalAdminSecret(),
			&configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.APIServerSpec{TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type: configv1.TLSProfileCustomType,
					Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256", "UNKNOWN-CIPHER-SENTINEL"},
					}},
				}},
			},
		).
		Build()
	m := &Controller{Options: Options{PostgresClientFactory: func(_ context.Context, _ postgres.Config) (postgres.Client, error) {
		return &fakePostgresClient{version: 160004}, nil
	}}}
	provider := externalProvider()
	rr := testRequest(t, cli, provider)

	g.Expect(m.reconcileExternalAction(context.Background(), rr)).To(Succeed())
	tlsCondition := conditions.FindStatusCondition(provider, ConditionTLSConfiguration)
	g.Expect(tlsCondition).NotTo(BeNil())
	g.Expect(tlsCondition.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(tlsCondition.Message).To(ContainSubstring("UNKNOWN-CIPHER-SENTINEL"))
}

func TestReconcileExternalActionReportsUnreachableWithoutReturningError(t *testing.T) {
	g := NewWithT(t)
	provider := externalProvider()
	provider.Status.ServerVersion = "15.8"
	cli := testClient(t, externalAdminSecret())
	clientCalls := 0
	m := &Controller{Options: Options{PostgresClientFactory: func(_ context.Context, _ postgres.Config) (postgres.Client, error) {
		clientCalls++
		return &fakePostgresClient{pingErr: errors.New("network unreachable")}, nil
	}}}
	rr := testRequest(t, cli, provider)

	g.Expect(m.reconcileExternalAction(context.Background(), rr)).To(Succeed())
	g.Expect(clientCalls).To(Equal(1))
	g.Expect(provider.Status.ServerVersion).To(BeEmpty())
	reachable := conditions.FindStatusCondition(provider, ConditionReachable)
	g.Expect(reachable.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(reachable.Message).To(ContainSubstring("network unreachable"))
	g.Expect(reachable.Message).To(ContainSubstring("Verify"))
	version := conditions.FindStatusCondition(provider, ConditionServerVersion)
	g.Expect(version.Status).To(Equal(metav1.ConditionUnknown))
	ready := conditions.FindStatusCondition(provider, string(fwapi.ConditionTypeReady))
	g.Expect(ready.Status).NotTo(Equal(metav1.ConditionTrue))
}

func TestReconcileExternalActionRedactsPasswordFromConnectionFailure(t *testing.T) {
	g := NewWithT(t)
	secret := externalAdminSecret()
	cli := testClient(t, secret)
	provider := externalProvider()
	m := &Controller{Options: Options{PostgresClientFactory: func(_ context.Context, _ postgres.Config) (postgres.Client, error) {
		return &fakePostgresClient{pingErr: errors.New("authentication failed for password secret")}, nil
	}}}
	rr := testRequest(t, cli, provider)

	g.Expect(m.reconcileExternalAction(context.Background(), rr)).To(Succeed())
	reachable := conditions.FindStatusCondition(provider, ConditionReachable)
	g.Expect(reachable).NotTo(BeNil())
	g.Expect(reachable.Message).To(ContainSubstring("[redacted]"))
	g.Expect(reachable.Message).NotTo(ContainSubstring(string(secret.Data[postgres.SecretKeyPassword])))
}

func TestReconcileExternalActionKeepsOutOfRangeServerUsable(t *testing.T) {
	g := NewWithT(t)
	secret := externalAdminSecret()
	secret.Data[postgres.SecretKeySSLMode] = []byte(postgres.SSLModeDisable)
	cli := testClient(t, secret)
	provider := externalProvider()
	m := &Controller{Options: Options{PostgresClientFactory: func(_ context.Context, _ postgres.Config) (postgres.Client, error) {
		return &fakePostgresClient{version: 180000}, nil
	}}}
	rr := testRequest(t, cli, provider)

	g.Expect(m.reconcileExternalAction(context.Background(), rr)).To(Succeed())
	g.Expect(provider.Status.ServerVersion).To(Equal("18.0"))
	reachable := conditions.FindStatusCondition(provider, ConditionReachable)
	g.Expect(reachable.Status).To(Equal(metav1.ConditionTrue))
	version := conditions.FindStatusCondition(provider, ConditionServerVersion)
	g.Expect(version.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(version.Message).To(ContainSubstring("outside the tested range"))
	ready := conditions.FindStatusCondition(provider, string(fwapi.ConditionTypeReady))
	g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
}

func externalProvider() *infraApi.DatabaseProvider {
	return &infraApi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "external"},
		Spec: infraApi.DatabaseProviderSpec{
			Type: infraApi.ProviderTypeExternal,
			External: &infraApi.ExternalProviderSpec{
				ConnectionSecretRef: corev1.SecretReference{Namespace: "database-system", Name: "external-admin"},
			},
		},
	}
}

func externalAdminSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "external-admin", Namespace: "database-system"},
		Data: map[string][]byte{
			postgres.SecretKeyHost:     []byte("postgres.example.test"),
			postgres.SecretKeyPort:     []byte("5432"),
			postgres.SecretKeyUser:     []byte("admin"),
			postgres.SecretKeyPassword: []byte("secret"),
			postgres.SecretKeyDatabase: []byte("postgres"),
			postgres.SecretKeySSLMode:  []byte(postgres.SSLModeRequire),
		},
	}
}

func testClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objects...).Build()
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		appsv1.AddToScheme,
		networkingv1.AddToScheme,
		infraApi.AddToScheme,
		configv1.AddToScheme,
		apiextensionsv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func testRequest(t *testing.T, cli client.Client, provider *infraApi.DatabaseProvider) *odhtypes.ReconciliationRequest {
	t.Helper()
	aggregator, err := conditions.NewAggregator(
		fwapi.ConditionTypeReady,
		conditions.Dependent(fwapi.ConditionType(ConditionReachable), conditions.HealthyWhenTrue),
		conditions.Dependent(fwapi.ConditionType(ConditionTLSConfiguration), conditions.HealthyWhenTrue),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &odhtypes.ReconciliationRequest{
		Client:     cli,
		Conditions: conditions.NewManager(provider, aggregator),
		Instance:   provider,
	}
}

type fakePostgresClient struct {
	version int
	pingErr error
}

func (c *fakePostgresClient) Config() postgres.Config { return postgres.Config{} }
func (c *fakePostgresClient) Close()                  {}
func (c *fakePostgresClient) Ping(context.Context) error {
	return c.pingErr
}
func (c *fakePostgresClient) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (c *fakePostgresClient) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (c *fakePostgresClient) QueryRow(context.Context, string, ...any) (pgx.Row, error) {
	return fakePostgresRow{version: c.version}, nil
}

type fakePostgresRow struct{ version int }

func (r fakePostgresRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return errors.New("unexpected scan arguments")
	}
	version, ok := dest[0].(*int)
	if !ok {
		return errors.New("unexpected scan target")
	}
	*version = r.version
	return nil
}
