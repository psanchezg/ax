---

description: "Task list for DeepSeek Harness & Non-Gemini Model Adaptation"
---

# Tasks: DeepSeek Harness & Non-Gemini Model Adaptation

**Input**: Design documents from `/specs/001-dsh-harness-model-adaptation/`

**Prerequisites**: [plan.md](./plan.md) (required), [spec.md](./spec.md) (required for user stories), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: Included — explicitly required by the spec (SC-002/SC-007 assert automated tests) and by Constitution Principle III (NON-NEGOTIABLE test-backed change). Tests are written first (red), but land **in the same commit** as the implementation they cover (Constitution III).

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1–US5 map to spec.md stories in priority order)
- Include exact file paths in descriptions

## Path Conventions

Repository layout per [plan.md](./plan.md) Project Structure: Go monorepo — `pkg/apis/v1alpha1/` (API), `internal/*` (implementation), `runner/` (embeddable), root-level Dockerfiles, `docs/`, `examples/`. Tests are colocated `*_test.go` (repository convention, no `tests/` tree). All source files keep the Apache 2.0 license header (Constitution II).

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify the baseline and the tooling this feature relies on

- [X] T001 Run the full quality gate on branch `001-dsh-harness-model-adaptation` and confirm green baseline: `make test`, `make build`, `go mod tidy` + `git diff --exit-code go.mod go.sum`, `gofmt -l .`, `go vet ./...`
- [X] T002 [P] Verify the proto regeneration workflow for `pkg/apis/v1alpha1` (protoc/make invocation used by `pkg/apis/v1alpha1/ax.proto`) reproduces `pkg/apis/v1alpha1/*.pb.go` as a clean no-op

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The two additive proto changes (contracts/proto-delta.md) — isolated `feat(api):` commits per research.md R16. They serve US1 (`base_url`), US3 (`modelRef` resolution source), and US4 (`harness`), so they precede all code stories.

**⚠️ CRITICAL**: US1, US3, and US4 cannot begin until this phase is complete. US2 (manifest-only) is the one story that is independent of this phase.

- [X] T003 Proto commit 1 `feat(api): add base_url to ModelSpec`: add `string base_url = 8 [json_name = "baseURL"];` to `ModelSpec` in `pkg/apis/v1alpha1/ax.proto` (keep `reserved 3, 4, 5` untouched), regenerate `pkg/apis/v1alpha1/*.pb.go` in the same commit, and add dual-spelling decode + round-trip tests (`baseURL` and `base_url` accepted; unknown fields round-trip without loss) in `pkg/apis/v1alpha1/types_test.go`
- [X] T004 Proto commit 2 `feat(api): add AgentHarness to WorkspaceSpec`: add `message AgentHarness` (fields: `kind = 1`, `image = 2`, `command = 3` repeated string, `model_ref = 4 [json_name = "modelRef"]`, `env = 5` repeated `EnvVar`, `system_instructions = 6 [json_name = "systemInstructions"]`) and `AgentHarness harness = 4;` to `WorkspaceSpec` in `pkg/apis/v1alpha1/ax.proto`, regenerate `pkg/apis/v1alpha1/*.pb.go` in the same commit, with decode tests in `pkg/apis/v1alpha1/types_test.go` (depends: T003)
- [X] T005 [P] Add version-skew round-trip tests proving old-decoder safety (unknown fields preserved, typo rejection intact per `pkg/apis/v1alpha1/types_test.go:128`) in `pkg/apis/v1alpha1/types_test.go` (SC-006; depends: T003, T004 — same file as T004, run after it)

**Checkpoint**: API delta complete and isolated in two cherry-pickable commits — story implementation can begin

---

## Phase 3: User Story 1 - Any model provider works, failures are loud (Priority: P1) 🎯 MVP

**Goal**: Provider registry with `google`/`openai`/`anthropic` adapters, `baseURL`-targetable endpoints, per-provider parameter translation, typed errors instead of fabricated completions, and apply-time provider validation.

**Independent Test**: Declare a `Model` with `provider: openai` and a local vLLM `baseURL`, apply it, trigger a generation, observe a real completion; repeat with a corrupted API key and observe a typed error — never a fabricated plan. A `Model` with a typo'd provider is rejected at `ax apply` (spec.md US1).

### Tests for User Story 1 ⚠️

> **NOTE: Write these tests FIRST (red); they land in the same commit as the implementation (Constitution III).**

