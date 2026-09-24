# Phase 0 Research: DeepSeek Harness & Non-Gemini Model Adaptation

**Date**: 2026-09-24 | **Plan**: [plan.md](./plan.md) | **Spec**: [spec.md](./spec.md)

All Technical Context unknowns and the spec's one `[NEEDS CLARIFICATION]` are resolved
below. Format: **Decision** / **Rationale** / **Alternatives considered**.

## R1. `dsh-tool-ask-user` in the preset *(resolves spec NEEDS CLARIFICATION)*

- **Decision**: Keep the shipped `standard` preset untouched in this feature. The
  `deepseek-harness` implementation runs DSH with `DSH_PERMISSION_MODE=danger-full-access`
  (approval `never`), so `dsh-tool-ask-user` can never block; questions surface in output
  and are not answered. Document this as known behavior in `docs/deepseek-harness.md`.
- **Rationale**: Preset composition is precisely the Phase 4 scope this feature excludes
  ("Dynamic Agentic Environment Curation"). Dropping the row would require generating a
  patch or preset artifact — codegen beyond FR-018's "minimal model-provider binding".
  With approval `never` the failure mode is cosmetic (an unanswered question in the
  transcript), never a hang. One-line escape hatch for later: a `--patch` row
  `{id: tool-ask-user, disabled: true}`.
- **Alternatives considered**:
  - *Drop the row via a static `--patch` in the harness* — rejected: second generated
    artifact, contradicts FR-018 and YAGNI until field evidence shows goal-degrading
    question loops.
  - *Ship a custom `headless` preset without the row* — rejected: preset shipping is
    Phase 4.
  - *Keep an interactive answerer* — rejected: no human exists inside a sandbox.

## R2. `DSH_HOME` location and durability

- **Decision**: `DSH_HOME=/ax/dsh` on the durable volume, outside `/workspace`. Session
  JSONL + SQLite state persist across suspend/resume. No retention policy in this feature
  (documented known limitation).
- **Rationale**: Parity with `bootstrapDataDir = /ax/antigravity` (Constitution II:
  identical semantics when moving Antigravity behind the interface); keeps the agent's
  own state off `/workspace`, which stays the agent's only file surface (SC-007).
- **Alternatives considered**: `~/.dsh` inside the container (rejected: not durable across
  suspend/resume); `/workspace/.dsh` (rejected: pollutes the agent's file surface and
  would be versioned with the workspace).

## R3. Provider registry shape

- **Decision**: `internal/model/provider.go` with
  `Provider interface { Name() string; Generate(ctx, *GenerateRequest) (*GenerateResponse, error) }`
  and `Register(name, factory func(Config) Provider)` called from provider `init()`;
  `NewClient` dispatches on `strings.ToLower(cfg.Provider)`. `google` is moved behind the
  interface with byte-identical request/response behavior.
