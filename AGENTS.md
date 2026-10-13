# Agent instructions

## Always load

Always load [ARCHITECTURE.md](ARCHITECTURE.md) for repository work.

## Load for these tasks

- [CONTRIBUTING.md](CONTRIBUTING.md): `When building, testing, debugging, or reviewing a change`.
- [Code Standards](docs/appendix/code-standards.md): `When writing or reviewing code, scripts, or CI/build configuration in this repo`.
- [Project documentation standard](docs/appendix/project-documentation.md): `When creating, reorganizing, or reviewing project documentation`.

## AI-specific gotchas

- For the `DatabaseService` CRD's Helm upgrade behavior, see [README.md's installation section](README.md#installing-this-chart).
- Don't add implementation-status or phase-history claims to [README.md](README.md) or [ARCHITECTURE.md](ARCHITECTURE.md); past drafts repeatedly did this. See the ["Keep out" guidance](docs/appendix/project-documentation.md#readmemd) for [both files](docs/appendix/project-documentation.md#architecturemd).
- For test-level selection and environment requirements, see [CONTRIBUTING.md](CONTRIBUTING.md#test-environments) and [Code Standards](docs/appendix/code-standards.md#tests).
- Before changing API scope, resource names, or Secret keys, check [Architecture's stable contracts](ARCHITECTURE.md#stable-contracts).