- [X] T006 [P] [US1] Registry table tests (register, lookup, duplicate-name rejection, unknown-provider error listing `Known()` names) in `internal/model/provider_test.go`
- [X] T007 [P] [US1] OpenAI adapter tests via `net/http/httptest`: 2xx → `choices[0].message.content` mapping, 4xx/5xx → `*ProviderError` carrying status and body, empty-key short-circuit, context cancellation, malformed body, in `internal/model/openai_test.go`
- [X] T008 [P] [US1] Anthropic adapter tests via `net/http/httptest`: 2xx → first `type == "text"` block mapping, `x-api-key` + `anthropic-version: 2023-06-01` headers, `max_tokens` default `4096` when absent, 4xx/5xx → `*ProviderError` with status and body, empty-key short-circuit, cancellation, in `internal/model/anthropic_test.go`
- [X] T009 [P] [US1] Parameter translation table tests (`maxOutputTokens`/`maxTokens` → `max_tokens`, `topP` → `top_p`, `topK` → `top_k`, `stopSequences` → `stop`/`stop_sequences`, `candidateCount` → `n`, unknown keys verbatim) in `internal/model/params_test.go`
- [X] T010 [P] [US1] Fallback regression tests: with the `DisableRemote` opt-in unset, empty keys, 4xx/5xx, and transport failures all yield `*ProviderError` and **zero fabricated text** (FR-006, SC-002) in `internal/model/client_test.go`
- [X] T011 [P] [US1] Google byte-compatibility guard tests (same request shape, `generationConfig` pass-through unchanged, SC-005/SC-009) in `internal/model/google_test.go`
- [X] T012 [P] [US1] `UpdateModel` validation tests: unknown provider rejected at apply with the list of valid values (SC-003); empty/`google` accepted; malformed `baseURL` rejected; invalid `secretKey.key` env name rejected in `internal/server/server_test.go`

### Implementation for User Story 1

- [X] T013 [US1] Implement `ProviderError` (`Provider`, `StatusCode`, `Body`, wrapped `Err` with `Unwrap()`), the `Provider` interface (`Name()`, `Generate(ctx, *GenerateRequest) (*GenerateResponse, error)`), and the `Register`/`Known`/`NewClient` registry in `internal/model/provider.go` (depends: T006)
- [X] T014 [US1] Move `generateGoogle` behind the `google` provider implementation with byte-identical request/response behavior in `internal/model/google.go` (depends: T011, T013)
- [X] T015 [P] [US1] Implement the `openai` adapter: `POST {baseURL}/chat/completions`, `Authorization: Bearer <key>`, request `{"model", "messages":[{"role":"user","content":<prompt>}], <translated params>}` in `internal/model/openai.go` (depends: T013)
- [X] T016 [P] [US1] Implement the `anthropic` adapter: `POST {baseURL or https://api.anthropic.com}/v1/messages`, `x-api-key` + `anthropic-version: 2023-06-01`, request `{"model", "max_tokens", "messages":[...], <translated params>}` in `internal/model/anthropic.go` (depends: T013)
- [X] T017 [US1] Implement per-provider parameter translation tables (research.md R5: translated known Gemini spellings, unknown keys pass through verbatim) in `internal/model/params.go` (depends: T009)
- [X] T018 [US1] Wire `ConfigFromSpec` to copy `ModelSpec.base_url` (FR-004), dispatch `Generate` through the registry (`strings.ToLower`; `""` ≡ `google`), and remove `fallbackResponse` from every non-test path — it survives only behind the explicit `DisableRemote` test opt-in — in `internal/model/client.go` (depends: T013–T017)
- [X] T019 [US1] Implement `UpdateModel` provider validation in `internal/server/server.go`: lowercased `spec.provider` must exist in the registry (empty ≡ `google`), else reject with the sorted list of valid names; validate `baseURL` parses as an `http(s)` URL and `secretKey.key` is a valid env var name (depends: T012, T013)
- [X] T020 [P] [US1] Create `examples/model-deepseek.yaml` (`provider: openai`, `model: deepseek-chat`, `baseURL: https://api.deepseek.com/v1`, `secretKey: {name: deepseek-api-secret, key: DEEPSEEK_API_KEY}`) and `examples/model-local-qwen.yaml` (credential-less local vLLM example) with obvious placeholders only
- [X] T021 [P] [US1] Update `docs/manifests.md` so the `provider: anthropic` example (lines ~130–158) works as written, and document `baseURL` + the valid provider values alongside it

