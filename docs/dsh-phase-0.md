# Running DeepSeek Harness on AX today (Phase 0)

This is the zero-code recipe: it proves DeepSeek Harness (DSH) runs inside an AX
sandbox on an **unmodified** AX deployment. Nothing here depends on the agent
harness seam — that lands in Phase 3 and makes this configuration declarative.

## What this proves, and what it does not

Proves: DSH's one-shot headless mode fits AX's supervised-command contract, and
AX's sandbox, workspace, egress, and `ax ssh` plumbing already support a
non-Antigravity agent.

Does not provide (yet): harness selection in the manifest, credentials derived
from a `Model` resource, a pre-baked DSH profile, or MCP/skills injection.

## Prerequisites

- A cluster with Agent Substrate and a working AX control plane
  (`ax-server`, `ax-controller`, Redis)
- `kubectl` access and the `ax` CLI (`make build`)
- Docker (or another OCI builder) to build the runner image
- A model endpoint and its key; the default gateway allows egress on `*:443`

## Step 1: build the DSH runner image

The image is the only Phase 0 artifact. It needs the task runner plus the `dsh`
CLI — nothing else:

```dockerfile
# Dockerfile.dsh-phase0
FROM node:22-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      git curl ca-certificates openssh-client procps bash \
    && rm -rf /var/lib/apt/lists/*
# Pin the CLI version; profiles are installed at first boot in this recipe.
# Both names are needed: npm drops @deepseek-ai/dsh-sandbox-local on a peer
# conflict and dsh does not boot without it. Keep the two versions in step.
RUN npm install -g @deepseek-ai/dsh@0.1.5-rc.3 @deepseek-ai/dsh-sandbox-local@0.1.5-rc.3
ENV DSH_HOME=/ax/dsh
COPY bin/linux_amd64/ax-task-runner /usr/local/bin/ax-task-runner
ENTRYPOINT ["/usr/local/bin/ax-task-runner"]
```

```bash
make build-task-runner          # cross-compiles bin/linux_amd64/ax-task-runner
docker build --platform linux/amd64 -f Dockerfile.dsh-phase0 -t <registry>/ax-dsh-runner:phase0 .
docker push <registry>/ax-dsh-runner:phase0
```

This throwaway image installs the profile at first boot, so that boot needs npm
registry access. The shipped harness image (Phase 3) pre-bakes the profile at
build time instead, so first boot needs no egress.

## Step 2: apply the manifest

```bash
ax apply -f examples/task-dsh-demo.yaml
```

Edit the image reference (`spec.image`) and the provider credentials first.
`spec.command` is the whole integration: argv after the entrypoint becomes the
supervised child process, and `dsh --profile headless "<prompt>"` answers one
task, streams reasoning to stderr, prints the final assistant message on stdout,
and exits.

## Step 3: observe it

```bash
ax get task dsh-demo                     # phase reaches Running, then Succeeded
ax describe task dsh-demo                # conditions and exit code
ax ssh dsh-demo -- ps aux | grep dsh     # requires spec.debug: true
ax ssh dsh-demo -- ls -la /workspace
```

`spec.debug: true` is what serves the guest services behind `ax ssh`; it is off
by default.

## Caveats

- **Double bootstrap.** A workspace `goal` triggers the Antigravity bootstrap,
  which then runs *alongside* DSH when `GEMINI_API_KEY` is present. The recipe
  therefore binds a workspace **without** a `goal` and passes the prompt on the
  command line. Phase 3 makes the harness declarative and removes the
  ambiguity.
- **Credentials are literal.** Task env accepts plain `name`/`value` pairs only
  (no `valueFrom`), so the provider key sits in the Task manifest here. Phase 2
  derives it from the `Model` resource's `secretKey` instead.
- **Egress.** The default gateway allows `*:443`. On a hardened deployment,
  allowlist the model endpoint (`api.deepseek.com:443`, or an in-cluster
  `vllm.default.svc.cluster.local:8000`) on the task's `Gateway`, otherwise the
  provider call is denied and DSH exits with an error that looks like a hung
  agent.
- **Profile boot cost.** Installing the profile at first boot adds minutes and
  needs registry access; pre-bake it for anything beyond a spike.
- **Single isolation layer.** This recipe does not touch DSH's own sandbox and
  approval settings. Phase 3 sets `DSH_PERMISSION_MODE=danger-full-access` so
  Agent Substrate stays the only isolation boundary.

## See also

- `examples/task-dsh-demo.yaml` — the manifest
- `docs/deepseek-harness-adaptation.md` — the full adaptation plan
- `docs/runner.md` — the task container contract
