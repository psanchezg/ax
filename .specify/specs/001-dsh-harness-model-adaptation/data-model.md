# Data Model: DeepSeek Harness & Non-Gemini Model Adaptation

**Date**: 2026-09-24 | **Plan**: [plan.md](./plan.md) | **Contracts**: [contracts/](./contracts/)

## Entities

### Model (existing resource, extended)

Persisted in the store; consumed by `internal/server` (validation), `internal/model`
(generation), `internal/controller` (credential resolution), and in-sandbox harnesses
(runtime discovery via `AX_MODEL_YAML` / metadata route).

| Field | Type | Notes |
|---|---|---|
| `metadata.name` | string | Resource name; also names the DSH provider entry (R12). |
| `metadata.atespace` | string | Scoping, unchanged. |
| `spec.provider` | string | Registry key, lowercase (`google`, `openai`, `anthropic`); empty ≡ `google`. Validated at apply time (FR-007). |
| `spec.model` | string | Model id, free-form (e.g. `deepseek-chat`). |
| `spec.baseURL` | string (new) | Endpoint base, scheme+host+port+optional `/v1` prefix, verbatim. `ModelSpec.base_url = 8`, `json_name = "baseURL"` (R8). Empty ≡ provider default endpoint. |
| `spec.secretKey` | `{name, key}` | Kubernetes secret reference; `key` is the env var name injected into task containers (FR-008). Absent/omitted ⇒ credential-less endpoint (local models). |
| `spec.parameters` | Struct (free-form) | Pass-through generation parameters; Gemini spellings translated per adapter (R5). No schema validation (spec Assumptions). |

**Validation rules**
- `provider` MUST be a registered provider name (case-insensitive) or empty → `google`;
  otherwise `UpdateModel` rejects with the valid-name list (SC-003).
- `baseURL`, when set, MUST parse as an `http(s)` URL; rejection at apply time with a
  typed error.
- `secretKey.key`, when set, MUST be a valid environment variable name (the container
  contract depends on it).
- Fields `reserved 3–5` in `ModelSpec` stay reserved (FR-021).

### AgentHarness (new message, bound to Workspace)

| Field | Type | Notes |
|---|---|---|
| `kind` | string | `antigravity` \| `deepseek-harness`; empty ≡ `antigravity`. Unknown → fail closed (FR-013). |
| `image` | string | Task-runner image override (e.g. the DSH image). Empty ≡ current image. |
| `command` | repeated string | Harness argv override; empty ≡ per-kind default. |
| `modelRef` | string | `Model` name whose credential/endpoint this harness uses. Empty ≡ legacy Gemini path (FR-009). |
| `env` | repeated `EnvVar` | Merged into the harness process environment (existing `EnvVar` message). |
| `systemInstructions` | string | Persona override. This feature: prompt prefix for `deepseek-harness`, ignored (documented) for `antigravity` (R12). |

Proto: `WorkspaceSpec.harness = 4` (next free after `skills = 3`); `AgentHarness` fields
numbered 1–6 as above. Full delta in [contracts/proto-delta.md](./contracts/proto-delta.md).

### Provider (internal, `internal/model`)

| Member | Type | Notes |
|---|---|---|
| `Name() | string` | Registry key, lowercase. |
| `Generate(ctx, *GenerateRequest) | (*GenerateResponse, error)` | One generation; returns `*ProviderError` on failure (R6). |

- **GenerateRequest**: `Model` (resolved config incl. `BaseURL`, `Parameters`), `Prompt`.
- **GenerateResponse**: `Text`, `Provider`, `Model` (echo for logging/telemetry).
- **Registry**: `Register(name, factory)` → map[string]factory; `Known()` feeds
  apply-time validation.

### Harness (internal, `internal/workspace`)

| Member | Type | Notes |
|---|---|---|
| `Kind() | string` | `antigravity` \| `deepseek-harness`. |
| `Setup(ctx, *AgentHarness, goal, workspacePath) | error` | Prepares the workspace and launches/configures the in-sandbox agent. |

Implementations: `antigravity` (moved `runBootstrap` semantics: script path, `GEMINI_API_KEY`, `/ax/antigravity` data dir — byte-compatible, SC-009) and `deepseek-harness` (R12/R14).

### ModelCredential (derived, controller → container)

| Component | Notes |
|---|---|
| `(secret value, key name)` | Resolved from `Model.spec.secretKey` at reconcile time; injected as one env var under `key name`. |
| `endpoint` | `spec.baseURL`, injected as `AX_MODEL_BASE_URL`. |
| `serialized source` | `AX_MODEL_YAML` env var and `/metadata/v1alpha1/ax/model` route (FR-010). |

Legacy shape (no model reference): fixed pair (`gemini-api-secret`, `GEMINI_API_KEY`) — unchanged (FR-009).

## Relationships

```text
Workspace ──spec.harness──▶ AgentHarness ──modelRef──▶ Model ──secretKey──▶ Kubernetes Secret
    │                            │
    │                            ├── kind: antigravity ──────▶ Harness#antigravity ──▶ /usr/local/bin/antigravity_bootstrap.py
    │                            └── kind: deepseek-harness ─▶ Harness#deepseek-harness ──▶ dsh --profile headless
    │                                                                 │
    └── spec.goal ────────────────────────────────────────────────────┼──▶ prompt
                                                                      └──▶ $DSH_HOME/settings.yaml ◀── Model (provider/baseURL/model/key name)

Model ──▶ Provider registry (google | openai | anthropic)   # control-plane generation path
```

## State transitions (workspace setup)

```text
SetupWorkspace(goal, harness)
  ├─ harness.kind == "" ─────────────▶ antigravity setup   (unchanged behavior)
  ├─ harness.kind == "antigravity" ──▶ antigravity setup   (explicit, same semantics)
  ├─ harness.kind == "deepseek-harness"
  │     ├─ modelRef resolves ────────▶ write settings.yaml, run dsh   → Ready
  │     ├─ modelRef unresolvable ────▶ typed error → not-Ready, condition reason
  │     │                              `UnresolvedModelRef` (never silent)
  │     └─ no modelRef ──────────────▶ run dsh with image-baked settings only
  │                                    (credential-less/local setup)  → Ready
  └─ unknown kind ──────────────────▶ NOT skipped; typed error → workspace not-Ready,
                                       condition reason `UnsupportedHarness` naming the kind
```

Conditions extend the existing `Ready` / `WorkspaceReady` model with documented reasons
(`UnsupportedHarness`, `UnresolvedModelRef`) — no new status fields (Constitution IV).

## Validation summary (apply/boot time)

| Rule | When | Failure |
|---|---|---|
| `Model.spec.provider` ∈ registry | `UpdateModel` (apply) | Reject, list valid providers (SC-003) |
| `Model.spec.baseURL` parseable URL | `UpdateModel` (apply) | Reject, typed error |
| `secretKey.key` valid env name | `UpdateModel` (apply) | Reject, typed error |
| `harness.kind` ∈ {antigravity, deepseek-harness} or empty | workspace setup (boot) | not-Ready, `UnsupportedHarness` |
| `harness.modelRef` resolvable to a `Model` | workspace setup (boot) | not-Ready, `UnresolvedModelRef` |
| Generation failures (empty key, 4xx/5xx, transport) | generation | `*ProviderError` — never fabricated (SC-002) |
