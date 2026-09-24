# Feature Specification: DeepSeek Harness & Non-Gemini Model Adaptation

**Feature Branch**: `001-dsh-harness-model-adaptation`

**Created**: 2026-09-24

**Status**: Draft

**Input**: User description: "Prepare a specification covering all phases of
`docs/deepseek-harness-adaptation.md` except Phase 4 (MCP/skills/subagents injection
codegen, which stays on the roadmap): run agents under DeepSeek Harness (DSH) instead of
or alongside Antigravity, and drive them with free or additional model providers instead
of only Gemini."

> **Scope note.** This specification covers Phases 0, 1, 2, 3, and 5 of the adaptation
> plan. Phase 4 — manifest-driven injection codegen for MCP servers, skills
> materialization, `AGENTS.md`, `settings.yaml`, and `agent-presets/` — is explicitly
> **out of scope** and remains roadmap work ("Dynamic Agentic Environment Curation").
> The only DSH configuration generated in scope is the minimal model-provider binding the
> harness needs to function (§5.5 of the plan).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Any model provider works, failures are loud (Priority: P1)

An operator declares a `Model` resource pointing at a non-Gemini provider — DeepSeek's
API, OpenRouter, Together, Groq, or a self-hosted vLLM/Ollama/LM Studio endpoint — and
the platform accepts it at apply time and generates through it. When the credential or
endpoint is wrong, the platform fails with a clear, typed error; it never fabricates a
plausible-looking completion.

**Why this priority**: The stated goal is "free or additional model providers rather than
only Gemini", and this story also removes the silent `fallbackResponse` defect that today
makes a misconfigured key indistinguishable from success. It is the highest-value slice
and is independently useful even before any harness work (the workspace planner and any
future `Model` consumer benefit immediately).

**Independent Test**: Declare a `Model` with `provider: openai` and a local vLLM
`baseURL`, apply it, trigger a generation, and observe a real completion; repeat with a
corrupted API key and observe a typed error — never a fabricated plan. A `Model` with a
typo'd provider is rejected at `ax apply`.

**Acceptance Scenarios**:

1. **Given** a running `ax-server`, **When** the operator applies a `Model` with
   `provider: openai`, `model: deepseek-chat`, `baseURL: https://api.deepseek.com/v1`,
   and a valid `secretKey`, **Then** the resource is accepted and generations are issued
   against that endpoint with the secret resolved from the Kubernetes secret.
2. **Given** the same `Model`, **When** the operator applies it with an empty or wrong
   API key, **Then** the generation call returns a typed error carrying the provider's
   HTTP status and body; no fallback text is returned and the failure is visible in task
   conditions/logs.
3. **Given** a running `ax-server`, **When** the operator applies a `Model` with
   `provider: gemini-flash` (unknown), **Then** `ax apply` fails immediately with a
   message naming the valid provider values.
4. **Given** a `Model` with `provider: anthropic`, **When** a generation is requested,
   **Then** it is issued against the Anthropic Messages API — making the example already
   present in `docs/manifests.md` actually work.
5. **Given** an existing `Model` with `provider: google` (or empty) and Gemini-style
   `parameters`, **When** a generation is requested, **Then** behavior is byte-for-byte
   compatible with today (same request shape, same `generationConfig` pass-through).
6. **Given** a self-hosted endpoint declared with `baseURL`, **When** `parameters`
   contains Gemini-style spellings such as `maxOutputTokens`, **Then** the
   OpenAI-compatible adapter translates them (e.g. `max_tokens` /
   `max_completion_tokens`) at the adapter boundary.

---

### User Story 2 - DSH runs in a sandbox today, with zero AX changes (Priority: P2)

An operator runs a DSH-powered task against an **unmodified** AX deployment by supplying
a custom image and `spec.command`, validating the whole premise end to end before any
control-plane code lands.

**Why this priority**: This is the cheapest end-to-end validation of the DSH premise and
de-risks every later story. It is a manifest-only recipe, so it can be tested
independently of all code work and shipped as documentation value on day one.

**Independent Test**: Build the DSH task-runner image, apply the Phase 0 manifest from
the plan (Task with `image`, `command: ["dsh", "--profile", "headless", ...]`,
`debug: true`, no workspace `goal`), and observe the task reach `Running` with DSH
producing its final message on stdout.

**Acceptance Scenarios**:

1. **Given** an unmodified AX control plane and a published DSH runner image, **When**
   the operator applies the Phase 0 manifest, **Then** the task reaches `Running`, and
   `ax ssh <task> -- ...` (with `debug: true`) shows the DSH process and workspace state.
