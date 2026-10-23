---
description: Non-obvious setup and verification requirements for contributors.
audience: [human, ai]
load: When building, testing, debugging, or reviewing a change
---

# Contributing

Run Make targets from the repository root. Use `make help` for the target list. See [README.md](README.md) for user installation and [ARCHITECTURE.md](ARCHITECTURE.md) for design context.

## Prerequisites

See `go.mod` for the Go version and `.github/workflows/ci.yaml` for CI's Go and helper-tool installation steps.

## Test environments

Run the narrowest suite that exercises your change. Run these commands from the repository root;
the [Makefile](Makefile) defines their setup steps and defaults.

| Suite | Command | What it checks | Environment |
| --- | --- | --- | --- |
| Unit | `make test` | Go packages outside `test/integration`, `test/envtest`, and `test/e2e`. | No cluster. |
| Envtest | `make test-envtest` | Reconciliation against a local Kubernetes API server and etcd. | No external cluster; requires `controller-gen`, `setup-envtest`, and its selected API server/etcd assets. |
| Integration | `make test-integration` | Operator behavior against the current kubeconfig cluster. | Dedicated test cluster and matching `TEST_INTEGRATION_CONTEXT`. |
| End to end | `make test-e2e-providers` | Internal and External provider claim lifecycles on Kind. | Existing isolated Kind cluster, Kind, Helm, and Podman CLIs; the cert-manager profile and External provider administrator Secret described below. |

The [CI workflow](.github/workflows/ci.yaml) runs unit and envtest suites. The separate
[E2E workflow](.github/workflows/e2e.yaml) runs both cert-manager profiles. Run the integration
suite yourself when a change touches its cluster boundary.

Before a cluster-backed suite, check `kubectl config current-context`. `make test-integration`
applies CRDs only when the current context matches `TEST_INTEGRATION_CONTEXT` (by default,
`kind-$(KIND_CLUSTER)`). Set that variable explicitly for another test cluster, such as OpenShift.
The suite needs exclusive use of the cluster-scoped `DatabaseService/default-db-operator` and will
not reuse or delete an existing instance.

`make test-e2e-providers` requires the context `kind-$(KIND_CLUSTER)` and does not create the Kind
cluster. Set `E2E_CERT_MANAGER_PROFILE=present` for a cluster with cert-manager, or `absent` for a
cluster without it. Set up a disposable External PostgreSQL instance and its administrator Secret,
then run the suite, for example:

```sh
bash test/e2e/scripts/setup-external-postgres.sh db-operator-dev
make test-e2e-providers KIND_CLUSTER=db-operator-dev E2E_CERT_MANAGER_PROFILE=present \
  E2E_EXTERNAL_ADMIN_SECRET_NAMESPACE=external-postgres \
  E2E_EXTERNAL_ADMIN_SECRET_NAME=external-postgres-admin
```

Use the `absent` profile on a cluster without cert-manager. The [E2E workflow](.github/workflows/e2e.yaml)
shows setup for both profiles. E2E setup builds and loads an image with Podman; teardown removes the
configured e2e namespace and `DatabaseService/default-db-operator`, so use an isolated cluster.
The `external-postgres` namespace created by the setup script remains after the suite. Uncached
`Containerfile` base images may require registry access.
