# Implementation Plan: DeepSeek Harness & Non-Gemini Model Adaptation

**Branch**: `001-dsh-harness-model-adaptation` | **Date**: 2026-09-24 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-dsh-harness-model-adaptation/spec.md`

## Summary

Primary requirement: run agents under DeepSeek Harness (DSH) alongside Antigravity and
drive them with free, self-hosted, or additional (non-Gemini) model providers — covering
Phases 0–3 and 5 of `docs/deepseek-harness-adaptation.md`, with Phase 4 (manifest-driven
injection codegen) explicitly excluded.

Technical approach (decisions in [research.md](./research.md)): make two seams explicit
rather than re-architecting anything.

- **Seam A — provider registry (control plane).** `internal/model` becomes a provider
  registry (`google` unchanged, `openai` OpenAI-compatible adapter, `anthropic` Messages
  API adapter) with `ModelSpec.base_url = 8` (JSON/YAML `baseURL`), per-provider
  parameter translation at the adapter boundary, typed errors instead of the fabricated
  `fallbackResponse`, and apply-time provider validation in `UpdateModel`.
- **Seam B — agent harness (runner/workspace).** `AgentHarness` proto message bound to
  `WorkspaceSpec.harness = 4`; a harness dispatcher in `internal/workspace` with
  `antigravity` (current semantics, moved behind the interface) and `deepseek-harness`
  (DSH one-shot headless, `DSH_HOME=/ax/dsh`, `DSH_PERMISSION_MODE=danger-full-access`,
  minimal `settings.yaml` provider binding generated from the bound `Model`) as peers.
  Unset kind → Antigravity (default preserved); unknown kind → not-`Ready` condition.
- **Seam C — Model-derived credentials (controller/metadata).** `resolveModelCredential`
  replaces the `gemini-api-secret` literal; the secret is injected under the key name the
  `Model` declares plus `AX_MODEL_BASE_URL`; `AX_MODEL_YAML` joins the `AX_*_YAML` family
  and `/metadata/v1alpha1/ax/model` joins the metadata server.
- **Phase 0 (zero code)** ships as a validated manifest recipe; **Phase 5** makes
  `DESIGN.md`, `docs/runner.md`, and a new DSH guide tell the truth.

**Commit slicing (Constitution I).** Each seam lands as its own sequence of atomic,
Conventional Commits: (1) `feat(model): provider registry + openai/anthropic adapters`,
(2) `feat(api): ModelSpec.base_url + AgentHarness` (`.proto` + regenerated
`pkg/apis/v1alpha1` in the same commit, per Constitution II — two isolated commits, one
per additive change), (3) `feat(controller): Model-derived credential injection`,
(4) `feat(workspace): harness dispatcher + deepseek-harness`, (5) `docs: …` per touched
home. Fork-local artifacts stay out of every one of them; upstream PRs are re-authored
per seam onto fresh `feat/…` branches cut from `upstream/main` and pass the
`git format-patch` cherry-pick test before submission.

## Technical Context

**Language/Version**: Go 1.27 (control plane: `ax`, `ax-server`, `ax-controller`,
`ax-task-runner`); Python 3 + `google-antigravity` (existing task-runner image, left
untouched); Node 22 + `@deepseek-ai/dsh` (observed `0.1.5-rc.2`) for the new DSH image.

**Primary Dependencies**: Unchanged Go runtime set — go-redis, grpc, protobuf, yaml.v3,
agent-substrate. The `openai`/`anthropic` adapters use stdlib `net/http` exactly like
`generateGoogle` (no provider SDKs). DSH is an image-build dependency, pinned at build
time (research.md R12).

**Storage**: Redis (hashes, streams, pub/sub) behind the `store.Store` seam; in-memory
store for tests. Durable non-workspace volume `/ax`: `/ax/antigravity` (today) and
`/ax/dsh` (new, DSH session state; no retention policy in scope — known limitation).

**Testing**: `go test ./...` (`make test`), `make coverage`; table-driven, colocated
`*_test.go`; mock Substrate gRPC server + in-memory store (hermetic, no network);
`net/http/httptest` for provider adapters; round-trip tests with unknown proto fields
for version skew. `gofmt -l .`, `go vet ./...`, `go mod tidy` clean are hard gates.

**Target Platform**: Linux containers on Kubernetes behind Agent Substrate
(`linux/amd64` images); gRPC + `/healthz` control plane; no host changes.

**Project Type**: Go monorepo — thin `cmd/*` entrypoints, `internal/*` implementation,
`pkg/apis/v1alpha1` public API surface, `runner/` embeddable package, container images
(`Dockerfile.task-runner` unchanged, `Dockerfile.task-runner-dsh` new).

**Performance Goals**: zero new per-task Redis round trips on the hot path (Model read
happens at reconcile time only); each generation is one HTTP round trip with bounded,
cancellable I/O; DSH workspace setup stays within the Antigravity bootstrap order of
magnitude — first boot needs no npm registry access (SC-008).

**Constraints**: additive-only `ax.v1alpha1` proto (FR-021; `reserved` discipline kept);
zero new Go dependencies (Constitution V); secrets only as Kubernetes secret references,
never in tree (Security §); deny-by-default egress via `Gateway` allowlists; Apache 2.0
license headers on every source file; fail-closed semantics everywhere (unknown provider
rejected at `ax apply`, unknown harness kind → not-`Ready`, no fabricated completions).

**Scale/Scope**: 2 additive proto fields/messages (`ModelSpec.base_url`,
`AgentHarness` + `WorkspaceSpec.harness`), 3 provider entries (google/openai/anthropic),
2 harness kinds (antigravity/deepseek-harness), 1 new image, 5 Go subsystems touched
(`internal/model`, `internal/server`, `internal/controller`, `internal/metadata`,
`internal/workspace`). The platform itself keeps targeting billions of short-lived tasks
per cluster.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Gate | Requirement | Plan evidence | Status |
|---|---|---|---|
| I.1 | One feature per branch; branches re-authored per seam for upstream | Fork-local development on `001-…`; each seam is exported later as its own `feat/…` branch off `upstream/main` (Summary: Commit slicing) | PASS |
| I.2 | Atomic, independently meaningful commits | Slicing above splits registry / proto / controller / harness / docs; proto+regen inseparable per II | PASS |
| I.3 | Conventional Commits matching upstream history | Slicing uses `feat(model):`, `feat(api):`, `feat(controller):`, `feat(workspace):`, `docs:` | PASS |
| I.4 | Fork-local artifacts never in upstream-bound commits | No sonar/reports/tooling in seam commits; spec-kit and quality tooling already quarantined as `Fork-Local: true` | PASS |
| I.5 | Cherry-pickable export verified | `git format-patch upstream/main..<seam-branch>` rehearsal is a task-level gate (quickstart.md §6) | PASS |
| II.1 | Layers clean: `cmd/*` thin, logic in `internal/*`, `runner/` embeddable | New code lands in `internal/model`, `internal/workspace`, `internal/controller`, `internal/metadata`; `cmd/ax-task-runner` untouched | PASS |
| II.2 | Extension seams preserved (`store.Store`, substrate, runner embeddability) | Provider registry extends `internal/model`; harness dispatcher extends `internal/workspace`; no seam bypassed | PASS |
| II.3 | API conventions (`ax.v1alpha1`, `<Method>Request/Response`, manifest shape) | Additive proto only; `UpdateModel` keeps its shape and gains validation; regeneration in the same commit | PASS |
| II.4 | Idiomatic, defensive Go; clean `gofmt`/`go vet`/`go mod tidy` | Verification gate per commit (quickstart.md §6) | PASS |
| II.5 | Apache 2.0 headers on every source file | Task-level checklist item | PASS |
| III.1 | Tests land with the change | Each seam ships table-driven tests with it (test matrix in quickstart.md) | PASS |
| III.2 | Deterministic and hermetic | `httptest` for adapters; mock Substrate + in-memory store; no wall-clock races | PASS |
| III.3 | Coverage never regresses on touched code | New files (`openai.go`, `anthropic.go`, `harness*.go`) cover success/error/edge; `make coverage` | PASS |
| III.4 | Suite is a merge gate | `make test` before every commit | PASS |
| III.5 | No weakened tests | Removing `fallbackResponse` tightens behavior; a regression test asserts typed errors (FR-006) | PASS |
| IV.1 | Verb vocabulary unchanged | No new CLI verbs; `ax apply` fails loudly on unknown providers | PASS |
| IV.2 | Status through phases and conditions | Unknown harness kind → `Ready`/`WorkspaceReady` condition reason `UnsupportedHarness`, not a new status field | PASS |
| IV.3 | Docs updated in the same commit | `docs/runner.md` (FR-011) with Seam C; `docs/manifests.md` fixed/verified with adapters (FR-003); `DESIGN.md` (FR-022) and `docs/deepseek-harness.md` (FR-023) with their behavior commits (Phase 5) | PASS |
| IV.4 | Predictable, scriptable output | Typed errors → stderr + non-zero exit; apply-time rejection message lists valid providers | PASS |
| V.1 | YAGNI; smallest design within the four primitives | No new primitives; `AgentHarness` is a `WorkspaceSpec` sub-message; no cross-cutting abstractions beyond the two planned seams | PASS |
| V.2 | Zero new dependencies | Stdlib `net/http` adapters; no provider SDKs; DSH pinned at image build only | PASS |
| V.3 | Hot path discipline | No per-task Redis round trips added; reconcile-time Model read; idempotent reconciliation preserved | PASS |
| V.4 | Bounded, cancellable I/O | Provider HTTP calls take `context.Context` + timeouts; `dsh` runs as the supervised child `spec.command` | PASS |
| V.5 | Fail loudly and observably | Typed provider errors; condition reason `UnsupportedHarness`; structured logs on credential resolution failures | PASS |
| Sec.1 | Sandbox isolation inviolable | `DSH_PERMISSION_MODE=danger-full-access` keeps Substrate as the single boundary; asserted by test (SC-007) | PASS |
| Sec.2 | Secrets never touch the tree | `resolveModelCredential` reads Kubernetes secrets at runtime; examples use placeholders | PASS |
| Sec.3 | Supply-chain hygiene | No workflow changes required; `Dockerfile.task-runner-dsh` pins `@deepseek-ai/dsh` at a fixed version | PASS |
| Sec.4 | `v1alpha1` changes coordinated | Additive-only; the strict client-side YAML parser rejection of new spellings is documented fail-closed skew (research.md R17) | PASS |
| Sec.5 | Apache 2.0 / CLA | Original work under the repo header convention; AI assistance disclosed per CONTRIBUTING.md | PASS |

**Verdict (pre-design)**: PASS — no violations to justify in Complexity Tracking.

**Verdict (post-Phase-1 re-check)**: PASS — design artifacts introduce no new
dependencies, no new primitives, no layering deviations. The only added surface is the
second image (`Dockerfile.task-runner-dsh`), which serves FR-017/SC-009 (Antigravity
image untouched) and is scoped, pinned, and documented. Still no Complexity Tracking
entries.

## Project Structure

### Documentation (this feature)

```text
specs/001-dsh-harness-model-adaptation/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
│   ├── proto-delta.md
│   ├── provider-interface.md
│   ├── harness-interface.md
│   └── container-env-contract.md
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
pkg/apis/v1alpha1/
├── ax.proto                    # + ModelSpec.base_url = 8 (json_name "baseURL")
│                               # + message AgentHarness; WorkspaceSpec.harness = 4
├── *.pb.go                     # regenerated in the same commit as ax.proto
└── types.go                    # strict protojson decode (version-skew fail-closed)

internal/model/
├── client.go                   # Config/ConfigFromSpec (+BaseURL), registry dispatch in Generate
├── provider.go                 # NEW: Provider interface, registry, request/response, typed errors
├── google.go                   # generateGoogle moved unchanged behind the interface
├── openai.go                   # NEW: OpenAI-compatible /chat/completions adapter + parameter map
└── anthropic.go                # NEW: Messages API adapter

internal/server/
└── server.go                   # UpdateModel: validate provider against the registry (fail closed)

internal/controller/
└── reconciler.go               # resolveModelCredential (Model-derived), env + AX_MODEL_YAML injection

internal/metadata/
└── server.go                   # + /metadata/v1alpha1/ax/model

internal/workspace/
├── setup.go                    # SetupWorkspace dispatches through the harness interface
├── harness.go                  # NEW: Harness interface, kind resolution (fail closed)
├── harness_antigravity.go      # NEW: current bootstrap semantics behind the interface
└── harness_deepseek.go         # NEW: dsh one-shot, settings.yaml binding, DSH env contract

Dockerfile.task-runner          # UNCHANGED (SC-009)
Dockerfile.task-runner-dsh      # NEW: node:22-slim + @deepseek-ai/dsh + pre-baked profile (FR-019)

docs/
├── deepseek-harness.md         # NEW: DSH runner guide — build, profile, env contract, isolation (FR-023)
├── runner.md                   # generalized credential variables (FR-011)
├── manifests.md                # anthropic/baseURL examples verified working
└── dsh-phase-0.md              # NEW: zero-code recipe + double-bootstrap caveat (FR-020)

DESIGN.md                       # harness contract row, Model data-path, registry note, isolation invariant (FR-022)
examples/
├── model-deepseek.yaml         # NEW: openai provider + baseURL example (placeholders)
├── workspace-dsh.yaml          # NEW: harness.kind: deepseek-harness example
└── task-dsh-demo.yaml          # NEW: Phase 0 manifest
```

**Structure Decision**: single Go monorepo (default). Everything follows the established
layout: thin `cmd/*`, implementation in `internal/*`, public API in `pkg/apis/v1alpha1`,
embeddable `runner/` untouched, images as root-level Dockerfiles. Tests are colocated
`*_test.go` per the repository convention (no separate `tests/` tree).

## Complexity Tracking

> No violations. The Constitution Check passes without justifications; nothing is
> tracked here. (If implementation surfaces a deviation, it must be recorded here and
> justified in-PR per the constitution's Compliance review.)