**Checkpoint**: User Story 1 fully functional and testable independently (MVP — quickstart.md §1)

---

## Phase 4: User Story 2 - DSH runs in a sandbox today, with zero AX changes (Priority: P2)

**Goal**: A validated, published Phase 0 recipe (custom image + `spec.command` + `debug: true`, no workspace `goal`) that runs DSH against an **unmodified** AX deployment. Zero Go code in this story.

**Independent Test**: Build the DSH runner image, apply the Phase 0 manifest, and observe the task reach `Running` with DSH producing its final message on stdout (spec.md US2). Independent of Foundational and all code stories.

### Implementation for User Story 2

- [X] T022 [P] [US2] Create `examples/task-dsh-demo.yaml` per spec: `Task` with `image: "ghcr.io/<org>/ax-dsh-runner@sha256:..."`, `command: ["dsh", "--profile", "headless", "Set up this workspace and report what you did."]`, `env` plain `name`/`value` only (task env has **no** `valueFrom`/secret reference today), workspace binding **without** a `goal`, `debug: true`
- [X] T023 [P] [US2] Create `docs/dsh-phase-0.md`: the runnable recipe, a self-contained throwaway image recipe (node:22-slim + pinned `@deepseek-ai/dsh` + `ax-task-runner`), the **double-bootstrap caveat** (a workspace `goal` plus `GEMINI_API_KEY` runs both harnesses — omit the `goal` in Phase 0), and the Gateway egress note (allowlist the model endpoint host:port; default `*:443` keeps out-of-the-box working)
- [X] T024 [US2] Validate the recipe verbatim against an unmodified deployment (quickstart.md §2): task reaches `Running`, `bin/ax ssh <task> -- ...` with `debug: true` shows the DSH process and workspace state (depends: T022, T023)

**Checkpoint**: The DSH premise is proven end to end with zero AX code changes (FR-020)

---

## Phase 5: User Story 3 - Credentials come from `Model`, not from a literal (Priority: P3)

**Goal**: The controller resolves the credential from the referenced `Model` and injects it under the name the `Model` declares, plus `AX_MODEL_BASE_URL` and `AX_MODEL_YAML`; the metadata server serves the model binding. Key rotation becomes one secret update.

**Independent Test**: Bind a task to a workspace whose model reference points at the `deepseek` `Model`; inspect the container env and metadata server; observe the credential under `DEEPSEEK_API_KEY`, `AX_MODEL_BASE_URL`, and `/metadata/v1alpha1/ax/model` serving the model binding (spec.md US3). Depends on Foundational (`modelRef` field) and US1 (`baseURL` semantics).

### Tests for User Story 3 ⚠️

- [X] T025 [P] [US3] Reconciler tests in `internal/controller/reconciler_test.go`: model bound → secret value injected under `<Model.spec.secretKey.key>` + `AX_MODEL_BASE_URL` + `AX_MODEL_YAML`; **no model reference → `gemini-api-secret`/`GEMINI_API_KEY` injected exactly as today** (FR-009); unresolvable secret → typed error with **no partial injection**
- [X] T026 [P] [US3] Metadata server tests in `internal/metadata/server_test.go`: `/metadata/v1alpha1/ax/model` returns the bound `Model`; unbound → consistent empty/404 behavior alongside `/task` and `/workspaces`; **never serves secret values** (only the `secretKey` reference)
- [X] T027 [US3] Golden test asserting `AX_MODEL_YAML` content equals the metadata route's `Model` serialization (single source) in `internal/controller/reconciler_test.go` (depends: T025 — same file)

### Implementation for User Story 3

- [X] T028 [US3] Replace `lookupGeminiKey` with `resolveModelCredential(ctx, atespace, modelRef)` reading the `Model` from the store and resolving `spec.secretKey` against the Kubernetes secret in `internal/controller/reconciler.go` (typed error on failure, no silent fallback to the Gemini literal when a model reference exists; legacy literal path preserved byte-for-byte when there is none) (depends: T025)
- [X] T029 [US3] Inject `<spec.secretKey.key>` (secret value) + `AX_MODEL_BASE_URL` (`spec.baseURL`, empty when unset) + `AX_MODEL_YAML` (serialized `Model`, reference not value) into the actor template env alongside `AX_TASK_YAML`/`AX_WORKSPACES_YAML` in `internal/controller/reconciler.go` (depends: T028)
- [X] T030 [US3] Add `GET /metadata/v1alpha1/ax/model` to `internal/metadata/server.go`, same content as `AX_MODEL_YAML`, consistent unbound behavior (depends: T026, T028)
- [X] T031 [P] [US3] Update the container contract in `docs/runner.md` to document the generalized credential variables (`<secretKey.key>`, `AX_MODEL_BASE_URL`, `AX_MODEL_YAML`) **instead of the hardcoded `GEMINI_API_KEY`** (FR-011), preserving the legacy pair note

