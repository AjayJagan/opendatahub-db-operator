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

// Package databaseservice contains the DatabaseService module-enablement
// reconciler. DatabaseService is a placeholder at this stage -- it registers
// this module with the ODH Operator without deploying a separate operand.
// The infrastructure CRDs (SchemaClaim/DatabaseClaim/DatabaseProvider) are
// out of scope for this reconciler and land in RHOAIENG-96277.
package databaseservice

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/reconciler"
	servicesv1alpha1 "github.com/opendatahub-io/opendatahub-db-operator/api/services/v1alpha1"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	dbcontroller "github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
)

// Module holds process-lifetime state for the DatabaseService controller.
// It embeds Options so that task-specific dependencies can be added via
// With* constructors without changing the Module type.
type Module struct {
	Options
}

// NewModule creates a Module with one-shot computed state.
func NewModule(cfg *moduleconfig.Config, opts ...Option) *Module {
	r := &Module{
		Options: Options{
			cfg:             cfg,
			platformRelease: cfg.PlatformRelease(),
		},
	}
	for _, opt := range opts {
		opt.applyOption(&r.Options)
	}
	return r
}

// Module operator's own CRD
// +kubebuilder:rbac:groups=services.platform.opendatahub.io,resources=databaseservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=services.platform.opendatahub.io,resources=databaseservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=services.platform.opendatahub.io,resources=databaseservices/finalizers,verbs=update

// Baseline RBAC required for all module operators. The CRD grant is
// deliberately narrower than a typical module operator's: this reconciler's
// two-action pipeline (UpgradeIfNeeded + reportStatus) never creates,
// updates, or deletes CustomResourceDefinitions, so it only needs read
// access.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create
// +kubebuilder:rbac:urls=/metrics,verbs=get

// odh-platform-utilities' reconciler records reconcile/provisioning failures
// via manager.GetEventRecorder (not the deprecated GetEventRecorderFor), which
// controller-runtime backs with the events.k8s.io/v1 broadcaster -- a
// different API group than the leader-election Role's core "events" grant,
// and one that Role (namespaced to the operator's own namespace) wouldn't
// cover for this cluster-scoped CR's events anyway.
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func NewReconciler(
	ctx context.Context,
	mgr ctrl.Manager,
	cfg *moduleconfig.Config,
	opts ...Option,
) error {
	m := NewModule(cfg, opts...)

	_, err := reconciler.ReconcilerFor(mgr, &servicesv1alpha1.DatabaseService{}).
		WithReconcilerOpts(
			reconciler.WithRelease(m.platformRelease),
			reconciler.WithDefaultRequeueAfter(retryInterval(cfg)),
		).
		WithAction(dbcontroller.UpgradeIfNeeded()).
		WithAction(m.reportStatus).
		Build(ctx)

	return err
}

// retryInterval returns the configured DatabaseService retry interval,
// falling back to the compiled default if it is unset (e.g. zero-value
// Config in tests).
func retryInterval(cfg *moduleconfig.Config) time.Duration {
	if cfg.DatabaseService.RetryInterval <= 0 {
		return moduleconfig.DefaultRetryInterval
	}
	return cfg.DatabaseService.RetryInterval
}
