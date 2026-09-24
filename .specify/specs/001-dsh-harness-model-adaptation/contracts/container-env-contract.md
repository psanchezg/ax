# Contract: Task Container Environment & Metadata Service

**Date**: 2026-09-24 | **Producers**: `internal/controller/reconciler.go`,
`internal/metadata/server.go` | **Consumers**: task containers / runners / harnesses |
**Decisions**: research.md R9, R10 | **Docs home**: `docs/runner.md` (FR-011)

## Environment variables (controller → task container)

| Variable | When | Value | Status |
|---|---|---|---|
| `AX_TASK_YAML` | always | serialized `Task` resource | existing |
| `AX_WORKSPACES_YAML` | always | serialized bound `Workspace` list | existing |
| `AX_MODEL_YAML` | model bound | serialized `Model` resource (the one named by `harness.modelRef`, or the workspace's resolved model reference) | **new** (FR-010) |
| `AX_MODEL_BASE_URL` | model bound | `Model.spec.baseURL` (empty string when unset) | **new** (FR-008) |
| `<Model.spec.secretKey.key>` | model bound with `secretKey` | secret value from Kubernetes secret `<secretKey.name>/<secretKey.key>` | **new** (FR-008) |
| `GEMINI_API_KEY` | **no** model reference (legacy) | secret value from literal `gemini-api-secret` | existing, preserved byte-for-byte (FR-009) |
| `GEMINI_API_KEY` | model bound with `modelRef` to a `google` Model | same contract as any `<secretKey.key>` if declared as such | driven by the `Model`, not by the literal |
| `DSH_HOME`, `DSH_PERMISSION_MODE` | harness-side (`deepseek-harness`) | `/ax/dsh`, `danger-full-access` | set by the harness implementation ([harness-interface.md](./harness-interface.md)) |

Rules:

- **Resolution**: `resolveModelCredential(ctx, atespace, modelRef)` replaces
  `lookupGeminiKey`; it reads the `Model` from the store and resolves `spec.secretKey`
  against the Kubernetes secret. Failures are typed and surfaced (no silent fallback to
  the Gemini literal when a model reference exists).
- **Legacy path**: when the task/workspace carries no model reference, injection is
  exactly `gemini-api-secret` → `GEMINI_API_KEY`, unchanged (SC-005/SC-009).
- **Rotation**: a rotated secret value is picked up at next task provisioning with no
  code or manifest change (SC-004).
- **Secrecy**: values live only in the container environment at runtime — never in
  logs, docs, examples, or the store serialization (`AX_MODEL_YAML` carries the
  `secretKey` *reference*, not the secret value).

## Metadata service (guest → `internal/metadata`)

Base: existing metadata server (in-sandbox discovery channel).

| Route | Returns | Status |
|---|---|---|
| `GET /metadata/v1alpha1/ax/task` | serialized `Task` | existing |
| `GET /metadata/v1alpha1/ax/workspaces` | serialized bound `Workspace` list | existing |
| `GET /metadata/v1alpha1/ax/model` | serialized bound `Model` (same content as `AX_MODEL_YAML`) | **new** (FR-010) |

Rules:

- Unbound model: the route returns the same empty/unbound shape the existing routes use
  (404/empty body consistent with `/task` unbound behavior) and `AX_MODEL_YAML` is
  absent — consumers must handle both.
- The route serves configuration discovery only; it never serves secret values.

## Consumer obligations

- Runners/harnesses MAY discover their model binding from `AX_MODEL_YAML` **or** the
  metadata route; the two channels carry identical serialized content.
- `docs/runner.md` documents this table as the authoritative container contract
  (FR-011) — replacing its current hardcoded `GEMINI_API_KEY` statement.

## Test matrix (Constitution III)

- Reconciler: model bound → `<secretKey.key>` + `AX_MODEL_BASE_URL` + `AX_MODEL_YAML`
  injected with correct values; no model reference → legacy pair only; unresolvable
  secret → typed error, no partial injection.
- Metadata server: `/model` returns the bound resource; unbound → consistent empty
  behavior; never leaks secret values.
- Golden test: `AX_MODEL_YAML` content equals the store's `Model` serialization used by
  the metadata route (single source).
