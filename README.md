# opendatahub-db-operator

The Open Data Hub database operator provides the shared database infrastructure service described by
[ODH-ADR-Operator-0017](https://github.com/opendatahub-io/architecture-decision-records/blob/main/architecture-decision-records/operator/ODH-ADR-Operator-0017-shared-database-infrastructure-service.md).
It runs as an ODH platform module and reconciles the cluster-scoped `DatabaseService` resource.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development workflows and [ARCHITECTURE.md](ARCHITECTURE.md)
for design details.

## Install and use

The [installation and usage guide](docs/installation-and-usage.md) covers enabling the module,
standalone chart installation, provider and claim configuration, status, support, and limitations.
