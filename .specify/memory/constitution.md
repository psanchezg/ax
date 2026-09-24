# AX Constitution

AX (this fork of `github.com/google/ax`) is a declarative, high-throughput orchestrator for
sandboxed agentic workloads on Kubernetes, written in Go. These principles govern every
change made in this repository. They are tuned to this fork's standing objective: develop
new features here and contribute **only those specific, self-contained commits** to
upstream (`github.com/google/ax`). Everything MUST be read through that lens: a change is
not done when it works locally; it is done when it is an isolated, reviewable, upstreamable
unit of work.

## Core Principles

### I. Fork-First Contribution Discipline (NON-NEGOTIABLE)

Every change MUST be shaped so that it can be contributed upstream as specific commits
without dragging fork-local noise along.

- **One feature per branch, one branch per purpose.** Each feature or fix lives on its own
  branch cut from an up-to-date `upstream/main`. Branch names follow
  `<type>/<short-slug>` (e.g. `feat/task-env-templating`), with `<type>` ∈
  {feat, fix, docs, refactor, test, chore, build}.
- **Commits are atomic and independently meaningful.** Each commit addresses exactly one
  logical change, compiles and passes tests on its own, and is independently
  cherry-pickable. A commit that mixes refactoring with behavior change MUST be split.
- **Commits MUST be Conventional Commits** matching upstream history
  (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, `build:`, `ci:`) with a
  concise imperative subject and a body explaining *why* when the diff does not speak for
  itself.
- **Upstream-bound branches stay pristine.** Fork-local artifacts — SonarQube config,
  local tooling, personal docs, CI experiments, scratch reports (coverage.out,
  test-report.out, golangci-lint-report.xml) — MUST NOT appear in commits intended for
  upstream. Keep them on separate fork-only branches or untracked.
- **Rebase, never merge, onto `upstream/main`.** History of an upstream-bound branch MUST
  stay linear and free of merge commits and of commits from other feature branches.
- **Upstreamability is verifiable, not assumed.** Before opening an upstream PR, the
  branch MUST pass the cherry-pick test:
  `git format-patch upstream/main..<branch>` applies cleanly on a fresh `upstream/main`
  checkout and the result builds and passes `make test` there.
- **Respect upstream process.** A Google CLA is required; every upstream PR needs review;
  AI-assisted code is permitted under the CLA but MUST be disclosed per
  CONTRIBUTING.md, and the author remains responsible for every line.

**Rationale:** The whole point of this fork is surgical contribution. If fork-local
changes and feature work share commits, upstreaming becomes archaeology. Atomic,
conventional, branch-scoped commits make `git cherry-pick` / `git format-patch` the
natural export path and keep every PR small enough to review.

### II. Code Quality & Architectural Discipline

All changes MUST preserve the established layered architecture and Go engineering
standards.

- **Keep the layers clean.** `cmd/*` entrypoints stay thin (flags, wiring, `main` only);
  implementation lives in `internal/*`; the public, importable surface lives in `pkg/*`
  and `runner/`. Business logic MUST NOT leak into `main` packages, and `internal/*`
  packages MUST NOT be imported by external consumers by construction.
- **Preserve the extension seams.** Storage goes through the `store.Store` interface
  (Redis and in-memory implementations); substrate access goes through the substrate
  client boundary; the `runner` package MUST remain embeddable by custom runner images
  with `cmd/ax-task-runner` as a thin wrapper. New capabilities extend these seams rather
  than bypassing them.
- **Honor the API conventions.** The gRPC service follows `ax.v1alpha1` with
  `<Method>Request` / `<Method>Response` messages; manifests follow
  `apiVersion: ax.io/v1alpha1`, `kind`, `metadata.name`, `spec`, `status`. Deviations
  MUST be justified in-PR; proto changes regenerate `pkg/apis/v1alpha1` and land in the
  same commit as the `.proto` change.
- **Write idiomatic, defensive Go.** `context.Context` is the first parameter; errors are
  wrapped with `%w` and carry actionable context; exported identifiers are documented;
  concurrency is explicit (no unprotected shared state; goroutines have documented
  lifetimes); `gofmt`, `go vet`, and `go mod tidy` MUST be clean.
- **Every source file keeps the Apache 2.0 license header** in the exact form used by
  existing files ("Copyright 2026 Google LLC" block).

**Rationale:** The layered layout, interface seams, and API conventions are what let the
CLI, server, controller, and runner evolve independently at scale. Drift multiplies
conflict with upstream and shrinks the set of commits that can be contributed cleanly.

