# Adapting AX to DeepSeek Harness and non-Gemini models

Status: proposal. Phases 0, 1, 2, 3, and 5 of the plan below are now implemented —
see [DeepSeek Harness](deepseek-harness.md), [Runners](runner.md), and
[Running DeepSeek Harness today](dsh-phase-0.md). Phase 4 (manifest-driven
injection of MCP servers, skills, `AGENTS.md`, and presets) remains roadmap work
under "Dynamic Agentic Environment Curation" in [roadmap.md](roadmap.md), and the
implementation differs from the sketch below in one place: the DSH image runs the
shipped `headless` profile rather than a custom profile created from it, because
`--from-default-profile` creates the profile and boots it, so it cannot run
during an image build.

This document reviews [`DESIGN.md`](../DESIGN.md) against a concrete goal: run
agents in AX using **DeepSeek Harness (DSH)** instead of (or alongside)
Antigravity, and drive them with **free or additional model providers** rather
than only Gemini. It records what the current architecture already gives us,
what is hardcoded, the two seams that need to be introduced, and a phased path
that starts with zero code changes.

## 1. What DESIGN.md covers, and what it leaves out

`DESIGN.md` describes the **control plane**: `ax` CLI → `ax-server` (gRPC,
Redis-backed) → Redis Streams → `ax-controller` → Agent Substrate → task
container. It lists the four binaries, the `AX` gRPC service, and the
`Task` / `Workspace` / `Model` RPC groups.

Two things the goal depends on are absent from it:

1. **Which agent actually runs inside the sandbox.** The architecture diagram
   stops at "Agent Substrate" and the components table describes `ax-task-runner`
   as something that "runs the agent command". Nothing in the design says how
   the *in-sandbox agent harness* is selected, configured, or credentialed.
   Today that answer is hardcoded to Antigravity.
2. **What `Model` is for, and who consumes it.** `DESIGN.md` documents the
   `Model` RPCs but never places `Model` on the data path. In the code it
   currently is not on the data path at all (see §2.2).

The design is otherwise well positioned for this change: the manifest surface is
already provider-agnostic, and the runner already has a clean
"contract + replaceable image" story in [`docs/runner.md`](runner.md). The work
is almost entirely about closing the gap between what the manifests promise and
what the runtime does.

## 2. Current state: where the hardcoding lives

### 2.1 The model client only speaks Gemini

`internal/model/client.go` defines a single provider constant and a single
generation implementation:

| Location | Behavior |
|---|---|
| `internal/model/client.go:39-47` | `ProviderGoogle = "google"` is the only provider constant. |
| `internal/model/client.go:486-495` | `Generate` dispatches to `generateGoogle` when the provider is `google` or empty; **any other provider returns `unsupported provider %q`**. |
| `internal/model/client.go:499-606` | `generateGoogle` is Gemini `generateContent` over REST, with API key in the query string. |

The `ModelSpec` schema itself is fine and needs no change:
`provider`, `model`, `secretKey`, and a free-form `parameters` struct
(`pkg/apis/v1alpha1/ax.proto:246-257`).

Two concrete defects matter for this goal:

- **A custom endpoint cannot be expressed in a manifest.**
  `Config.BaseURL` exists (`client.go:71`) and is settable in code via
  `WithBaseURL` (`client.go:209-213`), but `ConfigFromSpec` never copies a base
  URL from the spec (`client.go:87-95`) and `ModelSpec` has no `base_url` field.
  Self-hosted vLLM, Ollama, LM Studio, or any gateway therefore cannot be
  targeted declaratively.
- **Misconfiguration is silent.** When the API key is empty
  (`client.go:500`) or the API returns 401/403/429/503
  (`client.go:563-568`), or the HTTP call itself fails (`client.go:556-560`),
  the client returns a fabricated `fallbackResponse` (`client.go:608-621`)
  that reads as a plausible plan. A wrong key in production is
  indistinguishable from success.

### 2.2 `Model` is not on the runtime path

