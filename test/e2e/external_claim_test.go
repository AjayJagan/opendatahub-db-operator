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

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	infraapi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	modulemanager "github.com/opendatahub-io/opendatahub-db-operator/pkg/manager"
	"github.com/opendatahub-io/opendatahub-db-operator/test/support"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const connectionScript = `
set -eu
escape_conninfo() {
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e "s/'/\\\\'/g"
}
connection="host='$(escape_conninfo "$DB_HOST")'"
connection="$connection port='$(escape_conninfo "$DB_PORT")'"
connection="$connection user='$(escape_conninfo "$DB_USER")'"
connection="$connection password='$(escape_conninfo "$DB_PASSWORD")'"
connection="$connection dbname='$(escape_conninfo "$DB_NAME")'"
connection="$connection sslmode='$(escape_conninfo "$DB_SSLMODE")'"
`

const adminTableSetupScript = connectionScript + `
case "$DB_SSLMODE" in
    verify-full)
        connection="$connection sslrootcert='/etc/db-trust/ca.crt'"
        ;;
    disable)
        ;;
    *)
        echo "unexpected sslmode: $DB_SSLMODE" >&2
        exit 1
        ;;
esac
psql -X -qAt -v ON_ERROR_STOP=1 -v claim_role="$CLAIM_ROLE" "$connection" <<'SQL'
DROP TABLE IF EXISTS public.claim_contract_test;
CREATE TABLE public.claim_contract_test (value integer);
GRANT SELECT, INSERT ON TABLE public.claim_contract_test TO :"claim_role";
SQL
`

const consumerScript = connectionScript + `
case "$DB_SSLMODE" in
    verify-full)
        connection="$connection sslrootcert='/etc/db-trust/ca.crt'"
        expected='t|42'
        ;;
    disable)
        expected='f|42'
        ;;
    *)
        echo "unexpected sslmode: $DB_SSLMODE" >&2
        exit 1
        ;;
esac
query='INSERT INTO public.claim_contract_test VALUES (42);'
query="$query SELECT ssl, (SELECT value FROM public.claim_contract_test)"
query="$query FROM pg_stat_ssl WHERE pid = pg_backend_pid()"
result=$(psql -X -qAt -v ON_ERROR_STOP=1 "$connection" -c "$query")
test "$result" = "$expected"
`

