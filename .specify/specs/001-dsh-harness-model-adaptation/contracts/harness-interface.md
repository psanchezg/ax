# Contract: Agent Harness Interface (`internal/workspace`)

**Date**: 2026-09-24 | **Consumers**: `runner.Run` → `workspace.SetupWorkspace` |
**Decisions**: research.md R2, R11–R14, R18

The harness is "a replaceable, declaratively selected program that turns a workspace goal
into a prepared workspace and a running agent" (DESIGN.md addition, FR-022).

## Go interface

```go
// internal/workspace/harness.go
package workspace

// Harness prepares a workspace and configures/launches the in-sandbox agent.
type Harness interface {
    // Kind is the registry key ("antigravity", "deepseek-harness").
    Kind() string
    // Setup receives the workspace goal and the bound harness spec. It prepares
    // the workspace for this harness and reports whether the harness's own
    // bootstrap ran (Antigravity's goal bootstrap), so /readyz?check=workspace
    // keeps its current semantics (FR-016). An error fails the workspace.
    Setup(ctx context.Context, spec *v1alpha1.AgentHarness, goal, workspacePath string) (bool, error)
}

// Resolve maps spec.Kind to an implementation. Empty ≡ "antigravity".
// Unknown kinds return *UnsupportedHarnessError — never nil, never silent (FR-013).
func Resolve(kind string) (Harness, error)
```

## Resolution rules (fail closed, FR-013)

| `spec.kind` | Behavior |
|---|---|
| `""` / unset | `antigravity` — byte-compatible with today (SC-005) |
| `"antigravity"` | `antigravity` — explicit, identical semantics |
| `"deepseek-harness"` | `deepseek-harness` implementation |
| anything else | Typed `*UnsupportedHarnessError`; workspace reports **not-Ready** with condition reason `UnsupportedHarness` naming the kind. Setup is never skipped silently. |

## `antigravity` implementation (FR-014, SC-009)

Moves `runBootstrap` behind the interface with identical semantics:

| Property | Value (unchanged) |
|---|---|
| Script | `/usr/local/bin/antigravity_bootstrap.py` via `python3` |
| Args | `--goal <goal> --workspace <path> --data-dir /ax/antigravity` |
| Credential env | `GEMINI_API_KEY` |
| Data dir | `/ax/antigravity` (durable, non-workspace) |

Existing bootstrap tests pass without modification.

## `deepseek-harness` implementation (FR-015, FR-018, FR-019)

Setup sequence:

1. **Model binding resolution** — read the bound `Model` from `harness.modelRef`
   (resolved at controller level; carried in `AX_MODEL_YAML`, see
   [container-env-contract.md](./container-env-contract.md)). Unresolvable `modelRef` ⇒
   typed error ⇒ not-Ready condition `UnresolvedModelRef`.
2. **Generate `$DSH_HOME/settings.yaml`** — the ONLY generated artifact in scope
   (FR-018), the minimal `llm-pi-ai` provider binding derived from the `Model`:

   ```yaml
   llm-pi-ai:
     providers:
       <model.metadata.name>:
         apiKeyEnv: <spec.secretKey.key>
         baseURL: <spec.baseURL>
         api: <mapped>          # openai → openai-completions
                                # anthropic → anthropic-messages
                                # google → openai-completions (R18, Gemini OpenAI-compat
                                #   base https://generativelanguage.googleapis.com/v1beta/openai/)
         models:
           - id: <spec.model>
             name: <spec.model>
   ```

   No `runtime.patch.yml`, no `agent-presets/`, no `AGENTS.md`, no skills materialization
   (Phase 4).
3. **Environment contract** — process env carries:
   | Variable | Value |
   |---|---|
   | `DSH_HOME` | `/ax/dsh` (durable, non-workspace — R2) |
   | `DSH_PERMISSION_MODE` | `danger-full-access` — set by the implementation, never defaulted (R14) |
   | `<spec.secretKey.key>` | secret value, inherited from the container env (FR-008) |
   | plus `harness.env` entries | merged, never overriding the two `DSH_*` variables above |
4. **Invocation** — as the supervised child process (`spec.command` semantics):

   ```bash
   dsh --profile headless "<goal>"
   ```

   `harness.command`, when set, replaces argv after `dsh`; `harness.systemInstructions`,
   when set, is prepended to the goal prompt (documented limitation, R12). The command
   is only used when the task spec sets none, so an explicit task command always wins.
   The image installs the DSH CLI globally and runs the **shipped** `headless` profile,
   which auto-initializes from the package's local templates, so first boot needs no npm
   registry egress (FR-019, SC-008, research.md R13).
5. **Preset policy (R1)** — the shipped `standard` preset runs untouched;
   `dsh-tool-ask-user` questions surface unanswered but never block (approval `never`).
   Documented in `docs/deepseek-harness.md`.

## Invariants (all harness kinds)

- **Goal → prompt**: the harness receives the workspace goal and is responsible for
  preparing the workspace; `/readyz?check=workspace` semantics are identical across
  kinds (FR-016).
- **Single isolation boundary**: the agent must not be able to write outside
  `/workspace`; asserted by test (SC-007). `deepseek-harness` relies on Substrate alone
  (R14).
- **Durable state outside `/workspace`**: `/ax/<kind>` holds harness state; `/workspace`
  remains the agent's only file surface.
- **Loud failures**: every setup failure is a typed error surfaced as a condition; there
  is no silent degradation path (FR-013, Constitution V).

## Test matrix (Constitution III)

- Resolution table: empty / explicit antigravity / deepseek-harness / unknown (typed
  error + condition reason) .
- `antigravity`: existing bootstrap tests unchanged (SC-009).
- `deepseek-harness`: settings.yaml content from a bound `Model` (golden-file per
  provider mapping incl. `google` → `openai-completions`, R18); env contract assertions
  (`DSH_HOME`, `DSH_PERMISSION_MODE`, credential var); argv construction (default and
  `command` override); `modelRef` unresolvable → typed error.
- Isolation assertion (SC-007): agent write attempts outside `/workspace` fail at the
  Substrate boundary.