- `ax-server` stores `Model` resources but performs **no validation** on them
  (`internal/server/server.go:398-412`); a typo'd provider is accepted at
  `ax apply` time and only surfaces later, if at all.
- The only consumer of the model client is `workspace.Planner`
  (`internal/workspace/planner.go`), and `PlanEnvironment` has **no non-test
  caller**. `runner.Run` calls `workspace.SetupWorkspace`
  (`runner/runner.go:157`), which goes straight to `runBootstrap` and never
  consults the planner or the `Model` resource.
- `docs/manifests.md:130-158` documents `provider: anthropic` with a full
  example. That path cannot work: it hits the `unsupported provider` branch.

So today `Model` is effectively decorative for a running task, and the
documentation overstates it.

### 2.3 The agent harness is hardcoded to Antigravity

| Location | Hardcoding |
|---|---|
| `internal/workspace/setup.go:46-56` | `bootstrapScriptPath = "/usr/local/bin/antigravity_bootstrap.py"`, `bootstrapAPIKeyEnv = "GEMINI_API_KEY"`, `bootstrapDataDir = "antigravity"`. |
| `internal/workspace/setup.go:277-315` | `runBootstrap` shells out to `python3 <script> --goal ... --workspace ... --data-dir ...`. There is no harness dispatch. |
| `Dockerfile.task-runner:26-29` | The image `pip install`s `google-antigravity` and copies that one script. |
| `internal/controller/reconciler.go:41-42` | `geminiSecretName = "gemini-api-secret"`, `geminiSecretKey = "GEMINI_API_KEY"`. |
| `internal/controller/reconciler.go:152-154` | Injects `GEMINI_API_KEY` into the actor template environment. |
| `internal/controller/reconciler.go:371-385` | `lookupGeminiKey` resolves that literal secret/name; it never reads the `Model` resource. |
| `docs/runner.md:20` | Documents `GEMINI_API_KEY` as part of the container contract. |

Note also that the container never receives the `Model` resource at all: the
controller injects `AX_TASK_YAML` and `AX_WORKSPACES_YAML`
(`reconciler.go:156-164`) but there is no `AX_MODEL_YAML`, and the metadata
server exposes only `/task` and `/workspaces`
(`internal/metadata/server.go:93-100`).

### 2.4 MCP and skills are declared but not implemented

The manifests promise more than the runner delivers. `Workspace.spec.mcp`
(`ax.proto:207-224`) and `Workspace.spec.skills` (`ax.proto:226-235`) are fully
modelled and rendered by `ax describe workspace` (`cmd/ax/main.go:647-675`), but
`SetupWorkspace` never reads them:

- `setupSkills` (`internal/workspace/setup.go:262-270`) only calls
  `os.MkdirAll(skills.Path)`. `SkillsConfig.registries` is ignored entirely, so
  nothing is ever fetched into that directory.
- `Workspace.spec.mcp` is **not referenced anywhere** in `internal/workspace`.
  No MCP configuration is written, so no MCP server ever reaches the agent.

`docs/runner.md:39` instructs a runner to "write any MCP configuration", which
the default runner does not do. Any design that injects MCP servers and skills
into a harness is therefore implementing this stub, not extending it.

### 2.5 What already works in our favor

- `TaskSpec.command` (`ax.proto:81`) and `TaskSpec.image` (`ax.proto:80`) let a
  task run an arbitrary binary from an arbitrary image. **DSH can be run today
  with no AX changes** (see §4, Phase 0).
- The runner contract is documented and explicitly designed for replacement
  (`docs/runner.md:58-134`).
- Egress is Substrate's: which model endpoints a task may reach is a
  deployment concern, not an AX resource or a manifest field.
- `go.mod` has no provider SDKs; adding an OpenAI-compatible adapter needs no
  new dependency (plain `net/http`, as `generateGoogle` already does).

### 2.6 Relevant roadmap alignment

`docs/roadmap.md:32` already commits to "Allow Customization: Decouple the
built-in workspace bootstrap and coding agent harness so users can configure
custom agent runtimes, system instructions, tool policies, and model
configurations." This proposal is the concrete design for that item, plus the
`Model` provider work it depends on.

