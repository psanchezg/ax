# DeepSeek Harness

[DeepSeek Harness](https://github.com/deepseek-ai/dsh) (`dsh`) is a coding agent
that AX can run inside the sandbox instead of Antigravity. It is the second
built-in [agent harness](../DESIGN.md#agent-harnesses), selected per workspace
with `spec.harness`, and it is what makes non-Gemini model providers usable
end to end: the harness writes its own provider configuration from the `Model`
the control plane gives it.

A workspace picks it like this (runnable copy in
[`examples/workspace-dsh.yaml`](../examples/workspace-dsh.yaml)):

```yaml
apiVersion: ax.io/v1alpha1
kind: Workspace
metadata:
  name: dsh-ws
  atespace: default
spec:
  harness:
    kind: deepseek-harness
    image: "ghcr.io/<org>/ax-dsh-runner@sha256:..."
    modelRef: deepseek          # the Model holding provider, endpoint, and secret reference
    env:
      - name: DSH_LOG_LEVEL
        value: info
  git:
    - name: origin
      repo: "https://github.com/octocat/Hello-World.git"
      branch: "master"
      depth: 1
```

A task that binds the workspace supplies only a goal; the harness provides the
command that answers it:

```yaml
apiVersion: ax.io/v1alpha1
kind: Task
metadata:
  name: dsh-goal
  atespace: default
spec:
  workspaces:
    - name: dsh-ws
      goal: "Describe this repository and report what you found."
  debug: true
```

## Build the runner image

DSH needs Node; Antigravity needs Python. Rather than growing one image, AX
ships a second one, so an Antigravity-based task is unaffected:

```bash
make build-task-runner-dsh                                    # host architecture, :latest
make build-task-runner-dsh TASK_RUNNER_GOARCH=arm64 DSH_TASK_RUNNER_TAG=v1
make push-task-runner-dsh DSH_TASK_RUNNER_REPO=<registry>/ax-dsh-runner DSH_TASK_RUNNER_TAG=v1
```

`make build-task-runner-dsh` cross-compiles `ax-task-runner` for
`TASK_RUNNER_GOARCH` (default: the host's `go env GOARCH`) and builds
`Dockerfile.task-runner-dsh` for the same platform, tagged
`DSH_TASK_RUNNER_TAG` (default `latest`). That matters the moment the cluster's
nodes are not amd64 — on an Apple Silicon Mac, kind nodes are arm64, and an amd64
image there either fails to start or needs emulation. The `Dockerfile` selects the
matching build with `COPY bin/linux_${TARGETARCH}/` and then checks the copied
binary against the image platform, so a mismatch fails the build with
`ax-task-runner is 3e00 but this image is aarch64` instead of producing an image
that dies at exec time. Build it through the Makefile target, or pass
`--platform` and `--build-arg TARGETARCH` yourself.

`Dockerfile.task-runner-dsh` installs `@deepseek-ai/dsh` at the version pinned by
its `DSH_VERSION` build argument and copies the same task runner. The image runs
DSH's **shipped** `headless` profile: it initializes itself from templates inside
the installed package on first use, so first boot needs no registry egress and
there is no profile directory in this repository to keep in sync with the CLI
version.

The install is a **global** one (`npm install --global --prefix /opt/dsh`), and
that is what makes the tree bootable:

> A project-style install does not produce a runnable tree. npm hoists the CLI's
> plugins to the project root and nests a second, identical copy of every one of
> them under `dsh-base`, so the plugin loader imports each plugin twice and dies
> at boot with `Duplicate type name 'DSH_STARTUPINFOW'`; a global install instead
> hoists the bundle flat into the CLI's own `node_modules` — the layout of a
> working install, such as the Homebrew package on macOS — and duplicates nothing.
> npm then drops exactly one plugin, `@deepseek-ai/dsh-sandbox-local`, on a peer
> conflict (`plugin(s) failed to load: @deepseek-ai/dsh-sandbox-local`), so the
> Dockerfile names it explicitly. Both failures appear only at boot inside a task,
> so the build asserts instead: no `@deepseek-ai` package may appear twice under
> the prefix, and every `@deepseek-ai/dsh-*` package must sit at `DSH_VERSION`.
>
> Do not simply bump `DSH_VERSION`: `0.1.7-rc.2` boots, but it **ignores** the
> `llm-pi-ai` provider section, so the request goes to DSH's built-in route instead
> of the `Model` AX resolved. `0.1.5-rc.3` honors it (see the protocol note below).

## What the harness configures

The control plane resolves the workspace's `modelRef` and injects the credential
under the name the `Model` declares, the endpoint as `AX_MODEL_BASE_URL`, and
the resource itself as `AX_MODEL_YAML`. From that, the harness writes exactly one
file — `$DSH_HOME/settings.yaml` — with two sections:

```yaml
agent-default-model:
  provider: deepseek              # the route below
  model: deepseek-chat
llm-pi-ai:
  providers:
    deepseek:                     # named after the Model resource
      api: openai-completions
      apiKeyEnv: DEEPSEEK_API_KEY # a credential reference, never the value
      baseURL: https://api.deepseek.com/v1
      models:
        - id: deepseek-chat
          name: deepseek-chat
```

Both are required, and the second one is easy to miss: the shipped profile mounts
its own DeepSeek adapter as the default selection, so registering a provider route
is not enough — a fresh agent would ask for that built-in route and never reach
the `Model` AX resolved. `agent-default-model` is what points the agent at the
route above. (This is not theoretical: the first version of this harness wrote only
the provider section, and DSH answered `MISSING_CREDENTIAL` for its own route.)

`apiKeyEnv` is resolved by DSH from the container environment per request, which
is why AX injects the secret value under that name rather than a fixed
`GEMINI_API_KEY`. Rotating the key is a Kubernetes secret update; the next task
provisioning picks it up.

| `Model.spec.api` (or the provider default) | `api` written for DSH | Endpoint |
|---|---|---|
| `openai-completions` (default for `openai`) | `openai-completions` | `spec.baseURL`, or `https://api.openai.com/v1` |
| `openai-responses` | `openai-responses` | `spec.baseURL` |
| `anthropic-messages` (default for `anthropic`) | `anthropic-messages` | `spec.baseURL`, or `https://api.anthropic.com` |
| `google-generate-content` (default for `google`) | `openai-completions` | Gemini's OpenAI-compatible surface, or `spec.baseURL` |

The protocol comes from the `Model`, not from the harness: a workspace can drive DSH
with a Responses-only endpoint, or with a gateway that speaks Messages in front of
another vendor, by declaring `spec.api` (see
[Manifests](manifests.md#wire-protocol-api)). Gemini has no native pi-ai route, so
it goes through its OpenAI-compatible surface.

`spec.baseURL` is passed to DSH unchanged, and each protocol appends its own path
to it, which fixes what the value has to be:

| `api` | Request path DSH builds | So `spec.baseURL` is |
|---|---|---|
| `openai-completions` | `<baseURL>/chat/completions` | the versioned root, e.g. `https://api.deepseek.com/v1` |
| `openai-responses` | `<baseURL>/responses` | the versioned root, e.g. `https://api.openai.com/v1` |
| `anthropic-messages` | `<baseURL>/v1/messages?beta=true` | the host root, e.g. `https://api.anthropic.com` |

The last row is the Anthropic SDK's convention, not a quirk of the harness: a
`baseURL` that already ends in `/v1` produces `/v1/v1/messages`.

Nothing else is generated. MCP servers, skills, `AGENTS.md`, `agent-presets/`,
and profile patches are **not** materialized yet: that is the manifest-driven
"Dynamic Agentic Environment Curation" roadmap item, and until it lands the
harness ships no project files beyond the provider binding.

## Environment contract

| Variable | Value |
|---|---|
| `DSH_HOME` | `/ax/dsh`, on the durable AX volume, outside `/workspace` |
| `DSH_PERMISSION_MODE` | `danger-full-access` — set by the harness, **not** overridable through `spec.harness.env` |
| The name in `Model.spec.secretKey.key` | the secret value, injected by the controller |
| `AX_MODEL_BASE_URL`, `AX_MODEL_YAML` | the endpoint and the bound `Model` |
| `spec.harness.env` entries | merged into the process environment |

`spec.harness.command`, when set, replaces the arguments after `dsh`; otherwise
the harness runs:

```bash
dsh --profile headless "<systemInstructions + goal>"
```

and a task that sets its own `spec.command` always wins. `systemInstructions` is
prepended to the goal prompt rather than materialized as a project file.

## Why it disables DSH's own sandbox

Agent Substrate is the single isolation boundary for an AX task
([DESIGN.md](../DESIGN.md#isolation)). DSH has its own filesystem/bash sandbox and
approval layer, both driven by `DSH_PERMISSION_MODE`; `danger-full-access` turns
them off and switches approvals to `never`. The alternative — leaving DSH's
sandbox at its own default — would layer a second boundary AX cannot configure
under the one that actually enforces isolation, and would leave the agent blocked
on approval prompts that nobody in a sandbox can answer. The harness sets the
mode itself so a manifest cannot weaken it.

## Known limitations

- **`dsh-tool-ask-user` stays enabled.** The shipped preset is untouched, so the
  agent may emit a question; with approvals at `never` it is never answered and
  never blocks, but a goal can end with a question instead of work. Trimming the
  tool is a one-line profile patch, which belongs with the curation work above.
- **No session retention policy.** Sessions, telemetry, and caches accumulate
  under `/ax/dsh`. They survive suspend/resume, which is the point, but nothing
  prunes them yet; size the durable volume accordingly.
- **Egress is still opt-in.** A hardened deployment must allowlist the model
  endpoint on the task's `Gateway` (`api.deepseek.com:443`, or an in-cluster
  `vllm.default.svc.cluster.local:8000`). A denied call looks like a hung agent
  from the outside, so check the gateway first when a task produces no output.
- **`systemInstructions` is a prompt prefix**, not a project-level
  `AGENTS.md`.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `unsupported harness kind "..."` and the workspace never becomes Ready | `spec.harness.kind` is misspelled or not implemented | Use `antigravity` or `deepseek-harness` |
| `provider "..." has no DeepSeek Harness mapping` | A `Model` whose provider has no DSH adapter | Use `google`, `openai`, or `anthropic` |
| `UnresolvedModelRef` | `modelRef` names a `Model` that does not exist in the atespace | `ax get models`, then fix the reference |
| `UnresolvedModelCredential` | The `Model`'s `secretKey` cannot be read from the Kubernetes secret | Check the secret name, namespace, and key |
| DSH reports `MISSING_CREDENTIAL` | The `apiKeyEnv` name has no value in the container environment | Check the `Model`'s `secretKey.key` matches the variable the model expects |
| Task runs but produces no output | Egress denied, or the goal finished with a question | Allowlist the endpoint on the `Gateway`; inspect with `ax ssh <task>` |

## See also

- [Running DeepSeek Harness today](dsh-phase-0.md) — the zero-code recipe, useful
  before any of this existed and still the fastest way to sanity-check a provider
- [Runners](runner.md) — the task container contract the harness runs under
- [Manifests](manifests.md) — `Model` and `Workspace` field reference