**Checkpoint**: Rotation is one secret update (SC-004); the last Gemini literal is gone from the control plane except the preserved legacy path

---

## Phase 6: User Story 4 - Harness choice is declarative (Priority: P4)

**Goal**: `Workspace.spec.harness` selects the in-sandbox harness per workspace: `antigravity` (default, unchanged) and `deepseek-harness` (DSH one-shot) as peers, fail-closed on unknown kinds, with the minimal `settings.yaml` model-provider binding generated from the bound `Model`.

**Independent Test**: Apply a `Workspace` with `harness.kind: deepseek-harness` and `modelRef: deepseek`, apply a task with a `goal`, observe `dsh` one-shot headless against the goal with the model bound; same manifest without `harness` → identical Antigravity behavior to today (spec.md US4). Depends on Foundational (proto) + US3 (`AX_MODEL_YAML` discovery) + US1 (provider semantics for the binding).

### Tests for User Story 4 ⚠️

- [X] T032 [P] [US4] Harness resolution table tests in `internal/workspace/harness_test.go`: `""`/unset → antigravity; `"antigravity"` explicit; `"deepseek-harness"`; unknown kind → typed `*UnsupportedHarnessError` and condition reason `UnsupportedHarness` naming the kind — **setup never silently skipped** (FR-013)
- [X] T033 [P] [US4] Golden tests for the generated `$DSH_HOME/settings.yaml` provider binding in `internal/workspace/harness_deepseek_test.go`: entry named after `Model.metadata.name`, `apiKeyEnv` = `spec.secretKey.key`, `baseURL` = `spec.baseURL`, `api` mapping `openai` → `openai-completions`, `anthropic` → `anthropic-messages`, `google` → `openai-completions` against `https://generativelanguage.googleapis.com/v1beta/openai/` (R18), `models[].id` = `spec.model`; **only** this binding is generated (FR-018 — no patch/presets/AGENTS.md/skills)
- [X] T034 [US4] Env/argv construction tests in `internal/workspace/harness_deepseek_test.go`: `DSH_HOME=/ax/dsh`, `DSH_PERMISSION_MODE=danger-full-access` (never overridable via `harness.env`), credential var inherited, default argv `dsh --profile headless "<goal>"`, `harness.command` replaces argv after `dsh`, `harness.systemInstructions` prepended to the prompt; unresolvable `modelRef` → condition `UnresolvedModelRef` (depends: T033 — same file)
- [X] T035 [P] [US4] Isolation invariant test asserting the sandboxed agent cannot write outside `/workspace` while writes inside `/workspace` succeed (SC-007, mock Substrate gRPC boundary) in `runner/isolation_test.go`
- [X] T036 [US4] Verify the existing bootstrap tests pass **unmodified** (SC-009 byte-compatibility of the Antigravity path) in `internal/workspace/setup_test.go`

### Implementation for User Story 4