## 3. DeepSeek Harness: the integration surface

DSH (`@deepseek-ai/dsh`, observed version `0.1.5-rc.2`) is a good fit for the
runner contract because it ships a **one-shot headless mode**:

```bash
dsh --profile headless "set up this workspace for the declared goal"
# Answer one task, stream reasoning to stderr, print the final assistant
# message, and exit.
```

That maps directly onto `runner.Run`'s supervised child process
(`runner/runner.go:167-186`).

Facts that shape the integration:

| DSH surface | Why it matters to AX |
|---|---|
| `--profile headless` | One-shot, stdout/stderr, exit code. Matches `spec.command` semantics and the `OnCommandExit` hook. |
| Profiles are patch layers under `$DSH_HOME/profiles/<name>`, created from templates via `--from-default-profile headless`, overlaid with `--patch <file.yml>` | The harness becomes *configuration*, not code. AX can ship one profile patch per model provider. |
| Provider-neutral LLM seam: `dsh-llm` interface with adapters `dsh-llm-deepseek` and `dsh-llm-pi-ai` | This is the answer to "non-Gemini models". `dsh-llm-pi-ai` speaks `openai-completions`, `openai-responses`, and `anthropic-messages`, and its provider profile accepts `apiKeyEnv`, `baseURL`, `api` (protocol), `models[]`, `headers`, and `modelOverrides`. It indexes named providers including `openai`, `openrouter`, `together`, `baseten`, `zai`, `qwen`, and vLLM-style local routes. |
| Credentials resolve from environment through `apiKeyEnv` credential refs (`DEEPSEEK_API_KEY`, `DEEPSEEK_BASE_URL`, `DEEPSEEK_DEFAULT_MODEL`, …) | AX must inject the key under the name the provider profile declares, not a fixed `GEMINI_API_KEY`. |
| `dsh-session-persistence-jsonl`, `dsh-session-query-sqlite`, `dsh-session-telemetry-otel` | Session history and telemetry can land on the durable volume and be exported as OTLP — see `docs/roadmap.md:43`. |
| `dsh-sandbox*`, `dsh-fs-sandbox`, `dsh-bash-sandbox`, `dsh-permission-presets`, `dsh-user-approval` | DSH has its own isolation and approval layers, both driven by the single `DSH_PERMISSION_MODE` variable. One value turns both off so Substrate stays the only boundary — see §5.6. |
| `dsh plugin` forwards to `pnpm` inside the profile directory | Profiles are pnpm workspaces. **Pre-provision the profile at image build time**, or first boot needs registry egress and adds minutes of latency. |

Recommended container shape (analogous to the Antigravity image):

```dockerfile
FROM node:22-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl ca-certificates openssh-client procps bash \
    && rm -rf /var/lib/apt/lists/*
RUN npm install -g @deepseek-ai/dsh
# Materialize the profile once, at build time, so first boot needs no npm access.
ENV DSH_HOME=/ax/dsh
COPY dsh-profile/ /ax/dsh/profiles/ax-headless/
COPY bin/linux_amd64/ax-task-runner /usr/local/bin/ax-task-runner
ENTRYPOINT ["/usr/local/bin/ax-task-runner"]
```

## 4. Target design

The change splits into two independent seams. They can ship separately and in
this order.

### Seam A — Provider registry in the control plane (models)

Turn `internal/model` from "Gemini client" into "provider registry":

```go
// internal/model/provider.go
type Provider interface {
    Name() string
    Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)
}

// Register is called from provider init() functions; NewClient looks up by
// strings.ToLower(cfg.Provider).
func Register(name string, factory func(Config) Provider)
```

Three implementations cover essentially everything the goal asks for:

| Provider | Covers |
|---|---|
| `google` | Existing `generateGoogle`, unchanged. |
| `openai` (OpenAI-compatible `/chat/completions`) | DeepSeek's own API, OpenRouter, Together, Groq, Fireworks, Baseten, vLLM, Ollama, LM Studio, llama.cpp server. **This single adapter is the highest-leverage change in the document.** |
| `anthropic` | Messages API, which `docs/manifests.md` already advertises. |