func TestExternalProviderClaimLifecycle(t *testing.T) {
	adminNamespace := os.Getenv("ODH_E2E_EXTERNAL_ADMIN_SECRET_NAMESPACE")
	adminName := os.Getenv("ODH_E2E_EXTERNAL_ADMIN_SECRET_NAME")
	if adminNamespace == "" && adminName == "" {
		t.Skip("set ODH_E2E_EXTERNAL_ADMIN_SECRET_NAMESPACE and ODH_E2E_EXTERNAL_ADMIN_SECRET_NAME")
	}
	if adminNamespace == "" || adminName == "" {
		t.Fatal("both external admin Secret namespace and name are required")
	}

	g := NewWithT(t)
	restCfg, err := ctrl.GetConfig()
	g.Expect(err).NotTo(HaveOccurred())
	scheme, err := modulemanager.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())
	cli, err := client.New(restCfg, client.Options{Scheme: scheme})
	g.Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)

	adminSecret := &corev1.Secret{}
	g.Expect(cli.Get(ctx, client.ObjectKey{Namespace: adminNamespace, Name: adminName}, adminSecret)).To(Succeed())
	for _, key := range []string{"host", "port", "user", "password", "dbname", "ca.crt"} {
		g.Expect(adminSecret.Data[key]).NotTo(BeEmpty(), "external admin Secret needs %q", key)
	}
	g.Expect(string(adminSecret.Data["sslmode"])).To(Equal("verify-full"),
		"the administrator-supplied target must support hostname-verified TLS")

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "odh-db-contract-"}}
	g.Expect(cli.Create(ctx, namespace)).To(Succeed())
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		if err := support.DeleteNamespace(cleanupCtx, cli, namespace.Name, namespace.UID); err != nil {
			t.Errorf("cleaning consumer namespace: %v", err)
		}
	})

	provider := &infraapi.DatabaseProvider{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "odh-db-e2e-external-"},
		Spec: infraapi.DatabaseProviderSpec{
			Type: infraapi.ProviderTypeExternal,
			External: &infraapi.ExternalProviderSpec{
				ConnectionSecretRef: corev1.SecretReference{Namespace: adminNamespace, Name: adminName},
			},
		},
	}
	g.Expect(cli.Create(ctx, provider)).To(Succeed())
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		err := cli.Delete(cleanupCtx, provider, client.Preconditions{UID: &provider.UID})
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			t.Errorf("cleaning external provider: %v", err)
		}
	})

	g.Eventually(func(g Gomega) {
		current := &infraapi.DatabaseProvider{}
		g.Expect(cli.Get(ctx, client.ObjectKeyFromObject(provider), current)).To(Succeed())
		condition := conditions.FindStatusCondition(current, "Reachable")
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue), "%s", condition.Message)
		g.Expect(current.Status.TLS).NotTo(BeNil())
		g.Expect(current.Status.TLS.Ready).To(BeTrue())
	}).WithTimeout(2*time.Minute).Should(Succeed(), "external provider did not become reachable")

	claim := &infraapi.DatabaseClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "connection-contract", Namespace: namespace.Name},
		Spec: infraapi.DatabaseClaimSpec{
			Provider: infraapi.ProviderRef{Name: provider.Name},
			Access:   infraapi.AccessModeReadWrite,
		},
	}
	g.Expect(cli.Create(ctx, claim)).To(Succeed())
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		err := cli.Delete(cleanupCtx, claim, client.Preconditions{UID: &claim.UID})
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			t.Errorf("cleaning database claim: %v", err)
		}
	})

	g.Eventually(func(g Gomega) {
		current := &infraapi.DatabaseClaim{}
		g.Expect(cli.Get(ctx, client.ObjectKeyFromObject(claim), current)).To(Succeed())
		condition := conditions.FindStatusCondition(current, "Provisioned")
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue), "%s", condition.Message)
		g.Expect(current.Status.Connection.SecretRef.Name).To(Equal(claim.Name))
	}).WithTimeout(2*time.Minute).Should(Succeed(), "database claim did not provision")

	credentials := &corev1.Secret{}
	g.Eventually(func(g Gomega) {
		g.Expect(cli.Get(ctx, client.ObjectKey{Namespace: namespace.Name, Name: claim.Name}, credentials)).To(Succeed())
	}).WithTimeout(time.Minute).Should(Succeed())
	for _, key := range []string{"host", "port", "user", "password", "dbname", "ca.crt"} {
		g.Expect(credentials.Data[key]).NotTo(BeEmpty(), "claim Secret needs %q", key)
	}
	g.Expect(string(credentials.Data["sslmode"])).To(Equal("verify-full"))
	g.Expect(credentials.Annotations["opendatahub.io/connection-type-protocol"]).To(Equal("postgres"))

	image := envOrDefault("ODH_E2E_CONSUMER_IMAGE", "postgres:16")
	createAdminContractTable(t, ctx, cli, restCfg, adminNamespace, adminName,
		string(credentials.Data["dbname"]), string(credentials.Data["user"]),
		string(adminSecret.Data["sslmode"]), image, true)
	runConsumer(t, ctx, cli, restCfg, namespace.Name, claim.Name, image, true)

	g.Expect(cli.Delete(ctx, claim, client.Preconditions{UID: &claim.UID})).To(Succeed())
	g.Eventually(func(g Gomega) {
		current := &infraapi.DatabaseClaim{}
		err := cli.Get(ctx, client.ObjectKeyFromObject(claim), current)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "claim still exists: %v", err)
	}).WithTimeout(2 * time.Minute).Should(Succeed())
}