2. **Given** the Phase 0 manifest, **When** it omits the workspace `goal`, **Then** the
   Antigravity bootstrap does not run and only DSH executes inside the sandbox.
3. **Given** a workspace binding that sets a `goal` while `GEMINI_API_KEY` is present in
   the atespace, **When** the task boots, **Then** the double-bootstrap caveat is
   documented behavior for this story (fixed declaratively in Story 4).

---

### User Story 3 - Credentials come from `Model`, not from a literal (Priority: P3)

The controller resolves the model credential from the `Model` resource and injects it
into the task container under the name that `Model` declares, alongside the model
endpoint and a serialized `AX_MODEL_YAML`. Key rotation becomes one `ax apply`.

**Why this priority**: This removes the last Gemini-specific literal from the control
plane (`gemini-api-secret` / `GEMINI_API_KEY`) and delivers the key-rotation promise made
in `docs/concepts.md`. It is testable without any harness work and is a prerequisite for
Story 4's `modelRef` resolution.

**Independent Test**: Bind a task to a workspace whose model reference points at the
`deepseek` `Model`; inspect the container environment and the metadata server, and
observe the credential under `DEEPSEEK_API_KEY` (the name the `Model` declares), an
endpoint variable, and `/metadata/v1alpha1/ax/model` serving the model binding.

**Acceptance Scenarios**:

1. **Given** a `Model` with `secretKey: {name: deepseek-api-secret, key:
   DEEPSEEK_API_KEY}`, **When** a bound task is provisioned, **Then** the container
   environment contains the secret value under `DEEPSEEK_API_KEY` and a model-endpoint
   variable carrying `baseURL`.
2. **Given** a task with no model reference (legacy manifests), **When** it is
   provisioned, **Then** behavior is unchanged: `gemini-api-secret` / `GEMINI_API_KEY`
   are injected exactly as today.
3. **Given** a provisioned task, **When** the runner reads
   `/metadata/v1alpha1/ax/model`, **Then** it receives the serialized `Model` resource
   and can discover its configuration at runtime.
4. **Given** a rotated key in the referenced Kubernetes secret, **When** the task is
   re-provisioned, **Then** the new value is injected with no code or manifest change
   beyond the secret update.

---

### User Story 4 - Harness choice is declarative (Priority: P4)

An operator selects the in-sandbox harness per `Workspace` via a new `harness` block
(`kind`, `image`, `command`, `modelRef`, `env`, `systemInstructions`), with
`deepseek-harness` and `antigravity` as peers. Unset keeps Antigravity; an unknown kind
fails closed with a clear condition.

**Why this priority**: This is the headline "DSH instead of (or alongside) Antigravity"
deliverable and closes the roadmap item on customizable agent runtimes. It builds on
Stories 1–3 (providers, credentials) and is therefore prioritized after them, but it is
still an independently deployable slice with a working MVP once its dependencies exist.

**Independent Test**: Apply a `Workspace` with `harness.kind: deepseek-harness` and a
`modelRef` to the `deepseek` `Model`, apply a task binding it, and observe the runner
execute `dsh` one-shot headless against the goal with the model bound; apply the same
manifest without `harness` and observe identical Antigravity behavior to today.

**Acceptance Scenarios**:

1. **Given** a `Workspace` with `harness.kind: deepseek-harness` and `modelRef:
   deepseek`, **When** a task binding it with a `goal` boots, **Then** the runner invokes
   DSH one-shot headless with the goal as prompt and the bound `Model`'s
   endpoint/credential environment, and the workspace is prepared before the task command
   starts.
2. **Given** a `Workspace` with no `harness` block, **When** a task with a `goal` boots,
   **Then** the current Antigravity bootstrap runs exactly as today (default preserved).
3. **Given** a `Workspace` with `harness.kind: codex`, **When** a task boots, **Then**
   the workspace reports not-`Ready` with a condition naming the unknown kind — setup is
   never silently skipped.
4. **Given** the DSH harness, **When** it starts, **Then** `DSH_HOME` points at a
   durable non-workspace directory (e.g. `/ax/dsh`) so session state survives
   suspend/resume while `/workspace` remains the agent's only file surface.
5. **Given** the DSH harness, **When** it starts, **Then** it sets
   `DSH_PERMISSION_MODE=danger-full-access` so Agent Substrate remains the single
   isolation boundary, and a test asserts the agent cannot write outside `/workspace`.
