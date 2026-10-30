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

// Package labels defines shared resource labels. It must stay dependency-free
// so every layer (manager, controllers, rendered resources) can reference the
// same constants without import cycles.
package labels

const (
	// ManagedBy labels resources this operator owns. The manager scopes its
	// Secret informer to this label (RHOAIENG-96275) so only operator-written
	// Secrets are cached; Secrets without it must be read through the
	// manager's API reader, never the cached client.
	ManagedBy = "app.opendatahub.io/db-operator"

	// ManagedByValue is the value paired with ManagedBy.
	ManagedByValue = "true"
)