Schema and plumbing changes:

1. **Add `base_url` to `ModelSpec`** (next free field is 8) and copy it in
   `ConfigFromSpec`. Without this, no self-hosted or gateway endpoint can be
   declared.
2. **Map `parameters` per provider.** Today `parameters` flows into Gemini's
   `generationConfig` verbatim (`client.go:530-544`), so `maxOutputTokens` is a
   Gemini spelling. The OpenAI-compatible adapter must translate
   (`maxOutputTokens` → `max_tokens`/`max_completion_tokens`, and so on). Keep
   the free-form struct; translate at the adapter boundary.
3. **Never fabricate a completion.** Gate `fallbackResponse` behind an explicit
   opt-in (`DisableRemote`, already used by tests) and return a typed error
   otherwise. Return the provider's status body on 4xx/5xx. This is a
   correctness fix independent of DSH.
4. **Validate at apply time.** `UpdateModel` should reject an unknown provider
   against the registry, so `ax apply` fails instead of a task silently
   degrading.

Resulting manifest — no new kind, no new concept:

```yaml
apiVersion: ax.io/v1alpha1
kind: Model
metadata:
  name: deepseek
  atespace: default
spec:
  provider: openai              # OpenAI-compatible adapter
  model: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  secretKey:
    name: deepseek-api-secret
    key: DEEPSEEK_API_KEY
  parameters:
    temperature: 0.6
    maxTokens: 8000
---
# Free / local alternative, same adapter, no credential:
apiVersion: ax.io/v1alpha1
kind: Model
metadata:
  name: local-qwen
  atespace: default
spec:
  provider: openai
  model: Qwen/Qwen3-Coder-30B-A3B-Instruct
  baseURL: http://vllm.ax-system.svc.cluster.local:8000/v1
```

### Seam B — Agent harness abstraction (the runner)

Make the in-sandbox harness a declarative choice with Antigravity and DSH as
peers. Add to the API:

```proto
message AgentHarness {
  // kind selects the built-in integration: "antigravity" or "deepseek-harness".
  string kind = 1;
  // image overrides the task runner image, since each harness has its own
  // runtime (Python + google-antigravity vs Node + @deepseek-ai/dsh).
  string image = 2;
  // command overrides the harness invocation; defaults per kind.
  repeated string command = 3;
  // modelRef names the Model resource whose credentials and endpoint this
  // harness should use.
  string model_ref = 4;
  // env is merged into the harness process environment.
  repeated EnvVar env = 5;
  // systemInstructions overrides the harness persona.
  string system_instructions = 6;
}

message WorkspaceSpec {
  repeated GitRepo git = 1;
  MCPConfig mcp = 2;
  SkillsConfig skills = 3;
  AgentHarness harness = 4;   // new
}
```

Resolution rule (explicit, and fail closed): if `Workspace.spec.harness.kind`
is unset, keep today's Antigravity behavior for backward compatibility; if it is
set to an unknown kind, the workspace reports not-ready with a clear condition
rather than silently skipping setup.

Runner-side changes:

1. Replace the hardcoded `runBootstrap` with a dispatcher
   (`harness.Setup(ctx, harness, goal, path, dataDir)`) and move the Antigravity
   implementation into an `antigravity` harness that keeps the current script
   and semantics.
2. Add a `deepseek-harness` implementation that:
   - runs `dsh --profile <profile> --patch <patch.yml> "<goal prompt>"`,
   - points `DSH_HOME` at a **durable, non-workspace** directory
     (parity with `bootstrapDataDir = /ax/antigravity`) so suspend/resume keeps
     session state while `/workspace` stays the agent's only file surface,
   - injects the model endpoint/key env vars that the DSH provider profile
     declares via `apiKeyEnv`,
   - selects a non-interactive permission preset and enables DSH's workspace-only
     fs/bash policy, mirroring the intent of the current
     `policy.workspace_only(...) + policy.allow_all()`.