- [X] T037 [US4] Implement the `Harness` interface (`Kind()`, `Setup(ctx, *v1alpha1.AgentHarness, goal, workspacePath string) error`), `Resolve(kind)` fail-closed resolver, and `*UnsupportedHarnessError` in `internal/workspace/harness.go` (depends: T032)
- [X] T038 [US4] Move `runBootstrap` semantics behind the `antigravity` harness with identical behavior — script `/usr/local/bin/antigravity_bootstrap.py` via `python3`, args `--goal/--workspace/--data-dir /ax/antigravity`, env `GEMINI_API_KEY` — in `internal/workspace/harness_antigravity.go` (depends: T036, T037)
- [X] T039 [US4] Implement `deepseek-harness` in `internal/workspace/harness_deepseek.go`: resolve the bound `Model` from `AX_MODEL_YAML`/`modelRef`, write **only** the minimal `$DSH_HOME/settings.yaml` binding (FR-018), set `DSH_HOME=/ax/dsh` and `DSH_PERMISSION_MODE=danger-full-access` (set by the implementation, never defaulted — R14), run `dsh --profile headless "<goal>"` as the supervised child; run the shipped `standard` preset untouched (`dsh-tool-ask-user` questions surface but never block — R1) (depends: T033, T034, T037, T038)
- [X] T040 [US4] Dispatch `SetupWorkspace` through `Resolve()` in `internal/workspace/setup.go`: unknown kind → workspace not-`Ready` with condition reason `UnsupportedHarness`; unresolvable `modelRef` → `UnresolvedModelRef`; goal→prompt contract and `/readyz?check=workspace` semantics identical across harnesses (FR-016) (depends: T037–T039)
- [X] T041 [US4] Wire `harness.image` as the task-runner image override in `internal/controller/reconciler.go` (empty ⇒ existing Python/Antigravity image, unaffected) (depends: T040)
- [X] T042 [P] [US4] Create `Dockerfile.task-runner-dsh`: `FROM node:22-slim`, apt deps (git curl ca-certificates openssh-client procps bash), `npm install -g @deepseek-ai/dsh@${DSH_VERSION}` from a pinned `ARG`, `ENV DSH_HOME=/ax/dsh`, copy `bin/linux_amd64/ax-task-runner` to `/usr/local/bin/`, same entrypoint; **no custom profile** — the shipped `headless` profile auto-initializes from the package's local templates, so first boot needs no npm egress (FR-019, SC-008, research.md R13); leave `Dockerfile.task-runner` untouched (FR-017)
- [X] T043 [P] [US4] Create `examples/workspace-dsh.yaml` (`harness: {kind: deepseek-harness, modelRef: deepseek, image: ...}` with placeholder image ref)

**Checkpoint**: Declarative harness complete; Antigravity default preserved, DSH selectable per workspace (closes `docs/roadmap.md:32`)

---

## Phase 7: User Story 5 - The documentation tells the truth (Priority: P5)

**Goal**: `DESIGN.md` describes the agent-harness contract and `Model`'s data-path role; `docs/deepseek-harness.md` documents image build, profile pre-provisioning, the environment contract, and the isolation decision; every documented claim matches implemented behavior.

**Independent Test**: Review each documented claim against the implemented behavior — every RPC, env var, and recipe can be exercised as written (spec.md US5).

### Implementation for User Story 5

- [ ] T044 [P] [US5] Update `DESIGN.md` with exactly the four additions (FR-022): the fifth component row for the agent-harness contract ("A replaceable, declaratively selected program that turns a workspace goal into a prepared workspace and a running agent…"), the `Model` data-path statement (control plane resolves credentials and injects them; harnesses read the binding at runtime), the provider-registry note (identifiers validated against a registry — a new provider is an adapter, not a schema change), and the single-isolation-boundary invariant (**Agent Substrate is the only isolation boundary**)
- [ ] T045 [P] [US5] Create `docs/deepseek-harness.md` (FR-023): image build, profile pre-provisioning, environment contract, isolation decision, plus known limitations — `dsh-tool-ask-user` preset policy (R1) and `DSH_HOME` session-state growth with no retention policy (R2)
- [ ] T046 [P] [US5] Validate `examples/` against the code they describe (Constitution IV: docs and examples compile/validate) in `examples/`
- [ ] T047 [US5] Cross-document truthfulness pass: every claim in `DESIGN.md`, `docs/runner.md`, `docs/manifests.md`, `docs/deepseek-harness.md`, `docs/dsh-phase-0.md`, and `docs/concepts.md` (key-rotation claim now true after US3) exercises as written; fix drift in place (depends: T044–T046)

**Checkpoint**: All five success stories complete; docs match reality

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Whole-feature verification, security pass, and export rehearsal

