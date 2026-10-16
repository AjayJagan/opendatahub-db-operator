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

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"

	infraApi "github.com/opendatahub-io/opendatahub-db-operator/api/infrastructure/v1alpha1"
	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/postgres"
)

const (
	// maxRoleNameLen is PostgreSQL's identifier length limit.
	maxRoleNameLen = 63
)

var nonIdentRe = regexp.MustCompile(`[^a-z0-9_]`)

// RoleNameFor derives a deterministic, PostgreSQL-safe role name from a claim's
// namespace and name. A short hash of the original (unsanitized) identity is
// always appended so distinct claim names that sanitize to the same string
// (e.g. my-db vs my.db) cannot collide onto one role.
func RoleNameFor(namespace, name string) string {
	raw := fmt.Sprintf("%s_%s", namespace, name)
	safe := nonIdentRe.ReplaceAllString(raw, "_")
	h := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))[:8]
	baseMax := maxRoleNameLen - 9 // "_" + 8 hex
	if len(safe) > baseMax {
		safe = safe[:baseMax]
	}
	return safe + "_" + h
}

// NewClient loads the provider's admin connection config and opens a postgres.Client.
// Extracted from the identical openPool helpers in schemaclaim and databaseclaim.
func NewClient(
	ctx context.Context,
	cli client.Client,
	provider *infraApi.DatabaseProvider,
	cfg *moduleconfig.Config,
	factory postgres.ClientFactory,
) (postgres.Client, postgres.Config, error) {
	providerCfg, err := LoadProviderConfig(ctx, cli, provider, cfg.OperatorNamespace)
	if err != nil {
		return nil, postgres.Config{}, err
	}
	dbClient, err := OpenClient(ctx, providerCfg, factory)
	return dbClient, providerCfg, err
}

func OpenClient(
	ctx context.Context,
	providerCfg postgres.Config,
	factory postgres.ClientFactory,
) (postgres.Client, error) {
	if factory == nil {
		factory = postgres.DefaultClientFactory
	}
	dbClient, err := factory(ctx, providerCfg)
	if err != nil {
		return nil, fmt.Errorf(
			"opening postgres client: %w",
			postgres.SanitizeError(err, providerCfg.Password),
		)
	}
	return dbClient, nil
}

func ResolveDatabase(
	override string,
	providerCfg postgres.Config,
) string {
	if override != "" {
		return override
	}
	return providerCfg.DBName
}

func ExternalProviderAllows(
	provider *infraApi.DatabaseProvider,
	capability infraApi.ExternalCapability,
) bool {
	if provider == nil {
		return false
	}
	if provider.Spec.Type != infraApi.ProviderTypeExternal {
		return true
	}
	if provider.Spec.External == nil {
		return false
	}
	return slices.Contains(provider.Spec.External.Capabilities, capability)
}

// WrapQuickRetry wraps err with an operation label and upgrades it to a
// quick-retry signal when it's a connection-refused error. Extracted from the
// identical wrapQuickRetry helpers in schemaclaim and databaseclaim.
func WrapQuickRetry(op string, err error) error {
	if err == nil {
		return nil
	}
	wrapped := fmt.Errorf("%s: %w", op, err)
	if retryErr := StopWithQuickRetryIfConnectionRefused(wrapped); retryErr != nil {
		return retryErr
	}
	return wrapped
}

// ClearRoleOwnershipInDatabase opens an admin connection to database and
// reassigns/drops objects owned by role so DropRole can succeed. No-op when
// database is missing.
func ClearRoleOwnershipInDatabase(
	ctx context.Context,
	adminClient postgres.Client,
	adminCfg postgres.Config,
	factory postgres.ClientFactory,
	database string,
	role string,
) error {
	if database == "" || role == "" {
		return nil
	}
	exists, err := postgres.DatabaseExists(ctx, adminClient, database)
	if err != nil {
		return fmt.Errorf("checking database %q before ownership cleanup: %w", database, err)
	}
	if !exists {
		return nil
	}

	dbCfg := adminCfg
	dbCfg.DBName = database
	dbClient, err := OpenClient(ctx, dbCfg, factory)
	if err != nil {
		return fmt.Errorf("opening postgres client for database %q: %w", database, err)
	}
	defer dbClient.Close()

	newOwner := adminCfg.User
	if newOwner == "" {
		newOwner = dbClient.Config().User
	}
	if err := postgres.ClearRoleOwnedObjects(ctx, dbClient, role, newOwner); err != nil {
		return err
	}
	return nil
}

// ShouldDropClaimDatabase reports whether cleanup may DROP the claim database.
// Requires DeletionPolicy=Delete, status.databaseCreated, a named non-default
// database, and no other DatabaseClaim still referencing it on the same provider.
func ShouldDropClaimDatabase(
	claim *infraApi.DatabaseClaim,
	providerDefaultDB string,
	others []infraApi.DatabaseClaim,
) bool {
	if claim == nil {
		return false
	}
	if claim.Spec.DeletionPolicy != infraApi.DeletionPolicyDelete {
		return false
	}
	if !claim.Status.DatabaseCreated {
		return false
	}
	if claim.Spec.Database == "" {
		return false
	}
	database := claim.Status.Database
	if database == "" {
		database = claim.Spec.Database
	}
	if database == "" || database == providerDefaultDB {
		return false
	}
	provider := claim.Status.Provider
	if provider == "" {
		provider = claim.Spec.Provider.Name
	}
	for i := range others {
		other := &others[i]
		if other.UID == claim.UID {
			continue
		}
		if other.Namespace == claim.Namespace && other.Name == claim.Name {
			continue
		}
		otherProvider := other.Status.Provider
		if otherProvider == "" {
			otherProvider = other.Spec.Provider.Name
		}
		if otherProvider != provider {
			continue
		}
		otherDB := other.Status.Database
		if otherDB == "" {
			otherDB = other.Spec.Database
		}
		if otherDB == database {
			return false
		}
	}
	return true
}

// ListDatabaseClaims returns all DatabaseClaims visible to cli.
func ListDatabaseClaims(ctx context.Context, cli client.Client) ([]infraApi.DatabaseClaim, error) {
	list := &infraApi.DatabaseClaimList{}
	if err := cli.List(ctx, list); err != nil {
		return nil, err
	}
	return list.Items, nil
}
