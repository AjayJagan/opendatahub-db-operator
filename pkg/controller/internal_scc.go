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
	"encoding/json"
	"fmt"

	"github.com/opendatahub-io/odh-platform-utilities/framework/cluster"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/postgres/instance"
	"github.com/opendatahub-io/opendatahub-db-operator/pkg/resources/gvk"
	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// InternalPostgresSCCName is the shared cluster-scoped SCC for Internal
	// Postgres pods (UID/GID 26). One SCC serves all Internal providers.
	InternalPostgresSCCName = "odh-db-internal-postgres"

	// InternalPostgresSCCClusterRoleName grants use of InternalPostgresSCCName.
	InternalPostgresSCCClusterRoleName = "odh-db-internal-postgres"
)

// SecurityContextConstraintsAvailable reports whether the OpenShift SCC API is
// present. Kind and vanilla Kubernetes return false.
func SecurityContextConstraintsAvailable(cli client.Client) (bool, error) {
	return cluster.IsAPIAvailable(cli, gvk.SecurityContextConstraints)
}

// DesiredInternalPostgresSCC returns the restricted-v2-shaped SCC limited to
// RHEL postgresql UID/GID 26.
func DesiredInternalPostgresSCC() *securityv1.SecurityContextConstraints {
	uid := instance.InternalPostgresUID
	idRange := []securityv1.IDRange{{Min: uid, Max: uid}}
	return &securityv1.SecurityContextConstraints{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gvk.SecurityContextConstraints.GroupVersion().String(),
			Kind:       gvk.SecurityContextConstraints.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: InternalPostgresSCCName,
		},
		AllowPrivilegedContainer: false,
		AllowHostDirVolumePlugin: false,
		AllowHostNetwork:         false,
		AllowHostPorts:           false,
		AllowHostPID:             false,
		AllowHostIPC:             false,
		ReadOnlyRootFilesystem:   false,
		AllowPrivilegeEscalation: new(false),
		RequiredDropCapabilities: []corev1.Capability{
			"ALL",
		},
		AllowedCapabilities:    nil,
		DefaultAddCapabilities: nil,
		Volumes: []securityv1.FSType{
			securityv1.FSTypeConfigMap,
			securityv1.FSTypeDownwardAPI,
			securityv1.FSTypeEmptyDir,
			securityv1.FSTypePersistentVolumeClaim,
			securityv1.FSProjected,
			securityv1.FSTypeSecret,
		},
		SELinuxContext: securityv1.SELinuxContextStrategyOptions{
			Type: securityv1.SELinuxStrategyMustRunAs,
		},
		RunAsUser: securityv1.RunAsUserStrategyOptions{
			Type: securityv1.RunAsUserStrategyMustRunAs,
			UID:  &uid,
		},
		SupplementalGroups: securityv1.SupplementalGroupsStrategyOptions{
			Type:   securityv1.SupplementalGroupsStrategyMustRunAs,
			Ranges: idRange,
		},
		FSGroup: securityv1.FSGroupStrategyOptions{
			Type:   securityv1.FSGroupStrategyMustRunAs,
			Ranges: idRange,
		},
		SeccompProfiles: []string{"runtime/default"},
	}
}

// DesiredInternalPostgresSCCClusterRole grants use of the shared Internal SCC.
func DesiredInternalPostgresSCCClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: InternalPostgresSCCClusterRoleName,
		},
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{gvk.SecurityContextConstraints.Group},
			Resources:     []string{"securitycontextconstraints"},
			ResourceNames: []string{InternalPostgresSCCName},
			Verbs:         []string{"use"},
		}},
	}
}

// DesiredInternalPostgresSCCRoleBinding binds the Internal Postgres SA to the
// shared SCC-use ClusterRole in the pod's namespace.
func DesiredInternalPostgresSCCRoleBinding(namespace, serviceAccountName, providerName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "RoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      InternalSCCRoleBindingName(providerName),
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":                    providerName,
				"app.kubernetes.io/component":               "databaseprovider",
				"db.infrastructure.opendatahub.io/provider": providerName,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     InternalPostgresSCCClusterRoleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      serviceAccountName,
			Namespace: namespace,
		}},
	}
}

// EnsureInternalPostgresSCC applies the shared SCC and use ClusterRole when
// available is true. Both are cluster-scoped and must not carry a
// DatabaseProvider owner reference. Callers must pass a single discovery
// result shared with RoleBinding creation.
func EnsureInternalPostgresSCC(ctx context.Context, cli client.Client, available bool) error {
	if !available {
		return nil
	}

	if err := ensureSCC(ctx, cli, DesiredInternalPostgresSCC()); err != nil {
		return fmt.Errorf("ensuring Internal Postgres SCC %s: %w", InternalPostgresSCCName, err)
	}
	if err := ensureClusterRole(ctx, cli, DesiredInternalPostgresSCCClusterRole()); err != nil {
		return fmt.Errorf("ensuring Internal Postgres SCC ClusterRole %s: %w", InternalPostgresSCCClusterRoleName, err)
	}
	return nil
}

func ensureClusterRole(ctx context.Context, cli client.Client, desired *rbacv1.ClusterRole) error {
	return ensureObject(ctx, cli, desired, &rbacv1.ClusterRole{}, func(live client.Object) {
		live.(*rbacv1.ClusterRole).Rules = desired.Rules
	})
}

func ensureSCC(ctx context.Context, cli client.Client, desired *securityv1.SecurityContextConstraints) error {
	raw, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("marshaling SCC: %w", err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(raw); err != nil {
		return fmt.Errorf("decoding SCC unstructured: %w", err)
	}
	u.SetGroupVersionKind(gvk.SecurityContextConstraints)

	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(gvk.SecurityContextConstraints)
	return ensureObject(ctx, cli, u, live, func(existing client.Object) {
		dst := existing.(*unstructured.Unstructured)
		rv := dst.GetResourceVersion()
		uid := dst.GetUID()
		dst.Object = u.DeepCopy().Object
		dst.SetResourceVersion(rv)
		dst.SetUID(uid)
		dst.SetGroupVersionKind(gvk.SecurityContextConstraints)
	})
}

// ensureObject creates desired when missing, otherwise patches live after
// mutate copies desired fields onto a deep copy of live. Concurrent Creates
// are ignored (AlreadyExists) and followed by a get+patch. Patches use
// optimistic locking so stale resourceVersions fail instead of silently
// overwriting.
func ensureObject(
	ctx context.Context,
	cli client.Client,
	desired client.Object,
	live client.Object,
	mutate func(live client.Object),
) error {
	key := client.ObjectKeyFromObject(desired)
	err := cli.Get(ctx, key, live)
	switch {
	case apierrors.IsNotFound(err):
		if err := cli.Create(ctx, desired); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return err
			}
			if err := cli.Get(ctx, key, live); err != nil {
				return err
			}
		} else {
			return nil
		}
	case err != nil:
		return err
	}

	return patchLive(ctx, cli, live, mutate)
}

func patchLive(
	ctx context.Context,
	cli client.Client,
	live client.Object,
	mutate func(live client.Object),
) error {
	base := live.DeepCopyObject().(client.Object)
	patched := live.DeepCopyObject().(client.Object)
	mutate(patched)
	return cli.Patch(ctx, patched, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}