### III. Test-Backed Change (NON-NEGOTIABLE)

Every behavioral change MUST ship with automated tests, and the suite is a hard gate.

- **Tests land with the change, in the same commit.** Table-driven `*_test.go` files are
  colocated with the code they test and follow existing naming (`worker_test.go`,
  `reconciler_test.go`, ...). Bug fixes MUST include a regression test that fails without
  the fix.
- **Tests are deterministic and hermetic.** No real network, no real cluster, no wall-clock
  races: externalities are replaced by the mock Substrate gRPC server, the in-memory store,
  and fakes at package boundaries. Parallel tests MUST NOT share mutable state.
- **Coverage never regresses on touched code.** New packages and exported functions need
  coverage of success, error, and edge paths; `make coverage` quantifies the result.
- **The suite is a merge gate.** `make test` (`go test ./...`) MUST pass locally before a
  commit is made and in CI before merge. A red suite blocks the commit — fix forward or
  revert; never land "temporary" breakage on an upstream-bound branch.
- **Weakened tests are violations.** Deleting, skipping, or loosening an assertion to make
  a change pass requires explicit in-PR justification and, for invariants (store contract,
  reconciler phase transitions), maintainer agreement.

**Rationale:** The reconciler, store, and runner carry subtle lifecycle and state-machine
semantics (phases, conditions, suspend/resume). Only exhaustive tests keep a feature
branch from silently breaking behavior upstream reviewers cannot see.

### IV. API, CLI & Documentation Consistency

AX presents one coherent surface: kubectl-shaped CLI, declarative manifests, and docs that
match reality.

- **Reuse the established verb vocabulary.** The CLI keeps `apply`, `get`, `describe`,
  `watch`, `delete`, `suspend`, `resume`, `ssh`, `ctx`, `tunnel`, `version`, and global
  flags (`-a/--atespace`, `-n/--namespace`, `--context`, `--server`). New verbs MUST NOT
  be invented when an existing one fits; a genuinely new verb MUST be justified in-PR.
- **Status speaks through phases and conditions.** Lifecycle reporting follows the
  `status.phase` + conditions model (`WorkspaceReady`, `GatewayReady`, `Ready`); new
  resources or lifecycle steps MUST extend that model with documented condition reasons
  rather than ad-hoc status fields.
- **User-visible behavior changes update docs in the same commit.** Affected homes:
  `README.md` (CLI surface), `docs/concepts.md` (semantics), `docs/manifests.md` (every
  kind has an annotated example), `docs/` guides, `DESIGN.md` (architecture and API
  reference), and `examples/`. Docs and examples MUST compile/validate against the code
  they describe.
- **Output is predictable and scriptable.** Errors go to stderr with a non-zero exit and
  an actionable message; machine-consumable output stays stable across commits; human
  tables keep the existing column conventions.

**Rationale:** Predictability is the product. Consistent verbs, conditions, and docs are
also what make upstream PRs reviewable — reviewers can judge a change against documented
conventions instead of taste.

### V. Simplicity, Dependency & Performance Discipline

AX is designed to run billions of short-lived tasks per cluster; every addition is paid
for at scale.

- **Start simple; YAGNI applies.** Implement the smallest design that satisfies the
  current requirement within the four primitives (`Task`, `Workspace`, `Gateway`,
  `Model`). Cross-cutting or speculative abstractions MUST be agreed before implementation
  and justified in the plan's Complexity Tracking.
- **Zero new dependencies by default.** The runtime set is intentionally lean
  (go-redis, grpc, protobuf, yaml.v3, agent-substrate). A new dependency requires
  justification that the standard library or existing deps cannot serve the need, and MUST
  be pinned to a minimum version with `go mod tidy` clean.
- **Design for the hot path.** Redis usage (hashes, streams, pub/sub) MUST assume
  millions of short-lived tasks: bounded payloads, no unbounded scans or keys, no per-task
  round trips where a batch or stream read works. Reconciliation MUST be idempotent and
  safe to retry — at-least-once delivery is assumed.
- **All I/O is bounded and cancellable.** Every external call (Redis, gRPC, substrate,
  tunnels, guest/metadata services) takes a `context.Context`, sets timeouts, and handles
  cancellation; `cmd/*` binaries shut down gracefully on signals.
- **Fail loudly and observably.** Health endpoints (`/healthz`), phase/condition
  transitions, and structured log messages are the minimum observability contract for new
  components.