3. Keep the goal→prompt contract identical: the harness receives the workspace
   goal and is responsible for preparing the workspace. This keeps `readyz`
   semantics (`/readyz?check=workspace`) unchanged.

### Seam C — Credentials derived from `Model`, not from a literal

Once `AgentHarness.modelRef` exists, the controller must stop hardcoding the
Gemini secret:

1. `lookupGeminiKey` (`reconciler.go:371-385`) becomes
   `resolveModelCredential(ctx, atespace, modelRef)`, reading the referenced
   `Model` resource from the store and resolving `spec.secretKey`.
2. Inject the secret under **the key name the `Model` declares**, plus an
   endpoint variable, rather than a fixed `GEMINI_API_KEY`.
3. Inject `AX_MODEL_YAML` alongside `AX_TASK_YAML`/`AX_WORKSPACES_YAML`, and add
   `/metadata/v1alpha1/ax/model` to the metadata server so a harness can
   discover its configuration at runtime instead of relying only on env vars.
4. Update `docs/runner.md:20` so the container contract documents the
   generalized credential variables instead of `GEMINI_API_KEY`.

### Seam D — Egress

Egress belongs to Substrate: the sandbox reaches the model endpoint through
Substrate's networking, so AX itself needs no change per endpoint. A hardened
deployment allowlists the chosen endpoint per port where Substrate is
configured, because a blocked provider looks exactly like a hung agent:

- `api.deepseek.com:443`, `openrouter.ai:443`, `api.together.xyz:443`
- self-hosted inference inside the cluster, e.g.
  `vllm.ax-system.svc.cluster.local:8000`

Combined with pre-baked DSH profiles (§3), this means a sandboxed task needs
**no npm registry access at runtime**.

## 5. Injecting configuration, MCP, subagents, and skills

DSH is configured through three channels, and AX already owns the data that
belongs in each. Injection is a **codegen step**: the runner translates the
`Task` / `Workspace` / `Model` resources into DSH files at setup time, then
invokes the harness with them.

| Channel | Location | DSH reads it as | AX source |
|---|---|---|---|
| Process environment | container env | `DSH_PERMISSION_MODE`, `DSH_HOME`, provider `apiKeyEnv`, base-URL vars | `spec.env` + controller-injected `Model` credentials |
| Project files | `/workspace`, the command's cwd | `AGENTS.md` / `CLAUDE.md`, `.dsh/skills/`, `.agents/skills/` | `Workspace.spec.git`, `goal`, `skills.path` |
| Deployment files | `$DSH_HOME` (`/ax/dsh`) | profile, `--patch` overlays, `agent-presets/`, `settings.yaml`, `sessions/` | `AgentHarness`, `Workspace.spec.mcp`, `Model` |

The invocation that ties them together:

```bash
dsh --profile ax-headless \
    --patch /ax/dsh/runtime.patch.yml \
    "<goal>"
```

`--patch` takes a path to a patch-list YAML and is repeatable, so
`/ax/dsh/runtime.patch.yml` is the single generated artifact carrying everything
derived from the manifests. The patch format is a top-level array of entries
where each entry either **overrides an existing row by `id`** (including
`disabled: true`, which is how plugin rows are turned off) or **inserts rows**
into an existing group, or at the top level, via `insert:`.

### 5.1 Why this works without new machinery

Three DSH defaults line up with AX's existing layout:

- The runner starts `spec.command` with the **first workspace as its working
  directory** (`runner/runner.go:175`), and `dsh-skill-filesystem` discovers
  skills from `<projectRoot>/.dsh/skills` and `<projectRoot>/.agents/skills`.
  Materializing skills into `/workspace/.agents/skills` therefore needs **zero
  DSH configuration**.
- `dsh-agent-instructions` reads `AGENTS.md` (or `CLAUDE.md`) from the project
  root and folds it into the system prompt.
- `$DSH_HOME` is ours to define, so the profile, presets, `settings.yaml` and the
  session store all live outside `/workspace` — keeping the agent's own state off
  the workspace's file surface, exactly as `bootstrapDataDir = /ax/antigravity`
  does today.

### 5.2 MCP