6. **Given** `harness.image` set on the workspace, **When** the task is provisioned,
   **Then** the task runner image is overridden (e.g. the Node-based DSH image), leaving
   Antigravity users on the existing image unaffected.
7. **Given** the goal→prompt contract, **When** setup completes, **Then**
   `/readyz?check=workspace` semantics are unchanged from today.

---

### User Story 5 - The documentation tells the truth (Priority: P5)

`DESIGN.md` describes the agent-harness contract and where `Model` sits on the data path;
`docs/runner.md` documents the generalized credential variables; a guide explains how to
run DSH; the Phase 0 recipe is published.

**Why this priority**: The plan identified documentation drift as an active liability
(`docs/manifests.md` advertises a provider path that cannot work). Docs-only work is
independently reviewable and shippable, but its value is proportional to the code it
describes, so it is prioritized last even though Phase 0 documentation can ship earlier.

**Independent Test**: Review each documented claim against the implemented behavior —
every RPC, env var, and recipe in the docs can be exercised as written.

**Acceptance Scenarios**:

1. **Given** the merged implementation, **When** a reader opens `DESIGN.md`, **Then** it
   contains the fifth component row for the agent-harness contract, the `Model` data-path
   statement, the provider-registry note, and the single-isolation-boundary invariant.
2. **Given** the merged implementation, **When** a reader opens `docs/runner.md`, **Then**
   the container contract documents the generalized credential variables instead of a
   hardcoded `GEMINI_API_KEY`.
3. **Given** the Phase 0 recipe, **When** an operator follows it verbatim against an
   unmodified deployment, **Then** it works as written, including the double-bootstrap
   caveat.

---

### Edge Cases

- **Unknown provider at apply time**: rejected by `ax apply` with the valid provider
  list (fail closed, before any task consumes the `Model`).
- **Unknown harness kind at task boot**: workspace condition reports not-`Ready` with a
  clear reason; setup is never silently skipped.
- **Empty/missing API key**: typed error surfaced on the generation path — the silent
  `fallbackResponse` is never returned outside explicit test opt-in
  (`DisableRemote`-style gate).
- **Provider 4xx/5xx or network failure**: typed error carrying the provider status and
  body; no fabricated completion; visible in task conditions/logs.
- **Legacy manifests** (no `harness`, no `modelRef`): byte-for-byte current behavior —
  Antigravity bootstrap and `gemini-api-secret` injection.
- **Version skew — old `ax` CLI meets new fields**: the strict YAML decoder rejects
  manifests containing unknown fields (`baseURL`, `harness`) at parse time. This is
  accepted, documented fail-closed behavior; it is a client-side parse error, not a wire
  break (unknown proto fields round-trip safely through old binaries).
- **Phase 0 double bootstrap**: a workspace `goal` plus `GEMINI_API_KEY` in the atespace
  runs both harnesses; documented as a Phase 0 caveat and eliminated by Story 4's
  declarative dispatch.
- **Single isolation invariant**: with `DSH_PERMISSION_MODE=danger-full-access`, any tool
  DSH would have confined now relies entirely on Substrate — the boundary is asserted by
  a test (no writes outside `/workspace`).
- **DSH session state growth** on the durable volume (JSONL + SQLite): no retention
  policy in scope; documented as a known limitation.
- **No answerer for DSH user questions** (`dsh-tool-ask-user` in the standard preset):
  with approval `never` nothing blocks, but questions surface unanswered — preset
  composition decision deferred to plan (see Assumptions).

## Requirements *(mandatory)*

### Functional Requirements

**Provider registry (Phase 1)**

- **FR-001**: The model subsystem MUST expose a provider registry: providers register
  under a lowercase name and generations dispatch through a common provider interface;
  `google` remains a first-class entry with unchanged behavior.
- **FR-002**: The system MUST provide an `openai` provider adapter (OpenAI-compatible
  chat-completions) covering DeepSeek, OpenRouter, Together, Groq, Fireworks, Baseten,
  and OpenAI-compatible local servers (vLLM, Ollama, LM Studio, llama.cpp).
- **FR-003**: The system MUST provide an `anthropic` provider adapter (Messages API),
  making the `docs/manifests.md` example functional.
- **FR-004**: `ModelSpec` MUST gain a `base_url` field (proto field number 8, JSON/YAML
  spelling `baseURL`) copied into client configuration; self-hosted and gateway endpoints
  MUST be declarable without code changes.