**Rationale:** Cost and latency multiply by billions of tasks here. Small primitives,
idempotent reconciliation, bounded I/O, and a lean dependency graph are what keep the
system fast, cheap to operate, and cheap to rebase against upstream.

## Security, Isolation & Compatibility Constraints

- **Sandbox isolation is inviolable.** Task containers run with declared CPU/memory
  limits; nothing in control-plane code MAY weaken workspace fencing or egress filtering.
  Network exposure MUST flow through `Gateway` egress allowlists — deny-by-default,
  explicit hosts and ports only.
- **Secrets never touch the tree.** Model credentials live in Kubernetes secrets
  referenced by name from `Model` resources. API keys, tokens, kubeconfigs, and internal
  URLs MUST NOT appear in source, tests, docs, examples, or commit history; examples use
  obvious placeholders.
- **Supply chain hygiene.** GitHub Actions in `.github/workflows/` are pinned to full
  commit SHAs with a version comment (existing convention), and workflow permissions stay
  minimal (`contents: read` unless justified). Generated artifacts (`*.pb.go`,
  coverage/lint reports) are never hand-edited and never mixed into unrelated commits.
- **`v1alpha1` breaking changes are coordinated, not silent.** The API is pre-stable by
  design; a breaking manifest, RPC, or status change MUST be confined to its own commit,
  called out in the commit body and PR, and reflected in docs/examples in the same change.
- **Licensing is uniform.** Contributions are original work under the Apache 2.0 header
  convention and are covered by the Google CLA. Copyleft or incompatibly licensed code is
  prohibited.

## Fork Workflow, Commit Conventions & Quality Gates

- **Development loop.** Cut a branch from `upstream/main` → implement → verify → commit
  in logical units → push to `origin` → open the PR upstream (or against the fork for
  pre-review). Local forks of upstream (`git pull --rebase upstream main`) happen before
  every verification pass on an upstream-bound branch.
- **Verification gate before every commit:**
  - `make test` passes (or `go test ./...`);
  - `make build` produces `bin/ax`, `bin/ax-controller`, `bin/ax-server`;
  - `go mod tidy` leaves `go.mod`/`go.sum` unchanged
    (`git diff --exit-code go.mod go.sum`);
  - `gofmt -l .` and `go vet ./...` are clean;
  - touched behavior has/updates tests (Principle III) and docs (Principle IV).
- **PR discipline.** PRs are focused: one feature or fix, with motivation, summary of
  changes, and verification notes in the description; large or cross-cutting changes are
  discussed with maintainers before implementation; AI assistance is disclosed per
  CONTRIBUTING.md.
- **Fork-local branches are quarantined.** Branches like local CI/quality tooling
  (`build/…`, `chore/…` for Sonar, local reports) exist only on `origin` (this fork) and
  MUST NEVER be merged or rebased into an upstream-bound branch. If a fork-local improvement
  becomes upstreamable, it is re-authored onto a fresh branch off `upstream/main` as its own
  conventional commit.
- **Keep the export path rehearsed.** Periodically verify the current feature branch with
  `git format-patch upstream/main..<branch>` and a test application on clean
  `upstream/main`; a branch that cannot export cleanly is reworked before more work piles
  on top of it.

## Governance

This constitution is the binding standard for all work in this fork and supersedes ad-hoc
convention where they conflict. Upstream's own `CONTRIBUTING.md`, `DESIGN.md`, and review
decisions remain authoritative and win wherever they are stricter.

- **Authority.** Principles I–V are hard gates. The `Constitution Check` section of every
  plan MUST be evaluated against these principles; conflicts with a MUST are resolved by
  changing the spec, plan, tasks, or commit structure — never by diluting a principle.
- **Amendments.** Changes to this document require a dedicated commit (never mixed with
  feature work) stating the rationale, plus a version bump per the policy below. The Sync
  Impact Report at the top of this file records the change and is removed before the
  commit is finalized.
- **Versioning policy (SemVer for governance).** MAJOR = backward-incompatible governance
  or principle removal/redefinition; MINOR = a new principle/section or materially expanded
  guidance; PATCH = clarifications and non-semantic refinements.
- **Compliance review.** Every commit and PR MUST be checked against these principles
  before it leaves this fork. Added complexity or deviation MUST be justified in-PR (and,
  for plans, in Complexity Tracking). Unjustified violations block the contribution.

**Version**: 1.0.0 | **Ratified**: 2026-09-24 | **Last Amended**: 2026-09-24
