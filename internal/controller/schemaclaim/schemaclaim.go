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

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/actions/deploy"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/predicates/hash"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/reconciler"
	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	dbcontroller "github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
	resourcelabels "github.com/opendatahub-io/opendatahub-db-operator/pkg/resources/labels"
)

type Controller struct {
	Options
}

func NewController(cfg *moduleconfig.Config, opts ...Option) *Controller {
	m := &Controller{Options: Options{cfg: cfg, platformRelease: cfg.PlatformRelease()}}
	for _, opt := range opts {
		if opt != nil {
			opt.applyOption(&m.Options)
		}
	}
	return m
}

// +kubebuilder:rbac:groups=infrastructure.opendatahub.io,resources=schemaclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=infrastructure.opendatahub.io,resources=schemaclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.opendatahub.io,resources=schemaclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.opendatahub.io,resources=databaseproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func NewReconciler(ctx context.Context, mgr ctrl.Manager, cfg *moduleconfig.Config, opts ...Option) error {
	m := NewController(cfg, opts...)

	// Owns(Secret) heals credentials Secret drift: deletion heals by
	// rotation, non-secret field edits restore on the next reconcile, and a
	// tampered password heals by rotation once it fails the credential
	// stamp. The manager's Secret informer is label-scoped to
	// operator-managed Secrets (RHOAIENG-96275), so the watch sees only
	// claim credentials. The hash predicate fires on content changes only
	// (Secrets never bump metadata.generation, so generation-based
	// predicates would fire on every resourceVersion bump), and the
	// ownerReferences predicate covers ownership changes the hash strips.
	_, err := reconciler.ReconcilerFor(mgr, &infraApi.SchemaClaim{}).
		Watches(
			&infraApi.DatabaseProvider{},
			reconciler.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
			reconciler.WithEventMapper(
				dbcontroller.BroadcastListMapper(mgr.GetClient(), &infraApi.SchemaClaimList{}),
			)).
		Owns(&corev1.Secret{}, reconciler.WithPredicates(predicate.Or(
			hash.Updated(),
			dbcontroller.OwnerReferencesChanged(),
		))).
		WithReconcilerOpts(
			reconciler.WithRelease(m.platformRelease),
			reconciler.WithDefaultRequeueAfter(retryInterval(cfg)),
			reconciler.WithFinalizerName(FinalizerName),
		).
		WithFinalizer(m.cleanupAction).
		WithAction(m.provisionAction).
		WithAction(deploy.NewAction(
			deploy.WithCache(),
			deploy.WithApplyOrder(),
			deploy.WithLabel(resourcelabels.ManagedBy, resourcelabels.ManagedByValue),
		)).
		WithConditions(
			conditions.Dependent(ConditionProvisioned, conditions.HealthyWhenTrue),
			conditions.Dependent(ConditionTLSConfiguration, conditions.HealthyWhenTrue),
		).
		Build(ctx)

	return err
}