- **FR-005**: The free-form `parameters` map MUST remain pass-through for Gemini and MUST
  be translated per provider at the adapter boundary for other providers (e.g.
  `maxOutputTokens` → `max_tokens` / `max_completion_tokens`).
- **FR-006**: The system MUST NEVER return a fabricated completion outside an explicit
  test-only opt-in: empty keys, HTTP 4xx/5xx, and transport failures MUST return typed
  errors carrying the provider's status and body.
- **FR-007**: `UpdateModel` MUST validate `provider` against the registry at apply time
  and reject unknown providers with the list of valid values.

**Model-derived credentials (Phase 2)**

- **FR-008**: The controller MUST resolve credentials from the referenced `Model`
  resource (`spec.secretKey`), replacing the hardcoded `gemini-api-secret` /
  `GEMINI_API_KEY` lookup, and inject the value under the key name the `Model` declares
  plus a model-endpoint variable.
- **FR-009**: When no model reference is present (legacy manifests), the controller MUST
  preserve today's literal Gemini secret behavior unchanged.
- **FR-010**: The controller MUST inject `AX_MODEL_YAML` alongside `AX_TASK_YAML` /
  `AX_WORKSPACES_YAML`, and the metadata server MUST serve the model binding at
  `/metadata/v1alpha1/ax/model`.
- **FR-011**: `docs/runner.md` MUST document the generalized credential variables as the
  container contract.

**Agent harness (Phase 3)**

- **FR-012**: `WorkspaceSpec` MUST gain a `harness` field (proto field number 4) with
  `kind`, `image`, `command`, `modelRef`, `env`, and `systemInstructions`
  (an `AgentHarness` message), added additively — no existing field numbers change.
- **FR-013**: Harness resolution MUST fail closed: unset `kind` runs the current
  Antigravity bootstrap unchanged; an unknown `kind` marks the workspace not-`Ready`
  with a clear condition and does not silently skip setup.
- **FR-014**: The runner MUST dispatch setup through a harness interface, with the
  Antigravity implementation moved behind it with identical semantics (script, env var,
  data dir).
- **FR-015**: A `deepseek-harness` implementation MUST run DSH one-shot headless with
  the workspace goal as prompt, keep `DSH_HOME` on a durable non-workspace directory,
  inject the bound `Model`'s credential/endpoint environment, and set
  `DSH_PERMISSION_MODE=danger-full-access` so Agent Substrate is the single isolation
  boundary.
- **FR-016**: The goal→prompt contract and `/readyz?check=workspace` semantics MUST be
  identical across harnesses.
- **FR-017**: The DSH runner MUST be shipped as a separate Node-based task-runner image
  (custom images embedding the runner package remain supported); the existing
  Python/Antigravity image MUST NOT change its contents or contract.
- **FR-018**: The `deepseek-harness` implementation MUST generate only the minimal DSH
  model-provider binding derived from the bound `Model` (plan §5.5). Manifest-driven
  codegen for MCP, skills, `AGENTS.md`, and `agent-presets/` is NOT part of this feature.
- **FR-019**: The DSH profile MUST be pre-provisioned at image build time so first boot
  requires no npm registry egress.

**Zero-code validation (Phase 0)**

- **FR-020**: The Phase 0 recipe (custom image + `spec.command` + `debug: true`, no
  workspace `goal`) MUST work against an unmodified AX deployment and MUST be documented
  with its double-bootstrap caveat.

**Compatibility & documentation (Phase 5 + cross-cutting)**

- **FR-021**: All proto changes MUST be additive within `ax.v1alpha1` (new fields only,
  `reserved` discipline maintained); old binaries MUST interoperate — unknown fields
  round-trip without loss and unset fields reproduce current behavior.
- **FR-022**: `DESIGN.md` MUST gain: the agent-harness contract row, the `Model`
  data-path statement, the provider-registry note, and the single-isolation-boundary
  invariant.
- **FR-023**: The DSH runner guide MUST document image build, profile pre-provisioning,
  environment contract, and the isolation decision.

### Key Entities

- **Model** (existing, extended): named model configuration — provider, model id,
  endpoint (`baseURL`, new), credential reference (Kubernetes secret), free-form
  generation parameters. Consumed by the control plane to resolve credentials and by
  in-sandbox harnesses at runtime.
- **AgentHarness** (new): declarative in-sandbox harness selection bound to a Workspace —
  kind, image override, command override, model reference, extra environment,
  system-instruction override.