- **Rationale**: Matches the seam sketched in the adaptation doc §4A and preserves
  Constitution II (extend seams, don't bypass). Registration by lowercase name gives
  `UpdateModel` validation its source of truth (FR-007).
- **Alternatives considered**: switch statement in `Generate` (rejected: registration
  makes validation and dispatch share one map; no second place to update); per-provider
  SDK dependencies (rejected: Constitution V — `generateGoogle` already uses `net/http`).

## R4. `openai` adapter (OpenAI-compatible chat completions)

- **Decision**: Single adapter POSTing `{baseURL}/chat/completions` with
  `Authorization: Bearer <key>`, body `{model, messages:[{role:"user", content:<prompt>}],
  <translated params>}`; response read from `choices[0].message.content`. Covers DeepSeek,
  OpenRouter, Together, Groq, Fireworks, Baseten, and OpenAI-compatible local servers
  (vLLM, Ollama, LM Studio, llama.cpp). `baseURL` is taken verbatim from `ModelSpec.base_url`
  (the manifest includes the `/v1` suffix, as in the doc's examples).
- **Rationale**: One adapter satisfies FR-002's entire list; the ecosystem converged on
  this shape. Stdlib only.
- **Alternatives considered**: one adapter per vendor (rejected: 8× maintenance for one
  wire format); OpenAI SDK dependency (rejected: Constitution V).

## R5. Parameter translation at the adapter boundary

- **Decision**: `parameters` stays a free-form pass-through map (FR-005). Adapters
  translate a small known set of Gemini spellings and pass everything else through
  verbatim. Initial translation tables:
  - *openai*: `maxOutputTokens`/`maxTokens` → `max_tokens`, `topP` → `top_p`,
    `topK` → `top_k`, `stopSequences` → `stop`, `candidateCount` → `n`,
    `temperature` → `temperature`; unknown keys pass through unchanged.
  - *anthropic*: `maxOutputTokens`/`maxTokens` → `max_tokens` (required by the API;
    default `4096` when absent), `topP` → `top_p`, `topK` → `top_k`,
    `stopSequences` → `stop_sequences`; unknown keys pass through unchanged.
  - *google*: unchanged — `generationConfig` verbatim.
- **Rationale**: Existing manifests keep working against new providers; power users can
  spell native parameter names directly. Translation errors surface as typed provider
  errors (spec Assumptions), never silent drops of behavior.
- **Alternatives considered**: per-provider parameter schema validation (rejected:
  spec Assumptions explicitly keep `parameters` schema-free); dropping unknown keys
  (rejected: silent behavior change).

## R6. Error taxonomy — never fabricate a completion

- **Decision**: `*ProviderError{Provider string, StatusCode int, Body string, Err error}`
  implementing `error` and `Unwrap()`. Returned for: empty/missing key, HTTP 4xx/5xx
  (status + body carried), transport failures, malformed responses. `fallbackResponse`
  is removed from all non-test paths; it survives only behind the existing explicit test
  opt-in (`DisableRemote`), which tests must pass deliberately.
- **Rationale**: FR-006/SC-002; the current behavior makes a wrong key indistinguishable
  from success (doc §2.1). Typed, wrapped errors satisfy Constitution II (`%w`, actionable
  context) and V (fail loudly).
- **Alternatives considered**: deleting `fallbackResponse` outright (rejected: the
  `DisableRemote` test seam depends on deterministic offline responses; gated is safer
  and smaller); returning raw `fmt.Errorf` with status text (rejected: callers and tests
  need the status/body structured).

## R7. Apply-time provider validation

- **Decision**: `UpdateModel` in `internal/server` validates `spec.provider` against the
  registry (lowercased) and rejects unknown values with the sorted list of valid names;
  empty/`google` remain valid defaults. Failure is an apply-time error (exit non-zero via
  the CLI), before any task consumes the `Model`.
- **Rationale**: FR-007/SC-003; the server is the single write path for resources (doc
  §2.2 shows it currently validates nothing). Validation lives beside the registry it
  checks — one source of truth.
- **Alternatives considered**: admission-style webhook (rejected: no webhook machinery in
  the repo, YAGNI); warning-only logging (rejected: fail-closed is the spec's explicit
  requirement).

## R8. `base_url` field: number, spelling, and JSON name

- **Decision**: `string base_url = 8 [json_name = "baseURL"];` in `ModelSpec`. Manifest
  spelling is `baseURL`; protojson additionally accepts `base_url` on input. Verify the
  generated `json_name` in `types_test.go` round-trip tests after regeneration.
- **Rationale**: FR-004 mandates the `baseURL` spelling and field number 8 is the next
  free slot (`reserved 3–5` untouched). protoc's default camel-casing of `base_url` is
  `baseUrl` (no initialism handling), so the explicit `json_name` is what guarantees the
  documented spelling. Additive-only, per FR-021.
- **Alternatives considered**: accepting `baseUrl` as the spelling (rejected: `baseURL`
  matches the manifest examples in the adaptation doc and ecosystem convention);
  carrying the endpoint in `parameters` only (rejected: the doc's proto-free escape hatch
  is superseded by this feature owning the schema change).

## R9. Credential and endpoint environment contract

- **Decision**: The controller injects, for a task bound to a workspace with a resolved
  model reference: (1) the secret value under the env name `Model.spec.secretKey.key`
  declares (e.g. `DEEPSEEK_API_KEY`), (2) `AX_MODEL_BASE_URL` carrying `spec.base_url`
  (empty string when unset). Legacy path (no model reference): `gemini-api-secret` →
  `GEMINI_API_KEY`, exactly as today (FR-009). Full contract in
  [contracts/container-env-contract.md](./contracts/container-env-contract.md).
- **Rationale**: FR-008; the DSH provider profile declares its own `apiKeyEnv` name, so
  injecting under the `Model`-declared name is what makes DSH's credential resolution
  ("from the inherited environment") work with zero DSH-side configuration. The `AX_`
  prefix marks the endpoint var as platform contract (docs/runner.md, FR-011).
- **Alternatives considered**: always inject as `GEMINI_API_KEY` regardless (rejected:
  keeps the Gemini literal alive; FR-008); `<KEY_NAME>_BASE_URL` convention (rejected:
  fragile name arithmetic; one platform-prefixed variable is simpler).

## R10. `AX_MODEL_YAML` and the metadata route

- **Decision**: Inject `AX_MODEL_YAML` (serialized bound `Model` resource) alongside
  `AX_TASK_YAML`/`AX_WORKSPACES_YAML`; serve it at `/metadata/v1alpha1/ax/model` on the
  metadata server alongside `/task` and `/workspaces`. When no model is bound, the env
  var is absent and the route returns an empty/404 response consistent with the existing
  routes' unbound behavior.
- **Rationale**: FR-010; harnesses need full model configuration (provider, model id,
  baseURL, parameter spellings) to generate `settings.yaml` at setup time (R12), and the
  env-var-only channel cannot carry structured data. Same shape as the two existing
  channels (Constitution IV).
- **Alternatives considered**: only env vars (rejected: cannot express `models[]`
  structure); a new gRPC read (rejected: the metadata server is the in-sandbox channel).

## R11. Harness dispatcher and resolution rules

- **Decision**: `internal/workspace/harness.go` defines
  `Harness interface { Kind() string; Setup(ctx, *v1alpha1.AgentHarness, goal, workspacePath string) error }`
  and a resolver: `kind` unset/`""` → `antigravity` (today's behavior, byte-compatible);
  `kind: "antigravity"` → same implementation explicitly; `kind: "deepseek-harness"` →
  DSH implementation; anything else → `SetupWorkspace` returns a typed error and the
  workspace reports not-`Ready` with condition reason `UnsupportedHarness` (naming the
  unknown kind); setup is never silently skipped (FR-013). The Antigravity implementation
  moves behind the interface with identical semantics: same script path
  (`/usr/local/bin/antigravity_bootstrap.py`), same env var (`GEMINI_API_KEY`), same data
  dir (`/ax/antigravity`) (FR-014).
- **Rationale**: FR-012–FR-014; a map of kind → `Harness` mirrors the provider registry
  pattern (R3) so both sides of the platform resolve implementations the same way.
  Conditions, not ad-hoc status fields, carry the failure (Constitution IV).
- **Alternatives considered**: per-image entrypoint switching without code (rejected: the
  goal→prompt contract and `readyz` semantics need in-process supervision, FR-016);
  dispatch keyed on image name (rejected: implicit magic, not declarative).

## R12. DSH invocation, profile, and model-provider binding

- **Decision**: The `deepseek-harness` implementation:
  1. writes `$DSH_HOME/settings.yaml` with the minimal provider binding generated from
     the bound `Model` (doc §5.5): a `llm-pi-ai.providers` entry named after
     `Model.metadata.name` with `apiKeyEnv` = `spec.secretKey.key`, `baseURL` =
     `spec.base_url`, `api` mapped from `ModelSpec.provider` (`openai` →
     `openai-completions`, `anthropic` → `anthropic-messages`, `google` →
     `openai-completions` against Gemini's OpenAI-compatible endpoint with base
     `https://generativelanguage.googleapis.com/v1beta/openai/`), and `models[].id` =
     `spec.model`; **plus** an `agent-default-model` section (`provider` = the route,
     `model` = `spec.model`) that selects it;
  2. runs `dsh --profile headless "<goal>"` as the one-shot headless session
     (goal → prompt contract identical to Antigravity, FR-016);
  3. sets `DSH_HOME=/ax/dsh` (R2), `DSH_PERMISSION_MODE=danger-full-access` (R14), and
     inherits the container environment carrying the Model credential (R9).
  **Finding (validation, `@deepseek-ai/dsh` 0.1.5-rc.2)**: the `agent-default-model`
  section is not optional. `dsh-base` mounts `dsh-llm-deepseek` as the default and
  `dsh-llm-pi-ai` dormant; the first implementation wrote only the provider section, so
  the agent asked for the built-in `deepseek-official` route and DSH failed with
  `MISSING_CREDENTIAL` for its own route. With both sections, DSH resolves the request
  against `spec.baseURL` with the credential named by `apiKeyEnv` (verified locally
  against a stub endpoint: DSH POSTs to the configured baseURL, not to its default).
  The shipped `headless` profile runs as-is (R13). No `--patch`, no `agent-presets/`, no
  MCP/skills codegen — only the `settings.yaml` binding (FR-018). `harness.command`, when
  set, replaces argv after `dsh`; `harness.env` is merged into the process environment;
  `harness.systemInstructions` is written to `$DSH_HOME/AGENTS.md`-style override only in
  a later phase — in this feature it is passed via the prompt prefix *(documented
  limitation, see R13)*.

  **Clarification on `systemInstructions` (FR-012)**: the field is accepted and validated
  in the proto from the first commit, but the `deepseek-harness` implementation prepends
  it to the goal prompt and the `antigravity` implementation ignores it (documented),
  keeping FR-018's codegen scope minimal.

- **Rationale**: `llm-pi-ai` is dormant until `settings.yaml` names providers (doc §5.5),
  so the entire "free and additional models" runtime path is configuration, not code.
  Prompt-prefix keeps `systemInstructions` functional without generating project files
  (Phase 4 territory).
- **Alternatives considered**: `--patch`-based provider rows (rejected: `settings.yaml`
  is the documented channel for providers; patches are composition-layer territory);
  generating `runtime.patch.yml` (rejected: Phase 4); requiring a credential-less
  `apiKeyEnv` for local endpoints (kept: DSH resolves any non-empty value; local models
  use a placeholder name like `LOCAL_API_KEY`).

## R13. DSH image strategy

- **Decision**: New `Dockerfile.task-runner-dsh` (node:22-slim, `@deepseek-ai/dsh`
  pinned through an `ARG DSH_VERSION`, apt deps, `ENV DSH_HOME=/ax/dsh`, same
  `ax-task-runner` entrypoint). `Dockerfile.task-runner` is untouched.
  `harness.image` on the workspace selects the runner image per task (FR-012);
  without it, the existing Python/Antigravity image is used.
- **Profile correction (implementation finding)**: the plan originally pre-baked a
  custom `ax-headless` profile via `--from-default-profile`, but that is a *launcher*
  flag: it creates the profile **and boots it**, so it cannot run during an image
  build. DSH's shipped `headless` profile already auto-initializes on first use from
  shipped templates, and the bundles resolve from the local installation first, so no
  registry access is involved. AX therefore runs the shipped `headless` profile and
  adds only `$DSH_HOME/settings.yaml` for the provider binding. That satisfies SC-008
  (first boot needs no npm egress) without a build-time DSH launch.
- **Rationale**: FR-017/SC-009 and the doc §8 image-growth risk; Antigravity users see
  zero change; using the shipped profile removes a moving part (a hand-maintained
  profile directory in the repo) that could drift from the pinned DSH version.
- **Alternatives considered**: one combined image (rejected: grows the Antigravity image,
  violates SC-009 spirit); runtime `dsh plugin` install (rejected: needs npm egress,
  adds minutes of setup latency); committing a generated `dsh-profile/` directory
  (rejected: drifts from the pinned DSH version, and `--from-default-profile` cannot be
  run headlessly to regenerate it).

## R14. Single isolation boundary

- **Decision**: The DSH harness always sets `DSH_PERMISSION_MODE=danger-full-access`
  (set by the implementation, never defaulted to the user). Agent Substrate remains the
  only isolation boundary; a test asserts the agent cannot write outside `/workspace`.
- **Rationale**: doc §5.6 — one variable disables DSH's fs/bash sandbox *and* switches
  approval to `never`, with the tool catalog unchanged (prompt-cache stable). It is the
  deliberate AX choice; the effective DSH default (`workspace-write`) would leave the
  agent blocked on approval prompts nobody answers.
- **Alternatives considered**: `workspace-write` + patch-disabling approval rows
  (rejected: forks the host composition); disabling sandbox plugin rows by `id`
  (rejected: changes composition, maintenance burden).

## R15. Egress

- **Decision**: No code change. Document in `docs/deepseek-harness.md` and examples that
  hardened deployments must allowlist the chosen model endpoint (host:port) on the task's
  `Gateway`, with the default gateway (`*:443`) keeping out-of-the-box behavior working.
  The Phase 0 and quickstart recipes include a sample `Gateway` for
  `api.deepseek.com:443` and in-cluster vLLM `:8000`.
- **Rationale**: Seam D is configuration, not code; `Gateway` egress allowlists already
  exist (doc §2.5). A blocked provider masquerades as a hung agent, so documentation is
  part of the deliverable's correctness.
- **Alternatives considered**: automatic egress injection from `Model.base_url`
  (rejected: crosses the Gateway trust boundary; deny-by-default stays explicit).

## R16. Commit slicing and upstream export

- **Decision**: Five slice groups, each independently green: (1) `feat(model):` registry +
  adapters + typed errors + tests; (2) `feat(api):` `ModelSpec.base_url` and
  `feat(api):` `AgentHarness` (each `.proto` + regen + tests, isolated); (3)
  `feat(controller):` Model-derived credentials + `AX_MODEL_YAML` + metadata route +
  `docs/runner.md`; (4) `feat(workspace):` harness dispatcher + antigravity move +
  `feat(workspace):` deepseek-harness + image + tests; (5) `docs:` per home
  (`DESIGN.md`, `docs/deepseek-harness.md`, phase-0 recipe). Upstream PRs re-authored per
  slice onto `feat/…` branches cut from `upstream/main`; each must pass
  `git format-patch upstream/main..<branch>` + clean apply + `make test` before
  submission (Constitution I).
- **Rationale**: One logical change per commit; the two proto changes ride alone so a
  reviewer can judge API evolution separately from behavior (Security § "v1alpha1
  breaking changes are coordinated" and Constitution I atomicity).
- **Alternatives considered**: one `feat:` mega-commit per seam (rejected: harder to
  cherry-pick, harder to review); single PR upstream (rejected: Constitution I — small
  reviewable units).

## R17. Version skew with the strict client-side YAML parser

- **Decision**: Accept and document the fail-closed behavior: an old `ax` CLI rejects
  manifests containing `baseURL` or `harness` at parse time ("unknown fields are
  errors", `types.go`), while old *servers* round-trip the unknown proto fields safely.
  The docs (Phase 5) state the compatibility matrix: old CLI + new manifest = client-side
  parse error; new CLI + old server = accepted, fields ignored by old server behavior;
  new CLI + new server = full feature. Round-trip tests with unknown fields cover
  SC-006.
- **Rationale**: FR-021/SC-006; changing the decoder to silently ignore unknown fields
  would weaken typo rejection (a tested behavior, `types_test.go:128`) for everyone —
  strictly worse than a clear, early parse error.
- **Alternatives considered**: permissive decode mode behind a flag (rejected: YAGNI and
  it undermines a deliberate safety property); a parallel `ax.v1alpha2` package
  (rejected: overkill absent external stability consumers — prior versioning analysis).

## R18. OpenAI-compatible `google` mapping for the DSH path

- **Decision**: When a `deepseek-harness` harness is bound to a `Model` with
  `provider: google`, generate the `settings.yaml` binding as `api: openai-completions`
  against Gemini's OpenAI-compatible endpoint (R12). The control-plane `google` adapter
  keeps its native `generateContent` path unchanged.
- **Rationale**: `dsh-llm-pi-ai` speaks only `openai-completions` / `openai-responses` /
  `anthropic-messages` (doc §3); Gemini's OpenAI-compat surface keeps one binding shape
  for the harness path while the platform's native path stays byte-compatible (SC-005).
- **Alternatives considered**: fail closed for `google` + DSH (rejected: arbitrary
  restriction once a compat surface exists); a native Gemini DSH adapter (rejected: new
  DSH-side work, out of this repo's control).

## Resolved assumptions (carried from spec)

- DSH surfaces observed in `@deepseek-ai/dsh` 0.1.5-rc.2 remain valid; an upstream DSH
  change to `--profile`, `--patch`, `apiKeyEnv`, or `DSH_PERMISSION_MODE` is a
  re-planning trigger (R12/R14 pin the exact surfaces used).
- `DSH_HOME` retention/size policy: out of scope, documented limitation (R2).
- `TaskStatus.UsageStats`: remains unpopulated (roadmap).
- No `NEEDS CLARIFICATION` remains open.
