# AX Design

## Architecture

Storing millions of short-lived tasks as Kubernetes CRDs pushes etcd past its comfort zone (single-digit GB storage limits, write-rate bottlenecks, control plane degradation). AX keeps its state in Redis and uses Redis Streams as the work queue between the API server and a horizontally scaled pool of controllers.

```
                      ax apply -f task.yaml
                                │
                                ▼
                            ax-server
                      (gRPC API + /healthz)
                                │
                     store & publish event
                                │
                                ▼
                              Redis
               (Task Hashes + Event Streams + PubSub)
                                │
                      XREADGROUP (Streams)
                                │
                                ▼
                          ax-controller
                   (Horizontally Scaled Workers)
                                │
                        gRPC (Control API)
                                │
                                ▼
                         Agent Substrate
                ┌───────────────────────────────┐
                │ • Atespace Provisioning       │
                │ • Actor Creation & Activation │
                │ • Worker Assignment           │
                │ • Egress Policy Filtering     │
                └───────────────────────────────┘
```

## Components

| Binary | Role |
|---|---|
| `ax` | Developer CLI. Applies manifests, inspects and watches resources, tunnels to the cluster. |
| `ax-server` | Stateless gRPC API on port 8080. Validates manifests, persists to Redis, publishes events. |
| `ax-controller` | Reconciliation workers. Consume the Redis stream, provision atespaces and actors on Agent Substrate, apply egress policy, and drive tasks toward desired state. Scale by adding replicas. |
| `ax-task-runner` | Entrypoint inside every task container. Bootstraps the workspace, serves metadata, and runs the agent command. A thin wrapper over the `runner` package, which custom images can embed directly. |

## Agent harnesses

An agent harness is a replaceable, declaratively selected program that turns a workspace goal into a prepared workspace and a running agent. The `Workspace` picks one through `spec.harness`: `kind` names the integration, `image` supplies the runtime it needs, `modelRef` names the `Model` whose credential and endpoint it uses, `env` extends its environment, and `systemInstructions` overrides its persona. An unset kind keeps the default (Antigravity), and an unknown kind fails the workspace with an `UnsupportedHarness` condition rather than silently running nothing.

Antigravity and DeepSeek Harness are the two implementations today. They differ in runtime (Python versus Node) and in how they are driven — Antigravity is a goal-driven bootstrap, DeepSeek Harness is the one-shot `dsh` process — but both are selected by the same manifest field, both prepare their workspace before the task command starts, and both keep their own state on the durable AX volume so `/workspace` stays the agent's only file surface.

### Models on the runtime path

A `Model` is a model configuration, and the control plane is what puts it to work: when a task's workspace references one, `ax-controller` resolves its credential from the Kubernetes secret and injects the secret value under the name the `Model` declares, plus the endpoint and the resource itself (`AX_MODEL_YAML`, also served at `/metadata/v1alpha1/ax/model`). A harness reads that binding at runtime and configures its own provider — DeepSeek Harness writes the equivalent `$DSH_HOME/settings.yaml` — so adding a provider or rotating a key never requires touching the agent image. Rotating a key is one Kubernetes secret update; the next task picks it up.

### Providers

Provider identifiers are validated against a registry (`google`, `openai`, `anthropic`) at `ax apply` time, so a typo fails immediately and names the valid values. A provider is an adapter over one wire format — not a schema change — and the `openai` adapter covers every OpenAI-compatible endpoint, from hosted gateways to a local vLLM. Failures are loud: a missing key, an HTTP error, or an unreachable endpoint surfaces as an error carrying the provider's status and body, never as a fabricated completion.

### Isolation

Agent Substrate is the only isolation boundary. AX does not add a second one, and it deliberately switches off the sandbox and approval layers of the harnesses it runs: a sandbox that only AX can configure, layered under the one that actually enforces the boundary, would just leave the agent blocked on approvals nobody can answer. `DSH_PERMISSION_MODE=danger-full-access` is therefore set by the harness itself and cannot be weakened from a manifest.

## API reference

The control plane exposes the `ax.v1alpha1.AX` gRPC service. Health checks are plain HTTP: `GET /healthz` on the same port returns `200 OK`.

**Tasks**

| RPC | Description |
|---|---|
| `GetTask` | Get a task by atespace and name. |
| `ListTasks` | List tasks in an atespace, with pagination. |
| `UpdateTask` | Create or update a task. |
| `DeleteTask` | Delete a task. |
| `SuspendTask` | Checkpoint actor state and pause the task. |
| `ResumeTask` | Resume a suspended task. |
| `WatchTask` | Server-streaming RPC that emits status and condition transitions as they happen. |

**Gateways**

| RPC | Description |
|---|---|
| `GetGateway` | Get a gateway by atespace and name. |
| `ListGateways` | List gateways in an atespace. |
| `UpdateGateway` | Create or update a gateway. |
| `DeleteGateway` | Delete a gateway. |

**Workspaces**

| RPC | Description |
|---|---|
| `GetWorkspace` | Get a workspace by atespace and name. |
| `ListWorkspaces` | List workspaces in an atespace. |
| `UpdateWorkspace` | Create or update a workspace. |
| `DeleteWorkspace` | Delete a workspace. |

**Models**

| RPC | Description |
|---|---|
| `GetModel` | Get a model configuration by atespace and name. |
| `ListModels` | List model configurations in an atespace. |
| `UpdateModel` | Create or update a model configuration. |
| `DeleteModel` | Delete a model configuration. |

Request and response types follow the `<Method>Request` / `<Method>Response` convention. Generated Go types live in [`pkg/apis/v1alpha1`](pkg/apis/v1alpha1).