`dsh-mcp-client` is **one plugin instance per server**, so the translation is
mechanical: each `Workspace.spec.mcp.servers[]` entry becomes one patch row, with
the transport chosen by which fields are set.

```yaml
# /ax/dsh/runtime.patch.yml — generated by the runner from Workspace.spec.mcp
- insert:
    # endpoint set -> streamable-http
    - id: mcp-git-tools
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        transport: streamable-http
        serverName: git-tools
        url: http://git-mcp.default.svc.cluster.local:8080
    # command/args set -> stdio
    - id: mcp-filesystem
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        transport: stdio
        serverName: filesystem
        command: npx
        args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
```

This maps cleanly onto AX's `MCPServer` (`name`, `endpoint`, `command`, `args`).

`Workspace.spec.mcp.registries[]` (`provider`, `project`, `query`) has no DSH
analogue: registries are a **discovery** step that must be resolved to concrete
servers *before* codegen. That resolution is unimplemented in AX today (see
§2.4) and is the one piece of genuinely new work here. It belongs in the setup
phase, and the roadmap already scopes it as "Dynamic Agentic Environment
Curation" (`docs/roadmap.md:31`).

### 5.3 Subagents

DSH subagents are not a runtime flag: they come from the **agent preset**, a
directory holding `agent.cordis.yml` plus an optional `preset.yml` carrying
display metadata. The shipped `standard` preset is the reference — it carries
`tool-subagent` (`provider: spawn`), `tool-subagent-fork` (`provider: fork`,
history-preserving for KV-cache reuse), `tool-workflow`, and `tool-ralph`,
alongside two `disabled: true` rows for the `subagent_codex` and
`subagent_claude_code` providers.

Selecting subagents therefore means shipping a preset directory and naming it for
the session. Two consequences worth designing around:

- Presets are **agent-plane only**. The comment at the top of the shipped
  `standard` preset is explicit that the host composition keeps "the sandbox and
  approval stack, persistence, and the model route". Isolation cannot be granted
  or removed from a preset.
- The disabled provider rows are how DSH delegates to **other harnesses**.
  Shipping the matching bundles and re-enabling a row is a legitimate way to run
  Codex or Claude Code as a subagent inside an AX sandbox — a strong argument for
  keeping the harness contract (Seam B) generic rather than DSH-specific.

### 5.4 Skills

Skills are files. `dsh-skill-filesystem` scans, in rank order:

1. `<projectRoot>/.dsh/skills`
2. `<projectRoot>/.agents/skills`
3. `customSkillDirs[]` (from configuration)
4. `$DSH_HOME/skills`
5. `$DSH_AGENTS_HOME/skills` (default `~/.agents/skills`)

`Workspace.spec.skills.path` should default to `/workspace/.agents/skills` so
discovery is implicit; `customSkillDirs` is the escape hatch when a path must sit
outside the workspace. Note that `examples/task.yaml` currently uses the absolute
`/.agents/skills`, which would write to the filesystem root rather than the
workspace — worth correcting regardless of harness.

### 5.5 Model providers

`llm-pi-ai` is mounted **dormant** in the base composition: zero routes, and no
extra models, until a settings section supplies provider profiles. This is where
the "free and additional models" goal lands, and it needs no AX code beyond
writing the file.

```yaml
# $DSH_HOME/settings.yaml
llm-pi-ai:
  providers:
    deepseek:
      apiKeyEnv: DEEPSEEK_API_KEY
      baseURL: https://api.deepseek.com/v1
      api: openai-completions
      models:
        - id: deepseek-chat
          name: DeepSeek Chat
    local:
      apiKeyEnv: LOCAL_API_KEY        # any non-empty value
      baseURL: http://vllm.ax-system.svc.cluster.local:8000/v1
      api: openai-completions
      models:
        - id: Qwen/Qwen3-Coder-30B-A3B-Instruct
          name: Qwen3 Coder (local)
```

Credentials resolve per request from the inherited environment, the managed
`$DSH_HOME/.credentials.yaml`, or project/user `.env` fallbacks, so rotating a
key is an environment change rather than a config change.

