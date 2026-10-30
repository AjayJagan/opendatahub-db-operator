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

package manager

import (
	corev1 "k8s.io/api/core/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/opendatahub-db-operator/pkg/resources/labels"
)

// SecretScopedCache returns cache options that restrict the Secret informer
// to Secrets carrying the operator's managed label (RHOAIENG-96275). Without
// this restriction the informer caches every Secret in the cluster.
//
// Two invariants follow, and both are load-bearing:
//   - Secrets the operator does not label (administrator-supplied External
//     admin Secrets, cert-manager-issued TLS Secrets, and pre-existing
//     operator Secrets from before the label existed) never enter the cache,
//     so every read that gates a decision must go through the manager's API
//     reader instead of the cached client. A cached read of such a Secret
//     returns NotFound while the Secret exists.
//   - Controllers that watch Secrets via Owns rely on the watched Secrets
//     carrying the label; the deploy actions stamp it on claim credentials
//     and the Internal admin Secret carries it in its rendered form.
func SecretScopedCache() cache.Options {
	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {
				Label: k8slabels.Set{labels.ManagedBy: labels.ManagedByValue}.AsSelector(),
			},
		},
	}
}

// NewClientWithUncachedSecrets builds the manager's client with Secret reads
// bypassing the informer cache entirely (RHOAIENG-96275).
//
// The label selector in SecretScopedCache scopes typed Secret informers, but
// controller-runtime resolves per-GVK cache options only for typed reads:
// reads issued as unstructured objects (the platform-utilities client wrapper
// converts every typed read, and the deploy action looks resources up as
// unstructured) fall through to an unscoped informer that caches every Secret
// in the cluster. Routing Secret reads around the cache closes that path with
// no informer at all, and makes those reads strongly consistent.
//
// The Owns(Secret) watch is unaffected: source.Kind requests the typed,
// label-scoped informer directly from the cache.
func NewClientWithUncachedSecrets(config *rest.Config, options client.Options) (client.Client, error) {
	if options.Cache == nil {
		options.Cache = &client.CacheOptions{}
	}
	options.Cache.DisableFor = append(options.Cache.DisableFor, &corev1.Secret{})
	return client.New(config, options)
}
