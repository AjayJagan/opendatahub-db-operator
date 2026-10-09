---
description: General standards for code, scripts, build targets, and CI configuration in this repository
audience: [human, ai]
load: When writing or reviewing code, scripts, or CI/build configuration in this repo
---

# Code Standards

## General guidance

### Comments

Use comments only for context the source cannot show.

- Explain a non-obvious reason, constraint, invariant, or trade-off. Do not narrate statements, restate declarations or values, or describe routine steps; clarify the names or structure instead.
- Link platform quirks and workarounds to the relevant issue or documentation, and remove them when they no longer apply.
- Use structural grouping or functional section markers instead of decorative comment banners. Remove stale commented-out logic; keep a disabled configuration example only when nearby context explains its active constraint. The [TLS lint workflow](../../.github/workflows/tls-lint.yml) keeps a commented suppression setting with its false-positive rationale.
- Give follow-up notes a trackable issue and a condition for removing the note. Retain legally required notices.

### Failures and expected results

Propagate a failure when the caller must stop, recover, retry, or report it. Represent expected validation findings and policy decisions as results or status, not operational failures.

Add useful operation context at component and adapter boundaries while preserving the original cause. Do not turn a failure into unrelated text or add empty, repetitive context at every layer.

### Logging

Match severity and detail to operational significance. Log meaningful milestones and state transitions at the ordinary level; reserve detailed diagnostics for higher verbosity. Include the operation and useful identifiers as structured fields, and never log credentials, tokens, or other sensitive values.

Report a failure at the layer that owns its outcome. Do not log it before returning it to a layer that may log it again.

### Lint and static analysis

Treat findings as signals about correctness, naming, or design. Fix the underlying issue where practical; do not suppress a finding as the first response.

Use a suppression only for a narrow exception where the rule does not apply. Put it beside the affected code and explain the reason.

### Tests

Make each test prove behavior that could regress. Check observable results, errors, or state changes; a test that passes when the implementation is removed or broken is a liar test. A test that only calls code and checks that it does not panic proves too little.

- Keep expected values independent of the implementation; do not reproduce the tested algorithm in the assertion.
- Keep mock setup proportional to the behavior under test. Use focused fakes at unit boundaries and exercise real boundaries in integration tests.
- Match test scope to the boundary: unit tests should not need a cluster, and integration tests should not mock away the integration.
- Avoid fixed-delay waits, uncontrolled shared mutable state, and order-dependent assertions. Wait for real conditions with a bound; fix the cause of flakiness instead of adding retries.
- Test project behavior, not guarantees supplied by the language or test framework.
- Give shared input cases separate names so a failure identifies its case. Avoid aggregate assertion loops that hide which case failed; use a loop when checking a collection as a whole.
- Consolidate repeated setup that differs only by input into case data or a small helper, while keeping case-specific details visible. Keep assertions for one behavior together; separate unrelated behaviors instead of duplicating setup across one-assertion tests.

Choose the cheapest test level that faithfully exercises the boundary; see [CONTRIBUTING.md's Test environments section](../../CONTRIBUTING.md#test-environments) for this repository's test tiers.

### Design and complexity

Keep each unit at one useful abstraction level with one responsibility. Separate policy, I/O, transformation, and retry behavior at meaningful boundaries, and keep dependencies explicit.

Treat excessive branching or nesting as a readability signal. Simplify with early exits, named conditions, or a meaningful helper; do not extract wrappers that add no distinct role.

### Shell and CI steps

Preserve command failures through shell blocks, pipelines, and cleanup. If a non-zero status is intentionally ignored, handle it locally and explain why; do not blanket-ignore it. See the [Makefile](../../Makefile) and [CI workflow](../../.github/workflows/ci.yaml) for their current shell-strictness settings and cleanup behavior.

## Go-specific addendum

### Error wrapping and classification

Go's `%w` form adds context while retaining the underlying error; the operator uses it when loading configuration in [`cmd/operator/operator.go`](../../cmd/operator/operator.go). Callers can inspect the wrapped cause with [`errors.Is` or `errors.As`](https://pkg.go.dev/errors):

```go
return fmt.Errorf("loading operator config: %w", err)
```

[`pkg/controller/claimerrors/claimerrors.go`](../../pkg/controller/claimerrors/claimerrors.go) defines three typed claim outcomes through `Error()` methods; they carry or unwrap no cause, so they illustrate classification rather than wrapping.

### Logging with logr and zap

[`pkg/config/zap.go`](../../pkg/config/zap.go) builds a controller-runtime zap logger and returns it as `logr.Logger`; [`cmd/operator/operator.go`](../../cmd/operator/operator.go) installs it with `ctrl.SetLogger`. `ZapConfig.Level` is parsed by `zapcore.ParseLevel`.

At call sites, [`logr.Logger`](https://pkg.go.dev/github.com/go-logr/logr#Logger) provides `Info`, `Error`, and `V(n).Info`; it has no `Debug` or `Warning` method. Use `V(1).Info` or higher verbosity for diagnostic detail. An error call takes the cause, a message, then key-value pairs: `logger.Error(err, "operation failed", "key", value)`. Follow the general severity and status guidance for expected or recovered conditions.

### Go lint findings

The [`.golangci.yml`](../../.golangci.yml) enables `gocyclo`, `dupl`, and `goconst`; its `revive` rules check `comment-spacings` and `import-shadowing`.

- Use `gocyclo` findings to review branching against [Design and complexity](#design-and-complexity).
- For `dupl`, compare whether similar blocks represent the same behavior and ownership before extracting shared code.
- For `goconst`, name repeated strings when they represent a shared concept, not merely because their text matches.
- Fix `revive` findings by correcting comment spacing or avoiding import names that shadow other identifiers.

Apply the general suppression rule with Go's `//nolint:<rule> // reason` form, for example the [`gocyclo` suppression in `cmd/chartgen/chartgen.go`](../../cmd/chartgen/chartgen.go). The `.golangci.yml` does not enable `nolintlint`, so it will not check that rationale automatically; review must.

### Go tests

Tests use Go's `testing.T` with Gomega assertions, not Ginkgo. [`pkg/config/config_support_test.go`](../../pkg/config/config_support_test.go) and [`pkg/config/flags_test.go`](../../pkg/config/flags_test.go) use `NewWithT(t)` and `Expect`, while [`test/envtest/databaseservice_test.go`](../../test/envtest/databaseservice_test.go) uses `Eventually` for asynchronous reconciliation.

Use Go's `t.Run` subtests to implement the named-case guidance in [General guidance](#tests); [`pkg/postgres/ddl_test.go`](../../pkg/postgres/ddl_test.go) shows the pattern with Gomega assertions.