### 5.6 Single isolation layer

Keeping exactly one boundary — Agent Substrate's — is implemented by **one
environment variable**, because the shipped host composition already threads the
mode through consistently (`dsh-base/cordis.patch.yml`):

```yaml
- id: sandbox-policy
  config:
    mode: !!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'
- id: approval
  config:
    policy: !!js (process.env.DSH_PERMISSION_MODE ?? 'workspace-write') === 'danger-full-access' ? 'never' : 'ask'
```

Setting `DSH_PERMISSION_MODE=danger-full-access` does two things at once:

1. **Sandbox off.** `dsh-fs-sandbox` documents that `danger-full-access`
   "delegates unfenced", and `dsh-sandbox-local` only builds confinement grants
   for `workspace-write`. DSH performs no confinement of its own.
2. **Approval off.** The approval policy becomes `never`, so no tool call blocks
   waiting for a human who does not exist inside a sandbox.

Substrate remains the sole boundary and the **tool catalog is unchanged** — no
plugin swapping, so prompt-cache stability is preserved. This is strictly better
than disabling the sandbox rows by `id` in the patch, which would change the
composition and require maintaining a fork of the host layer.

Two details matter. The effective default is `workspace-write` (the composition's
own fallback), not the plugin schema's `read-only`; and a DSH harness that omits
this variable still gets an agent that can write inside `/workspace` but will
stop on approval prompts. `danger-full-access` is the deliberate AX choice and
should be set by the harness implementation, not left to the user.

## 6. Phasing

| Phase | Change | Touches | Value |
|---|---|---|---|
| **0** | Run DSH through `spec.command` with a custom image and `debug: true` | None (manifest only) | Validates the whole premise end to end today, before any code change. |
| **1** | OpenAI-compatible provider, `baseURL` in `ModelSpec`, remove the silent fallback, validate providers at apply time | `internal/model`, `pkg/apis/v1alpha1/ax.proto`, `internal/server` | Any non-Gemini model, including free/local ones, usable by the platform. |
| **2** | `AX_MODEL_YAML` + `Model`-derived credential injection in the controller | `internal/controller`, `internal/metadata`, `docs/runner.md` | Removes the last Gemini-specific literal from the control plane. |
| **3** | `AgentHarness` in the proto, harness dispatcher, `deepseek-harness` implementation, DSH image, `DSH_PERMISSION_MODE` decision | `ax.proto`, `internal/workspace`, `Dockerfile.task-runner` (or a sibling) | Declarative harness choice; closes `docs/roadmap.md:32`. |
| **4** | Injection codegen: `runtime.patch.yml` from `Workspace.spec.mcp`, skill materialization into `/workspace/.agents/skills`, `AGENTS.md`, `settings.yaml`, shipped `agent-presets/` | `internal/workspace`, harness implementation | Makes MCP, skills and subagents actually reach the agent (§5). |
| **5** | Docs: `DESIGN.md` component table gains the harness contract and the single-isolation invariant; `docs/custom-runners.md`-style guide for DSH | `DESIGN.md`, `docs/` | Makes the design honest about where the agent comes from. |

Phase 0 detail — the manifest that should work against an unmodified AX:

```yaml
apiVersion: ax.io/v1alpha1
kind: Task
metadata:
  name: dsh-demo
spec:
  image: "ghcr.io/<org>/ax-dsh-runner@sha256:..."
  command: ["dsh", "--profile", "headless", "Set up this workspace and report what you did."]
  env:
    # Plain name/value only: task env has no secret reference today (see below).
    # In Phase 0 the key comes from the harness's own credential source.
    - name: DEEPSEEK_PROVIDER_ID
      value: "deepseek"
  workspaces:
    - name: default-workspace
      path: "/workspace"
  debug: true
```

Two caveats that Phase 0 must confront, both fixed properly in Phase 2/3:

- `spec.env` is a plain `name`/`value` pair (`ax.proto:96-99`); there is **no
  `valueFrom`/secret reference** for task env. The key therefore has to arrive
  via the harness's own `Model`-derived injection (Phase 2) rather than a
  `valueFrom` in the manifest.
