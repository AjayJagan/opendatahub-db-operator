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

package databaseclaim

import (
	"time"

	moduleconfig "github.com/opendatahub-io/opendatahub-db-operator/pkg/config"
	dbcontroller "github.com/opendatahub-io/opendatahub-db-operator/pkg/controller"
)

const (
	ConditionProvisioned      = dbcontroller.ConditionProvisioned
	ConditionTLSConfiguration = dbcontroller.ConditionTLSConfiguration
	FinalizerName             = "infrastructure.opendatahub.io/databaseclaim-cleanup"
)

// retryInterval returns the configured DatabaseClaim retry interval,
// falling back to the compiled default if it is unset (e.g. zero-value
// Config in tests).
func (c *Controller) retryInterval() time.Duration {
	if c.cfg == nil || c.cfg.DatabaseClaim.RetryInterval <= 0 {
		return moduleconfig.DefaultRetryInterval
	}
	return c.cfg.DatabaseClaim.RetryInterval
}

func retryInterval(cfg *moduleconfig.Config) time.Duration {
	if cfg == nil || cfg.DatabaseClaim.RetryInterval <= 0 {
		return moduleconfig.DefaultRetryInterval
	}
	return cfg.DatabaseClaim.RetryInterval
}
