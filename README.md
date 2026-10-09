# opendatahub-db-operator

The Open Data Hub database operator provides the shared database infrastructure service described by
[ODH-ADR-Operator-0017](https://github.com/opendatahub-io/architecture-decision-records/blob/main/architecture-decision-records/operator/ODH-ADR-Operator-0017-shared-database-infrastructure-service.md).
It runs as an ODH platform module and reconciles the cluster-scoped `DatabaseService` resource.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development workflows and [ARCHITECTURE.md](ARCHITECTURE.md)
for design details.

## Installing this chart

The chart does not set a namespace in `values.yaml`; install it in `odh-db-operator-system`, the
namespace used by this operator's kustomize bundle, or choose another namespace:

```sh
helm install opendatahub-db-operator config/chart --create-namespace --namespace odh-db-operator-system
```

After installation, apply the cluster-scoped `DatabaseService` singleton and wait for it to become Ready:

```sh
kubectl apply -f - <<'EOF'
apiVersion: services.platform.opendatahub.io/v1alpha1
kind: DatabaseService
metadata:
  name: default-db-operator
EOF
kubectl wait --for=condition=Ready databaseservice/default-db-operator --timeout=5m
```

Helm installs the `DatabaseService` CRD from `config/chart/crds/` on first install but skips CRD
changes on upgrade. Apply an updated schema separately with `kubectl apply -f config/chart/crds/`
alongside `helm upgrade`.