- Leaving a `goal` on the workspace binding will still trigger the Antigravity
  bootstrap path if `GEMINI_API_KEY` happens to be set in the atespace. Omit the
  goal in Phase 0, or accept that both harnesses run.

## 7. Proposed `DESIGN.md` additions

`DESIGN.md` should gain, at minimum:

1. A fifth component row for the **agent harness** contract: "A replaceable,
   declaratively selected program that turns a workspace goal into a prepared
   workspace and a running agent. `ax-task-runner` is the default host; the
   harness is named per `Workspace`."
2. A sentence in the `Model` API section stating the intended consumption path:
   the control plane resolves credentials from `Model` and injects them into the
   task container; in-sandbox harnesses read the model binding at runtime.
3. A note that provider and model identifiers are free-form strings validated
   against a provider registry, so adding a provider is an adapter, not a schema
   change.
4. A statement of the isolation invariant: **Agent Substrate is the only
   isolation boundary.** Harnesses are configured not to add a second one, so the
   trust model has exactly one place to audit.

## 8. Risks and open questions

- **Single isolation layer (decided).** Implemented by
  `DSH_PERMISSION_MODE=danger-full-access` (§5.6). The residual risk is
  asymmetric: a tool DSH would have confined now relies entirely on Substrate.
  Verify that Substrate's filesystem boundary is at least as strong as DSH's
  `workspace-write`, and add a test asserting the agent cannot write outside
  `/workspace`.
- **No answerer for user questions.** With approval set to `never`, DSH will not
  block — but `dsh-tool-ask-user` is part of the `standard` preset and will still
  surface questions nobody can answer. Decide whether the AX preset keeps that
  row or drops it.
- **MCP and skills codegen is new work.** §5.2 and §5.4 are not extensions of
  existing behavior: AX declares `mcp` and `skills` but the runner ignores both
  (§2.4). Budget the translation layer, including registry resolution, as a real
  deliverable rather than a configuration detail.
- **`DSH_HOME` sizing and durability.** Session JSONL plus the SQLite query
  index grow with conversation length and live on the durable volume. Needs a
  retention/size policy, and it must not live under `/workspace` (which is the
  agent's own file surface).
- **Token accounting.** `TaskStatus.UsageStats` (`ax.proto:138-141`) has
  prompt/completion counters but the runner does not populate them. DSH
  telemetry is the natural source; wiring it is what makes budget guardrails
  (`docs/roadmap.md:42`) possible.
- **Image growth.** A Node-based DSH image is meaningfully larger than the
  current Python image. Consider a separate `ax-task-runner-dsh` image rather
  than replacing `Dockerfile.task-runner`, so Antigravity users are unaffected.
- **Documentation drift.** `docs/manifests.md:130` already promises Anthropic.
  Either implement it (Phase 1) or remove it; leaving it is a support burden.
- **Key rotation.** `Model` is described as making rotation "one `ax apply`"
  (`docs/concepts.md:43`). That is only true once Phase 2 lands; today the
  controller reads a literal secret name.

## 9. Summary

AX does not need to be re-architected. It needs two seams made explicit:
a **provider registry** in `internal/model` (plus `baseURL` in `ModelSpec` and
the removal of the silent fallback), and an **agent harness** contract that
makes Antigravity and DeepSeek Harness interchangeable through the `Workspace`
manifest. Everything else — the Redis-backed control plane, the runner
contract, the egress allowlist, the durable `/workspace` volume — already
supports the goal. DSH's `--profile headless` mode and provider-neutral `dsh-llm`
seam are the two features that make it fit without inventing new machinery.

Injection follows the same shape: the runner **generates** DSH configuration from
resources AX already stores — a `runtime.patch.yml` for MCP servers, an
`agent-presets/` directory for subagents, `settings.yaml` for model providers,
`AGENTS.md` and `.agents/skills/` in the workspace for instructions and skills —
and passes it on the command line. Single-layer isolation is one environment
variable, not a fork.
