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

`make test` and `make test-envtest` run without an external cluster. Envtest needs API server and etcd assets selected by `setup-envtest`. The [CI workflow](.github/workflows/ci.yaml) runs the fast checks; the separate [E2E workflow](.github/workflows/e2e.yaml) runs the Internal and External provider claim lifecycles in Kind with cert-manager present and absent. Run the cluster-backed integration suite yourself when your change touches that surface.

`make test-integration` applies CRDs to the kubeconfig cluster only when its current context matches `TEST_INTEGRATION_CONTEXT` (default: the configured Kind cluster). Set that variable explicitly to use another test cluster, such as OpenShift. The suite requires exclusive use of the cluster-scoped `DatabaseService/default-db-operator` and will not reuse or delete an existing instance.

`make test-e2e-providers` runs the Internal and External provider claim lifecycles on an existing, isolated Kind cluster. The current context must match `kind-$(KIND_CLUSTER)`. Set `E2E_CERT_MANAGER_PROFILE` to `present` or `absent` to match the cluster: the present profile requires cert-manager, and the absent profile requires it to be missing. The target needs an administrator Secret for the External PostgreSQL instance; to create a disposable local instance and Secret, run `bash test/e2e/scripts/setup-external-postgres.sh <kind-cluster-name>`. The [E2E workflow](.github/workflows/e2e.yaml) shows the full setup.
