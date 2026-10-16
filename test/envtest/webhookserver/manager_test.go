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

package webhookserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	modulemanager "github.com/opendatahub-io/opendatahub-db-operator/pkg/manager"
)

func TestManagerStartsWebhookServerWithoutAdmissionHandlers(t *testing.T) {
	g := NewWithT(t)
	ctrl.SetLogger(logr.Discard())

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{"../../../config/crd/bases"},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			LocalServingHost: "127.0.0.1",
			Paths:            []string{"../../../config/webhook"},
		},
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
	cfg.Controller.LeaderElection.Enabled = false
	cfg.Controller.Metrics.BindAddress = "0"
	cfg.Controller.Health.BindAddress = "0"
	cfg.Controller.Pprof.BindAddress = "0"
	cfg.Controller.Webhook.Enabled = true
	cfg.Controller.Webhook.Port = env.WebhookInstallOptions.LocalServingPort
	cfg.Controller.Webhook.CertDir = env.WebhookInstallOptions.LocalServingCertDir

	mgr, err := modulemanager.New(ctx, restCfg, cfg)
	g.Expect(err).NotTo(HaveOccurred())

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

	caPool := x509.NewCertPool()
	g.Expect(caPool.AppendCertsFromPEM(env.WebhookInstallOptions.LocalServingCAData)).To(BeTrue(),
		"envtest must provide a CA for the generated serving certificate")
	webhookClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: caPool, MinVersion: tls.VersionTLS12},
		},
	}
	t.Cleanup(webhookClient.CloseIdleConnections)
	webhookURL := fmt.Sprintf("https://127.0.0.1:%d/", cfg.Controller.Webhook.Port)
	g.Eventually(func() error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, webhookURL, nil)
		if err != nil {
			return err
		}
		response, err := webhookClient.Do(request)
		if err != nil {
			return err
		}
		if err := response.Body.Close(); err != nil {
			return err
		}
		if response.StatusCode != http.StatusNotFound {
			return fmt.Errorf(
				"expected HTTP %d from an unregistered path, got %d",
				http.StatusNotFound,
				response.StatusCode,
			)
		}
		return nil
	}).WithTimeout(30*time.Second).Should(Succeed(),
		"the TLS webhook listener should be reachable while no handler is registered")

	clientset, err := kubernetes.NewForConfig(restCfg)
	g.Expect(err).NotTo(HaveOccurred())
	validating, err := clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(
		ctx,
		metav1.ListOptions{},
	)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(validating.Items).To(BeEmpty(), "no validating webhook configuration should be installed")
	mutating, err := clientset.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(mutating.Items).To(BeEmpty(), "no mutating webhook configuration should be installed")
}