- [ ] T048 [P] Execute quickstart.md §1–§5 end to end and record results in `specs/001-dsh-harness-model-adaptation/quickstart.md`
- [ ] T049 Verify every Success Criterion SC-001…SC-009 in `specs/001-dsh-harness-model-adaptation/spec.md` against evidence (add pointers to the tests/scenarios that prove each)
- [ ] T050 Security pass per Constitution Security constraints: no real secrets/keys/URLs anywhere in `docs/`, `examples/`, tests, or history (placeholders only); deny-by-default egress documented; no workflow/permission changes smuggled in
- [ ] T051 Verify commit slicing per research.md R16 in `git log`: atomic Conventional Commits, the two `feat(api):` proto commits isolated, fork-local artifacts absent; run the cherry-pick test per seam: `git format-patch upstream/main..<seam-branch>` applies cleanly on a fresh `upstream/main` and builds + passes `make test` there
- [ ] T052 Final quality gate (Constitution verification gate): `make test`, `make build`, `go mod tidy` + `git diff --exit-code go.mod go.sum`, `gofmt -l .`, `go vet ./...`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Setup — blocks US1, US3, US4
- **US1 (Phase 3, P1)**: Foundational only — no story dependencies → **MVP**
- **US2 (Phase 4, P2)**: **No dependencies at all** (manifest-only) — can run fully in parallel with everything
- **US3 (Phase 5, P3)**: Foundational + US1 (`baseURL` semantics; registry for error behavior)
- **US4 (Phase 6, P4)**: Foundational + US1 + US3 (`AX_MODEL_YAML` discovery, `modelRef` resolution)
- **US5 (Phase 7, P5)**: All code stories (its claims describe them)
- **Polish (Phase 8)**: Depends on all desired stories being complete

### User Story Dependencies

- **US1 (P1)**: after Foundational — independent
- **US2 (P2)**: immediately — independent of everything (zero-code)
- **US3 (P3)**: after US1 — independent of US2/US4
- **US4 (P4)**: after US3 — independent of US2
- **US5 (P5)**: last — describes US1–US4

### Within Each User Story

- Tests written first (red) but committed together with their implementation (Constitution III)
- Interfaces/types before adapters/implementations (T013 before T015/T016)
- Core implementation before wiring (T028 before T029/T030)
- Story checkpoint before moving to the next priority

### Parallel Opportunities

- US2 runs entirely in parallel with Phases 1–2 and every code story
- Test tasks marked [P] within a story can be written together (different files)
- US1 adapter tasks T015/T016 are parallel after T013
- T020/T021 (examples + manifests doc) parallel with US1 core
- T042/T043 (image + example) parallel with US4 runner work
- T044/T045/T046 (docs) parallel among themselves

---

## Parallel Example: User Story 1

```bash
# Launch all US1 tests together (different files):
Task: "Registry table tests in internal/model/provider_test.go"
Task: "OpenAI adapter tests in internal/model/openai_test.go"
Task: "Anthropic adapter tests in internal/model/anthropic_test.go"
Task: "Parameter translation tests in internal/model/params_test.go"

# After T013 (provider.go), launch both adapters together:
Task: "Implement the openai adapter in internal/model/openai.go"
Task: "Implement the anthropic adapter in internal/model/anthropic.go"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1: Setup → Phase 2: Foundational (two isolated `feat(api):` commits)
2. Phase 3: US1 (provider registry + adapters + typed errors + validation)
3. **STOP and VALIDATE**: quickstart.md §1 — free/self-hosted model generating, typo'd provider rejected at apply, corrupted key → typed error
4. Ship as `feat(model):` commit sequence (R16 slice 1) — independently valuable to the planner and any `Model` consumer

*Optionally ship US2 even earlier: it needs no code at all and de-risks the whole premise.*

### Incremental Delivery

1. Foundational → US1 → validate → MVP export-ready (slice 1)
2. + US2 (recipe docs) → validate on unmodified deployment
3. + US3 → key rotation is one secret update (slice 3)
4. + US4 → declarative harness, roadmap item closed (slice 4)
5. + US5 → truthful docs (slice 5); Polish → export rehearsal per seam

### Parallel Team Strategy

1. Everyone: Setup + Foundational together
2. Then: Developer A → US1; anyone → US2 (docs/manifests only); B blocked until US1 lands, then US3 → US4 sequentially (shared files in `internal/controller`, `internal/workspace`); C → US5 doc skeleton, finalize last

---

## Notes

- [P] tasks = different files, no dependencies; same-file tasks are deliberately unmarked
- Commit guidance (Constitution I): one logical change per commit; tests land with their change; the two proto commits stay isolated (R16); fork-local artifacts never enter seam commits
- Stop at any checkpoint to validate a story independently
- Avoid: vague tasks, same-file parallel conflicts, cross-story dependencies that break independence (only the documented US1→US3→US4 chain exists)