func runConsumer(
	t *testing.T,
	ctx context.Context,
	cli client.Client,
	restCfg *rest.Config,
	namespace, secretName, image string,
	withCA bool,
) {
	t.Helper()
	runPod(t, ctx, cli, restCfg, consumerPod(namespace, secretName, image, withCA))
}

func createAdminContractTable(
	t *testing.T,
	ctx context.Context,
	cli client.Client,
	restCfg *rest.Config,
	namespace, secretName, databaseName, claimRole, adminSSLMode, image string,
	withCA bool,
) {
	t.Helper()
	pod := consumerPod(namespace, secretName, image, withCA)
	pod.Name = ""
	pod.GenerateName = "claim-contract-table-"
	pod.Spec.Containers[0].Command = []string{"/bin/sh", "-ec", adminTableSetupScript}
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
		Name: "CLAIM_ROLE", Value: claimRole,
	})
	for i := range pod.Spec.Containers[0].Env {
		switch pod.Spec.Containers[0].Env[i].Name {
		case "DB_NAME":
			pod.Spec.Containers[0].Env[i].Value = databaseName
			pod.Spec.Containers[0].Env[i].ValueFrom = nil
		case "DB_SSLMODE":
			pod.Spec.Containers[0].Env[i].Value = adminSSLMode
			pod.Spec.Containers[0].Env[i].ValueFrom = nil
		}
	}
	runPod(t, ctx, cli, restCfg, pod)
}

func runPod(
	t *testing.T,
	ctx context.Context,
	cli client.Client,
	restCfg *rest.Config,
	pod *corev1.Pod,
) {
	t.Helper()
	g := NewWithT(t)
	g.Expect(cli.Create(ctx, pod)).To(Succeed())
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := cli.Delete(cleanupCtx, pod); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleaning pod %s/%s: %v", pod.Namespace, pod.Name, err)
		}
	})

	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		current := &corev1.Pod{}
		if err := cli.Get(ctx, client.ObjectKeyFromObject(pod), current); err != nil {
			return false, err
		}
		switch current.Status.Phase {
		case corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("pod failed: %v", current.Status.ContainerStatuses)
		default:
			return false, nil
		}
	})
	if err != nil {
		logCtx, logDone := context.WithTimeout(context.Background(), 15*time.Second)
		defer logDone()
		logs, logErr := consumerLogs(logCtx, restCfg, pod.Namespace, pod.Name)
		if logErr != nil {
			t.Fatalf("waiting for pod %s/%s: %v; reading logs: %v", pod.Namespace, pod.Name, err, logErr)
		}
		t.Fatalf("waiting for pod %s/%s: %v; logs: %s", pod.Namespace, pod.Name, err, logs)
	}
}

func consumerPod(namespace, secretName, image string, withCA bool) *corev1.Pod {
	secretField := func(envName, key string) corev1.EnvVar {
		return corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: key,
			}},
		}
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "connection-consumer", Namespace: namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "consumer",
				Image:   image,
				Command: []string{"/bin/sh", "-ec", consumerScript},
				Env: []corev1.EnvVar{
					secretField("DB_HOST", "host"),
					secretField("DB_PORT", "port"),
					secretField("DB_USER", "user"),
					secretField("DB_PASSWORD", "password"),
					secretField("DB_NAME", "dbname"),
					secretField("DB_SSLMODE", "sslmode"),
				},
			}},
		},
	}
	if withCA {
		pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
			Name: "trust", MountPath: "/etc/db-trust", ReadOnly: true,
		}}
		pod.Spec.Volumes = []corev1.Volume{{Name: "trust", VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
				Items:      []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
			},
		}}}
	}
	return pod
}

func consumerLogs(ctx context.Context, cfg *rest.Config, namespace, name string) ([]byte, error) {
	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return cli.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(ctx)
}