- **Provider** (internal): registry entry translating generic generation requests to a
  specific model API (google, openai, anthropic) with per-provider parameter mapping and
  error normalization.
- **Harness** (internal, runner-side): setup contract that turns a workspace goal into a
  prepared workspace and a running agent; Antigravity and DeepSeek Harness are peer
  implementations.
- **ModelCredential** (derived): the resolved (secret value, key name, endpoint) triple
  injected into task containers; its serialized source is `AX_MODEL_YAML` and the
  metadata route.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator runs a DSH-driven agent against a free or self-hosted
  (non-Gemini) model end to end — from `ax apply` to a completed goal — with no AX code
  modified at run time.
- **SC-002**: Misconfiguration is always loud: 0 fabricated completions in tests covering
  empty keys, 4xx/5xx, and transport failures; 100% of these cases surface typed errors.
- **SC-003**: 100% of `Model` resources with unknown providers are rejected at
  `ax apply`, before any task consumes them.
- **SC-004**: Key rotation requires only a Kubernetes secret update and task
  re-provisioning — zero code or manifest changes (after Phase 2).
- **SC-005**: 100% backward compatibility for legacy manifests: all existing tests pass
  unchanged, and manifests without `harness`/`modelRef`/`baseURL` behave identically to
  the current release.
- **SC-006**: Interoperability across versions: old CLI + new server and new CLI + old
  server both operate without wire errors (additive-only proto; verified by
  round-trip tests with unknown fields).
- **SC-007**: Single isolation invariant holds: the sandboxed agent cannot write outside
  `/workspace`, asserted by an automated test.
- **SC-008**: First boot of a DSH task needs no npm registry access (profile
  pre-baked), keeping workspace setup latency within the current Antigravity bootstrap
  order of magnitude.
- **SC-009**: The Antigravity path is untouched in behavior: existing image contract and
  bootstrap tests pass without modification.

## Assumptions

- DSH behavior is taken as observed in `@deepseek-ai/dsh` 0.1.5-rc.2 (`--profile
  headless`, `--patch`, `apiKeyEnv` credential refs, `DSH_PERMISSION_MODE` threading);
  a DSH upgrade that changes these surfaces is a re-planning trigger.
- Implementation order follows the plan's phasing (0 → 1 → 2 → 3 → 5); story priorities
  reflect user value, and Stories 3–4 depend on Story 1's provider work.
- The `google` provider remains the default for `provider` empty or `"google"`; no
  migration of existing `Model` resources is required.
- `parameters` stays a free-form pass-through map; no schema validation of parameter
  names is added (translation errors surface as typed provider errors).
- The DSH harness is shipped as a separate `ax-task-runner-dsh`-style image rather than
  replacing `Dockerfile.task-runner`, keeping image growth away from Antigravity users.
- Pre-provisioned DSH profiles are baked per provider family at image build time; runtime
  profile/plugin installation is out of scope (needs npm egress).
- Minimal DSH model-provider configuration (plan §5.5 `settings.yaml` block) is generated
  by the harness implementation from the bound `Model`; full manifest-driven injection
  codegen is out of scope.
- Whether the shipped DSH preset keeps or drops the `dsh-tool-ask-user` row is a plan
  decision [NEEDS CLARIFICATION: no answerer exists inside a sandbox; keeping it surfaces
  unanswered questions, dropping it removes a standard-preset capability].
- DSH session-state growth on the durable volume has no retention/size policy in this
  feature; documented as a known limitation (roadmap: budget guardrails).
- Token accounting (`TaskStatus.UsageStats`) remains unpopulated; wiring DSH telemetry is
  out of scope (roadmap: budget guardrails).
- This fork contributes each seam as separate, atomic, cherry-pickable commits per the
  project constitution (`.specify/memory/constitution.md`); the two additive proto
  changes are isolated in their own commits.

### Out of Scope

- **Phase 4 — injection codegen**: `runtime.patch.yml` from `Workspace.spec.mcp`, skills
  materialization into `/workspace/.agents/skills`, `AGENTS.md` generation, full
  `settings.yaml` generation, `agent-presets/` shipping, and MCP registry resolution
  (roadmap: "Dynamic Agentic Environment Curation").
- Codex / Claude Code as delegated subagents (DSH disabled provider rows).
- Budget guardrails and `UsageStats` population.
- Retention/size policy for DSH session state.
- A formal `ax.v1alpha2` package split (additive evolution within `ax.v1alpha1` is
  sufficient; see the versioning analysis in prior discussion).
