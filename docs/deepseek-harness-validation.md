# Validation runbook: DSH + non-Gemini providers

Step-by-step guide to exercise this feature before exporting anything upstream. Every step
carries the expected result and what it proves, and the "if it fails" note points at the
right place. Tick the checkboxes as you go.

This is the record of the manual validation: what was executed, what each check proves,
and what it does not. The criteria it verifies are named in §5.

## Validation status

| Level | Status |
|---|---|
| 0 — suite and the real runner, locally | ✅ verified (macOS/arm64 and Linux/amd64) |
| 0.5 — real `dsh` against the generated `settings.yaml` | ✅ verified (this is how the `agent-default-model` bug surfaced) |
| 1 — control plane without a cluster | ✅ verified (macOS/arm64 and Linux/amd64) |
| 2 — real sandbox (Kubernetes + Substrate) | ✅ verified (macOS/arm64 and Linux/amd64; kind, gVisor) |

Four things we learned by running it, which correct earlier assumptions:

1. **`harness.image` has to be pinned by digest.** Substrate rejects an `ActorTemplate`
   whose image is only tagged (`must be pinned by digest`), and because AX then falls back
   to the default template, the error you see is a misleading `actor template not found`
   (§3.3).
2. **A `WorkerPool` has to exist**: AX does not create capacity (§3.1), and every `Task`
   that is `Running` holds one worker.
3. **The agent *can* write outside `/workspace`** inside the sandbox. Substrate isolates the
   sandbox (gVisor kernel: the actor does not escape, does not see the host or other
   actors), but it does not restrict the actor's rootfs. The isolation criterion has to be
   read as "it cannot escape the sandbox", not as "it only writes to `/workspace`" (§3.4.4).
4. **`Running` does not mean the agent is still working.** AX does not propagate the task
   command's exit status: if `dsh` dies — for example with
   `AUTH: 401 ... api key is invalid` — the `Task` stays `Running` with no process left in
   the actor. To tell whether it is still alive:
   `./bin/ax ssh <task> -- ps -eo pid,args | grep "[d]sh"`. Filed as an AX improvement, not
   as part of this feature.

## 0. Preparation

```bash
cd /path/to/ax                       # the AX checkout (on this machine: /Users/psanchezg/Documents/GitHub/ax)
git log --oneline -1                 # the tip should be the most recent docs commit
git status --short                   # clean tree
make build                           # bin/ax, bin/ax-server, bin/ax-controller
```

| Check | Expected |
|---|---|
| `go version` | 1.27+ |
| `docker info` | daemon reachable (level 2 and image builds) |
| `ko version` | installed (level 2: `make deploy`) |
| `kind version` | installed (level 2: the Substrate script uses it) |
| `dsh --help` | CLI present (level 0.5) |

### Platform notes (both platforms validated)

The commands were run on macOS on Apple Silicon and on Linux/amd64. On Linux they are the
same, except for these:

| macOS | Linux |
|---|---|
| `sed -i '' -E '...'` | `sed -i -E '...'` |
| `shasum -a 256` | `sha256sum` |
| `TASK_RUNNER_GOARCH` defaults to `arm64` | defaults to `amd64`, which is what amd64 nodes need |
| the Docker Desktop traps of §3.1 apply | they do not (`/dev/kvm`, `DOCKER_DEFAULT_PLATFORM`) |

Everything else — manifests, `ax` commands, Substrate and the sandbox checks — is identical.

### ⚠️ First of all: which cluster is kubectl pointing at

Levels 0 and 1 **do not touch Kubernetes**: level 0 runs the runner locally and level 1
talks to `ax-server` over `AX_SERVER`. Everything in level 2 — `kubectl`, `make deploy`, and
any `bin/ax` **without** `--server`, which resolves the server from the active context —
follows your kubeconfig. On this machine the active context is a **remote** cluster, so
capture the original and check where you are *before* writing anything:

```bash
export ORIG_CTX="$(kubectl config current-context)"   # to get back when you are done
echo "current context: $ORIG_CTX"
kubectl config get-contexts | sed -n '1,10p'
```

If that context is a shared or production cluster, do **not** run level 2 against it:
`make deploy` would install AX (namespace `ax-system`, read RBAC on secrets, Redis,
controller) and apply egress there. Level 2 belongs on a disposable local cluster (`kind`,
§3.1), never on the remote one. Levels 0 and 1 can be run without touching the context.

Export your values once per shell (do not commit them):

```bash
export REGISTRY=ghcr.io/<your-org>          # a registry the cluster can pull from
export ATESPACE=default
export DSH_IMAGE="${REGISTRY}/ax-dsh-runner:0.1.5-rc.3"
export DEEPSEEK_API_KEY_SRC=...             # your real key, only in the shell
```

- [X] `make build` with no errors

---

## 1. Level 0 — regression and local E2E (no infrastructure)

### 1.1 Full suite

```bash
make test
```

Expected: `ok` in all 8 packages (`internal/controller`, `internal/metadata`,
`internal/model`, `internal/server`, `internal/tunnel`, `internal/workspace`,
`pkg/apis/v1alpha1`, `runner`). It exercises the registry and adapters, apply-time
validation, credential injection, the harness dispatcher, and the untouched Antigravity
path.

If it fails: `go test -count=1 -run <Test> ./<package>/... -v`.

- [X] Suite green

### 1.2 The repository examples are still valid

```bash
go test -count=1 ./pkg/apis/... -run TestExamples -v
```

Expected: PASS for `model-deepseek.yaml`, `model-local-qwen.yaml`, `simple.yaml`,
`task-dsh-demo.yaml` (both of its documents) and `task.yaml`. It proves no example went
stale against the strict schema.

- [X] PASS

### 1.3 The real runner, locally, with the DSH harness

This exercises the whole container cycle without a cluster:

```bash
W=/tmp/ax-runbook; rm -rf "$W"; mkdir -p "$W/ws" "$W/dsh"

# The host runner (`make build` does not produce it: build-task-runner cross-compiles
# it for linux/amd64, which cannot run on macOS).
go build -o "$W/ax-task-runner" ./cmd/ax-task-runner

cat > "$W/task.yaml" <<'EOF'
apiVersion: ax.io/v1alpha1
kind: Task
metadata: {name: local-task, atespace: default}
spec:
  command: ["sh", "-c", "echo runner-ok > /tmp/ax-runbook/out.txt"]
  workspaces:
    - name: dsh-ws
      path: /tmp/ax-runbook/ws
      goal: "prepare this workspace"
  debug: true
EOF

cat > "$W/ws.yaml" <<'EOF'
apiVersion: ax.io/v1alpha1
kind: Workspace
metadata: {name: dsh-ws, atespace: default}
spec:
  harness: {kind: deepseek-harness, modelRef: deepseek}
EOF

cat > "$W/model.yaml" <<'EOF'
apiVersion: ax.io/v1alpha1
kind: Model
metadata: {name: deepseek, atespace: default}
spec:
  provider: openai
  model: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  secretKey: {name: deepseek-api-secret, key: DEEPSEEK_API_KEY}
EOF

DSH_HOME="$W/dsh" AX_MODEL_YAML="$(cat "$W/model.yaml")" \
  "$W/ax-task-runner" --task-file "$W/task.yaml" --workspace-file "$W/ws.yaml" --port 18099 \
  > "$W/runner.log" 2>&1 &
RUNNER_PID=$!
sleep 2

curl -s http://127.0.0.1:18099/readyz                        # → ok
curl -s http://127.0.0.1:18099/metadata/v1alpha1/ax/model     # → the Model, with the secret reference
cat "$W/out.txt"                                             # → runner-ok
cat "$W/dsh/settings.yaml"                                   # → the llm-pi-ai binding
kill $RUNNER_PID
```

Expected in `settings.yaml` (**two** sections; the second is what makes the agent use the
first one's route, and it is easy to forget — its absence was this validation's first
finding):

```yaml
agent-default-model:
    model: deepseek-chat
    provider: deepseek
llm-pi-ai:
    providers:
        deepseek:
            api: openai-completions
            apiKeyEnv: DEEPSEEK_API_KEY
            baseURL: https://api.deepseek.com/v1
            models:
                - id: deepseek-chat
                  name: deepseek-chat
```

Also check that the output of `/metadata/v1alpha1/ax/model` contains **no** secret value,
only `name`/`key`.

If it fails: `cat "$W/runner.log"`. An `unsupported harness kind` means the kind is
misspelled; a `provider ... has no DeepSeek Harness mapping` error comes from a provider
with no DSH adapter (`google`/`openai`/`anthropic` are the valid ones).

- [X] `/readyz` answers `ok`
- [X] `/metadata/v1alpha1/ax/model` serves the `Model` with no secret value
- [X] `settings.yaml` generated as above
- [X] The task command ran

### 1.4 The one external assumption: the real `dsh` uses *your* binding

#### 1.4a With your real key (end-to-end proof)

Outside a cluster nobody injects the credential, so **export the variable your `Model`
declares** (`secretKey.key`) before launching DSH:

```bash
export DEEPSEEK_API_KEY=...            # the name your Model declares, not another one
DSH_HOME=/tmp/ax-runbook/dsh DSH_PERMISSION_MODE=danger-full-access \
  dsh --profile headless "list the workspace files and say what you see"
```

Expected: DSH starts, resolves the credential through `apiKeyEnv`, calls the `Model`'s
`baseURL` and finishes with an answer on stdout.

> Watch the interpretation: if your `Model` points at `api.deepseek.com`, a 401 does **not**
> distinguish your route from DSH's internal one, because both use that host by default.
> This test proves what it proves: the credential arrives, the endpoint answers, and the
> error propagates without fabricating anything. For the route, use 1.4b.

#### 1.4b Without a key: check that DSH calls the `Model`'s `baseURL`

This is the test that discriminates. Replace the endpoint with a local one of yours and see
whether DSH calls there:

```bash
W=/tmp/ax-runbook

# 1) A fake endpoint that logs what it receives and answers 401.
cat > "$W/fake.py" <<'EOF'
import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('Content-Length') or 0)
        b = self.rfile.read(n)
        print("REQ", self.path, "auth=" + str(self.headers.get('Authorization')), b[:80], flush=True)
        self.send_response(401); self.end_headers()
        self.wfile.write(b'{"error":{"message":"fake server reached"}}')
    def log_message(self, *a): pass
http.server.HTTPServer(('127.0.0.1', 18999), H).serve_forever()
EOF
python3 "$W/fake.py" > "$W/fake.log" 2>&1 &
FAKE=$!
sleep 1
head -3 "$W/fake.log"          # should be empty; "Address already in use" means another process owns 18999
# If the port is taken, change 18999 to 18998 here and in the baseURL below.

# 2) Point the Model at the stub and regenerate settings.yaml with the real runner.
sed -i '' 's|https://api.deepseek.com/v1|http://127.0.0.1:18999/v1|' "$W/model.yaml"
grep baseURL "$W/model.yaml"                      # → http://127.0.0.1:18999/v1
rm -rf "$W/dsh"; mkdir -p "$W/dsh"
DSH_HOME="$W/dsh" AX_MODEL_YAML="$(cat "$W/model.yaml")" \
  "$W/ax-task-runner" --task-file "$W/task.yaml" --workspace-file "$W/ws.yaml" --port 18099 \
  > "$W/runner.log" 2>&1 &
RUNNER=$!
sleep 2; kill $RUNNER 2>/dev/null || true          # the runner already wrote settings.yaml
grep -A2 agent-default-model "$W/dsh/settings.yaml"

# 3) Launch DSH with a dummy key and see who receives the request.
DSH_HOME="$W/dsh" DEEPSEEK_API_KEY=dummy-key \
  dsh --profile headless "say hello" > "$W/dsh.log" 2>&1 &
DSH=$!
for i in $(seq 1 12); do sleep 5; grep -q "REQ" "$W/fake.log" 2>/dev/null && break; done
kill $DSH 2>/dev/null || true

echo "== requests received by the stub =="; cat "$W/fake.log"
echo "== dsh output =="; head -3 "$W/dsh.log"
kill $FAKE 2>/dev/null || true
```

Expected:

```text
== requests received by the stub ==
REQ /v1/chat/completions auth=Bearer dummy-key b'{"model":"deepseek-chat","messages":[{"role":"system",...
== dsh output ==
dsh: AUTH: 401: {"message":"fake server reached"}
```

The error comes from **your** endpoint, not from DeepSeek: that proves the `Model`'s route
is selected. If the stub receives nothing and you see
`MISSING_CREDENTIAL ... llm-deepseek: ... route "deepseek-official"`, DSH is using its
internal route: check that `settings.yaml` includes the `agent-default-model` section
pointing at the `llm-pi-ai` route (and that you regenerated it with the current runner).

Remember to revert the `model.yaml` `baseURL` afterwards:

```bash
sed -i '' 's|http://127.0.0.1:18999/v1|https://api.deepseek.com/v1|' "$W/model.yaml"
```

If it fails: `MISSING_CREDENTIAL` for **your** route → you did not export the `Model`'s
variable; an HTTP error from the provider → the key or the `baseURL`; a timeout →
endpoint/egress on your network.

- [X] 1.4a: DSH answers with a real key
- [X] 1.4b: the local stub receives the request (the `Model`'s route is selected)

---

## 2. Level 1 — control plane without a cluster

This exercises the real `ax apply` validation and persistence without running tasks. The
ports are shifted (16379/18080) so they do not collide with a Redis or an 8080 you already
have up. The `AX_SERVER` on the first line is what keeps this level out of Kubernetes:
without it, `bin/ax` would resolve the server from your active context (your remote
cluster).

```bash
docker run -d --rm --name ax-runbook-redis -p 16379:6379 redis:7-alpine
./bin/ax-server --addr :18080 --redis-addr localhost:16379 &
SERVER_PID=$!
export AX_SERVER=127.0.0.1:18080
sleep 1

# 1) Validation: a provider with a typo must be rejected at apply, not later.
#    ✅ THIS STEP PASSES WHEN IT FAILS. An "Error: ... unknown provider" here is the
#    success criterion, not a problem.
cat <<'EOF' | ./bin/ax apply -f -
apiVersion: ax.io/v1alpha1
kind: Model
metadata: {name: typo-model, atespace: default}
spec: {provider: gemini-flash, model: x}
EOF
# Expected: non-zero output, and the message names google, openai, anthropic:
#   Error: applying document 1: rpc error: code = InvalidArgument desc =
#   invalid model: unknown provider "gemini-flash" (valid providers: anthropic, google, openai)
# Note: that apply returns 1 on purpose; if your shell has `set -e`, wrap it in `|| true`
# or run step 2 on a separate line.

# 2) The real manifests must be accepted
./bin/ax apply -f examples/model-deepseek.yaml
./bin/ax apply -f examples/workspace-dsh.yaml
./bin/ax get models
./bin/ax get tasks
./bin/ax get workspaces
```

Expected (real output from this recipe):

```text
model.ax.io/deepseek created
workspace.ax.io/dsh-ws created
task.ax.io/dsh-goal created

NAME       ATESPACE   PROVIDER   MODEL
deepseek   default    openai     deepseek-chat

NAME       ATESPACE   PHASE     ACTOR    WORKER-IP   AGE
dsh-goal   default    Pending   <none>   <none>      0s

NAME     ATESPACE   GIT-REPOS   MCP-SERVERS
dsh-ws   default    1           0
```

The `Task` stays `Pending`: there is no controller or Substrate, which is normal at this
level. What has been proven is that the server **validates the provider at apply** and that
`Workspace.spec.harness` travels and is persisted correctly.

Cleanup:

```bash
kill $SERVER_PID; docker stop ax-runbook-redis
unset AX_SERVER     # or level 2 keeps dialling this dead local server (see §3.3)
```

- [X] Provider with a typo rejected, naming the valid ones
- [X] `Model`, `Workspace` and `Task` created and read back

---

## 3. Level 2 — real sandbox (Kubernetes + Agent Substrate)

The only level that proves the whole objective. **Substrate is a separate project**: AX does
not install it (`deploy/` only carries Redis, the server and the controller). Without a
Control API reachable at `api.ate-system.svc.cluster.local:443`, this level does not start.

### 3.1 Cluster and Substrate

Do not create the cluster by hand: Substrate ships the script this level needs, and it also
creates the **local registry** we later use for the AX images (kind nodes are already
configured to pull from it, with the feature gates its `podcertcontroller` needs).

```bash
# Isolate the kubeconfig so your remote cluster is untouched (§0). The script honours $KUBECONFIG.
export KUBECONFIG=/tmp/ax-test.kubeconfig
export KIND_CLUSTER_NAME=ax-test        # stops it from deleting a kind cluster you already have named "kind"

git clone https://github.com/agent-substrate/substrate.git /tmp/ax-substrate
cd /tmp/ax-substrate
hack/create-kind-cluster.sh             # cluster + localhost:5001 registry (KIND_REGISTRY_PORT)
hack/install-ate-kind.sh --deploy-ate-system

cd -                                    # back to the AX repository
kubectl config current-context          # → kind-ax-test
kubectl get svc -n ate-system           # the Control API AX expects (api.ate-system…)
kubectl get pods -n ate-system
```

Notes from the script's source: the default cluster is called `kind` (`KIND_CLUSTER_NAME`
changes that), the local registry is `kind-registry` on `127.0.0.1:5001`, `IP_FAMILY`
defaults to `ipv4`, and **when there is no `/dev/kvm` — the usual case with Docker Desktop
on macOS — it disables micro-VMs and carries on with gVisor**. Teardown is
`hack/delete-kind-cluster.sh`.

If the Control API is not called `api` or does not listen on 443, no code change is needed:
`ax-controller` accepts `--substrate-endpoint` and `--substrate-authority`.

#### ⚠️ The macOS trap that costs an hour: `DOCKER_DEFAULT_PLATFORM`

On Apple Silicon, kind uses Docker's default platform for the node image. If you have
`DOCKER_DEFAULT_PLATFORM=linux/amd64` (easy to have in `~/.zshrc` for another project), the
node is created **amd64 and emulated**, and `containerd`/`kubelet` crash-loop: the cluster
sits at `NotReady` and it looks like a bootstrap timeout.

```bash
echo "$DOCKER_DEFAULT_PLATFORM"                     # does it say linux/amd64?
docker logs ax-test-control-plane 2>&1 | grep -m1 "Detected architecture"
#   amd64 → emulated node (wrong);  arm64 → correct

# Fix it for this shell only and rebuild the cluster:
unset DOCKER_DEFAULT_PLATFORM
kind delete cluster --name ax-test
docker rmi -f kindest/node:v1.37.0
docker pull --platform linux/arm64 kindest/node:v1.37.0
hack/create-kind-cluster.sh
```

It is worth removing that `export` from `~/.zshrc` and passing it per command
(`DOCKER_DEFAULT_PLATFORM=linux/amd64 docker build …`) when you need it: otherwise it also
affects the runner image and any `docker build` in this flow.

And a warning about kind: its default `--wait` **does not wait for the control plane**, so a
broken node is reported as a successful creation. Always check with `kubectl get nodes` that
the node is `Ready` before continuing.

#### ⚠️ If the install fails pulling `gcr.io/distroless`: the `gcloud` credential helper

No Google login is needed to install Substrate on kind — the script even does
`unset GCE_REGION ... PROJECT_ID`. But if your `~/.docker/config.json` maps `gcr.io` to the
`gcloud` credential helper while gcloud has **no active account**, **ko cannot pull even a
public image** (`gcr.io/distroless/...`): the install dies inside `ko resolve -f -`, before
applying anything.

```text
WARNING: Could not open the configuration file: [~/.config/gcloud/configurations/config_default]
cache.get("gcr.io/distroless/static-debian13:latest@sha256:...") failed with error getting
credentials - err: exit status 1, out: `You do not currently have an active account selected.`
Error: error processing import paths in "-": error resolving image references: fetching base
image: ... error getting credentials
```

Diagnose it in one line — if this exits 1, the helper is the failure:

```bash
echo gcr.io | docker-credential-gcloud get      # → exit 1
```

`ko` (go-containerregistry) asks the keychain *before* the request, so a failing helper aborts
the pull instead of falling back to anonymous. The distroless base images are public and need
no credentials at all.

Two ways out:

```bash
# A) Reauthenticate gcloud once (interactive, opens the browser)
gcloud auth login

# B) Leave your global configuration alone: a DOCKER_CONFIG for this shell that drops only the
#    gcr.io credential helpers and keeps your Docker Hub / GHCR / ECR logins.
mkdir -p /tmp/docker-nogcloud
jq 'del(.credHelpers)' ~/.docker/config.json > /tmp/docker-nogcloud/config.json
export DOCKER_CONFIG=/tmp/docker-nogcloud
```

Do not shortcut B with `echo '{}'`: that also throws away the `index.docker.io` login, and
this flow pulls public images from Docker Hub too (the AX task-runner builds on
`alpine/git`, and §3.2 builds `node:22-slim`), where anonymous pulls hit rate limits.

Verified with `ko 0.19.1`: with B, ko builds against
`gcr.io/distroless/static-debian13:latest` (anonymous, no helper) and pushes to
`localhost:5001`, which asks for no credentials. Keep the variable exported for the rest of
the flow (§3.2 and the `ko apply` of AX): it is the same keychain.

Then AX. The cluster's local registry is what lets us deploy without publishing anything:

```bash
# Node architecture: on Apple Silicon kind uses arm64 nodes, so the images must be
# arm64 (both ko and docker build, with --platform).
NODE_ARCH="$(docker exec ax-test-control-plane uname -m)"
case "$NODE_ARCH" in aarch64|arm64) PLATFORM=linux/arm64 ;; *) PLATFORM=linux/amd64 ;; esac
echo "nodes $NODE_ARCH → $PLATFORM"

export AX_IMAGE_REPO=localhost:5001/ax            # the cluster registry
KO_DOCKER_REPO=$AX_IMAGE_REPO ko apply --platform=$PLATFORM -f deploy/redis.yaml
KO_DOCKER_REPO=$AX_IMAGE_REPO ko apply --platform=$PLATFORM -f deploy/ax-server.yaml
KO_DOCKER_REPO=$AX_IMAGE_REPO ko apply --platform=$PLATFORM -f deploy/ax-controller.yaml
kubectl get pods -n ax-system
```

#### Capacity: a `WorkerPool` has to be created (AX does not)

AX asks Substrate for atespaces, templates and actors, but it **does not create capacity**.
Without a `WorkerPool` the actor has nowhere to go and the task never starts. AX pins the
`SANDBOX_CLASS_GVISOR` class, so the pool has to be gVisor (the `SandboxConfig` Substrate
installs is already called `gvisor-default`), and the worker image is built with ko:

```bash
cat > /tmp/ax-pool.yaml <<'EOF'
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
  name: ax-pool
  namespace: ax-system
  labels:
    workload: ax
spec:
  replicas: 2
  workerImage: ko://github.com/agent-substrate/substrate/cmd/ateom-gvisor
  template:
    resources:
      limits:
        cpu: "2"
        memory: 4Gi
EOF

cd /tmp/ax-substrate
KO_DOCKER_REPO=localhost:5001 ko apply --platform=$PLATFORM -f /tmp/ax-pool.yaml
cd -
kubectl get workerpools -n ax-system          # DESIRED 2 / READY 2
kubectl get pods -n ax-system -l ate.dev/worker-pool=ax-pool
```

The pool's `namespace` does not restrict selection (matching is by labels);
`spec.template.resources.limits` is the **per-actor capacity**, so size it above what you
request in the task. Every `Task` that is `Running` occupies one worker, so a new task fails
with `no free workers available` while the pool is full.

- [X] `kubectl config current-context` = `kind-ax-test`
- [X] Substrate installed and its Control API visible in `ate-system`
- [X] `ax-system` pods `Running`
- [X] The images' platform matches the nodes'
- [X] `WorkerPool` with 2 replicas `READY`

### 3.2 Runner image with DSH

```bash
export DSH_IMAGE="localhost:5001/ax-dsh-runner:0.1.5-rc.3"   # the cluster's local registry

# The target compiles the runner and the image for the SAME architecture
# (TASK_RUNNER_GOARCH, host by default, which is what kind's nodes run here) and with
# the tag you give it: split $DSH_IMAGE into repo and tag, or the Makefile would publish
# :latest and you would not find the tag you use in the manifest.
unset DOCKER_DEFAULT_PLATFORM
export DOCKER_CONFIG=/tmp/docker-nogcloud      # the §3.1 config; it keeps your Docker Hub login
make push-task-runner-dsh \
  TASK_RUNNER_GOARCH="${PLATFORM#linux/}" \
  DSH_TASK_RUNNER_REPO="${DSH_IMAGE%:*}" \
  DSH_TASK_RUNNER_TAG="${DSH_IMAGE##*:}"
```

`push` publishes to the local registry (`localhost:5001`), which is where the nodes pull
from; `build-task-runner-dsh` only builds locally.

Check the architecture before applying anything:

```bash
docker image inspect "$DSH_IMAGE" --format 'image: {{.Architecture}}/{{.Os}}'
docker run --rm --entrypoint sh "$DSH_IMAGE" -c 'uname -m'     # → aarch64 on Apple Silicon, x86_64 on amd64
```

If you build again with the **same** tag, the nodes may keep the cached copy: bump the tag
(`:test2`) on repeat.

> **How the DSH boot was fixed (this validation).** The CLI has to be installed
> **globally** (`npm install -g --prefix /opt/dsh`): that way npm flattens the whole bundle
> inside the CLI's own `node_modules` and duplicates nothing. A project install nests a
> second, full copy of the closure under `dsh-base` and the loader dies with
> `Duplicate type name 'DSH_STARTUPINFOW'`. npm also drops one plugin,
> `@deepseek-ai/dsh-sandbox-local`, on a peer conflict (`plugin(s) failed to load:
> @deepseek-ai/dsh-sandbox-local`), so it is named explicitly in the install. Some ~16
> transitive packages stay duplicated (the build reports them) and they do **not** stop the
> boot. Because the failure only shows at boot inside a task, the `Dockerfile` boots the CLI
> once against a closed port and requires it to get past the plugin tree. Details in
> `docs/deepseek-harness.md`.

- [X] Image published in the cluster registry, with the right architecture

### 3.3 Test resources

```bash
# Substrate requires the DIGEST: it rejects a template whose image is only tagged
# ("must be pinned by digest"), and if the template fails AX falls back to the default
# and the error you see is a confusing "actor template not found".
DIGEST=$(docker image inspect "$DSH_IMAGE" --format '{{index .RepoDigests 0}}' 2>/dev/null | sed 's/.*@//')
[ -n "$DIGEST" ] || DIGEST=$(docker push "$DSH_IMAGE" 2>&1 | awk '/digest: sha256/{print $3; exit}')
echo "pinned image: ${DSH_IMAGE%:*}@$DIGEST"

kubectl create secret generic deepseek-api-secret \
  --from-literal=DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY_SRC"

# Edit the image in the example with the digest, not the tag. The pattern replaces any
# previous digest: otherwise you would validate the old image believing it is the new one.
sed -i '' -E "s|^([[:space:]]*image: \").*(\")$|\1${DSH_IMAGE%:*}@${DIGEST}\2|" examples/workspace-dsh.yaml
grep -n 'image:' examples/workspace-dsh.yaml    # your repo and the freshly published digest

./bin/ax apply -f examples/model-deepseek.yaml
./bin/ax apply -f examples/workspace-dsh.yaml
./bin/ax get task dsh-goal
./bin/ax describe task dsh-goal
```

Expected: the `Task` goes through `Running`. For DSH to answer the goal its call has to
leave the sandbox, and AX does not configure that: with the `Gateway` gone, an actor has no
egress unless the deployment gives it one through Substrate's own primitives (`EgressGateway`
on the actor; Substrate captures and refuses the actor's TCP when it is absent). Without it
DSH reports `TRANSPORT: Connection error.` and the goal is never answered — the sandbox
checks in §3.4 still pass, because none of them needs the provider.

> **If the first `apply` fails with `dial tcp 127.0.0.1:18080: connect: connection refused`**,
> `AX_SERVER` is still exported from level 1 (§2) and `bin/ax` is talking to the local
> `ax-server` you killed in that level's cleanup. An explicit `AX_SERVER` (or `--server`)
> always wins over the kube context, with no health check and no fallback
> (`internal/tunnel/tunnel.go`). `unset AX_SERVER`: then `bin/ax` resolves `svc/ax-server` in
> `ax-system` from the active context and keeps a background `kubectl port-forward` for you
> (state in `~/.ax/tunnels`, managed with `./bin/ax tunnel list|stop`). Check the server is
> actually up with `kubectl get pods -n ax-system` before blaming the tunnel.

#### Where the goal is answered: the worker's log

The actor is not a pod of its own: it is a guest inside a worker pod of the pool, and `ateom`
copies its stdout/stderr into that pod's log, one JSON line per output line, tagged with
`ate.actor.name`. That log is the only place where the harness output and the command's exit
are visible (`ax describe task` only shows conditions):

```bash
# Which worker runs our actor (the pool has two; only one is ours)
WORKER_IP=$(./bin/ax describe task dsh-goal | awk '$1=="Worker"{print $3}')
kubectl -n ax-system get pod -l ate.dev/worker-pool=ax-pool -o wide | grep "$WORKER_IP"

# This actor's output only. Drop -f and use --tail=-1 for a run that already ended; add
# --prefix if you prefer the pod name on every line (the label selector covers the pool).
kubectl -n ax-system logs -l ate.dev/worker-pool=ax-pool -c ateom -f \
  | jq -Rr 'fromjson? | select(.labels["ate.actor.name"]=="dsh-goal") | .message'
```

The run ends well when that stream ends with the runner's own verdict (`runner/runner.go`):

| Line in the worker's log | Meaning |
|---|---|
| `msg="started task command" pid=44 command="[dsh --profile headless …]"` | the command is running: the actor is alive, not merely scheduled |
| `msg="wrote DSH provider binding" model=deepseek provider=openai path=/ax/dsh/settings.yaml` | the harness bound the `Model` (what §3.4.1 inspects afterwards) |
| the model's answer, line by line | DSH's final message, on the container's stdout |
| `msg="task command completed successfully" pid=44` | ✅ the command exited 0 |
| `level=ERROR msg="task command exited with error" … exitCode=N` | ❌ the command failed with exit code `N` |

A `WARN msg="workspace maiden run setup completed with errors; marker omitted to allow retry"`
(a git fetch that had to retry) does not stop the run: the verdict is the `started` /
`completed` pair. And the opposite trap: **the `Task` never leaves `Running` by itself**.
`ax-task-runner` does not wire the `OnCommandExit` hook, so once the command exits it keeps
serving metadata and the actor stays alive (`runner.Run` blocks until its context is
cancelled): `ax get tasks` shows `Running` forever after a successful goal. The completion
evidence is the log line, not the phase.

- [X] `Task` reaches `Running`, and the worker's log shows
      `msg="started task command" pid=…` for the `dsh` command
- [X] The same stream ends with `msg="task command completed successfully" pid=…` carrying
      DSH's final message (`./bin/ax ssh dsh-goal -- ps aux | grep dsh` confirms it live)

### 3.4 The six checks that matter

| # | Criterion | Procedure |
|---|---|---|
| 1 | `settings.yaml` with the `Model` binding | §3.4.1 |
| 2 | credential and endpoint in the actor's environment | §3.4.2 |
| 3 | `/metadata/v1alpha1/ax/model` from inside | §3.4.3 |
| 4 | sandbox isolation | §3.4.4 |
| 5 | the no-`harness` path is untouched | §3.4.5 |
| 6 | key rotation with no changes | §3.4.6 |

Two preconditions that are not part of this feature but block any new `Task` (and therefore
checks 5 and 6):

- **A free worker.** Every `Task` in `Running` occupies one worker from the pool (§3.1) and
  the pool has two. With the pool full the new task never starts and the condition says:
  `Ready False ActorResumeFailed ... rpc error: code = ResourceExhausted desc = no free workers available`.
  Free one first: `./bin/ax delete task dsh-e2e` (or whichever you are not using). A task
  left in `Failed` does not recover with another `apply`: delete it and apply it again.
- **An image the node can pull.** `examples/simple.yaml` and `examples/task.yaml` use
  `gcr.io/ax-substrate/ate-images/...`, which is private; the actor dies while creating the
  bundle with
  `DENIED: Unauthenticated request ... artifactregistry.repositories.downloadArtifacts`.
  For a new task use the image from §3.2 (local registry) or a plain runner in your own
  registry.

#### 3.4.1 · `settings.yaml` inside the sandbox

```bash
./bin/ax ssh dsh-goal -- ls -l /ax/dsh
./bin/ax ssh dsh-goal -- cat /ax/dsh/settings.yaml
```

Expected: `settings.yaml` (plus `profiles/`, `sessions/`, `storages/`, which DSH creates on
its first boot) with the **two** sections the harness writes:
`llm-pi-ai.providers.<model>` with `api`, `apiKeyEnv`, `baseURL` and `models`, and
`agent-default-model` pointing at that provider. If the second one is missing, DSH falls
back to its internal route and the symptom is `MISSING_CREDENTIAL`.

#### 3.4.2 · Credential and endpoint in the actor's environment

```bash
./bin/ax ssh dsh-goal -- sh -c 'env | grep -E "^(DEEPSEEK_API_KEY|AX_MODEL_BASE_URL|DSH_HOME|DSH_PERMISSION_MODE)=" | sed "s/=\(.\{4\}\).*/=\1.../"'
```

Expected: the credential **under the name the `Model` declares** (`DEEPSEEK_API_KEY` here),
`AX_MODEL_BASE_URL` with the endpoint, `DSH_HOME=/ax/dsh` and
`DSH_PERMISSION_MODE=danger-full-access`. The `sed` trims the value to four characters: do
not print keys into the validation record.

#### 3.4.3 · The metadata route, from inside

```bash
./bin/ax ssh dsh-goal -- curl -s http://127.0.0.1:80/metadata/v1alpha1/ax/model
```

Expected: the whole `Model`, with `secretKey.name` and `secretKey.key` but **not** the
secret's value. Both `ax ssh` and that route depend on `debug: true` in the `Task` (the
example already sets it); without it, the runner does not serve the guest services.

#### 3.4.4 · Isolation: the actor does not escape the sandbox

```bash
./bin/ax ssh dsh-goal -- uname -r
kubectl get node -o jsonpath='{.items[0].status.nodeInfo.kernelVersion}'; echo
./bin/ax ssh dsh-goal -- sh -c 'echo x > /etc/x; echo exit=$?; ls -l /etc/x'
./bin/ax ssh dsh-goal -- mount | grep -E " / |workspace"
```

Expected, and how to read it:

| Command | Output measured here | What it means |
|---|---|---|
| `uname -r` in the actor | `4.19.0-gvisor` | the actor brings **its own kernel**, gVisor |
| node kernel | `7.0.12-linuxkit` | they do not share a kernel: it is not "another container" and there is no escape to the host |
| write to `/etc` | `exit=0` and the file exists | the rootfs is **not** read-only |
| `mount` | `/` as an overlay `rw`, `/workspace` as `9p` | `/workspace` is the **durable** surface (it survives suspend/resume) |

Mind the reading: this check does **not** prove the agent only writes to `/workspace`; it
proves the opposite. The criterion asks that the actor cannot reach the host or other actors, and
Substrate provides that (gVisor plus its policies), with the different kernel as the visible
evidence. The strong promise ("only writes to `/workspace`") would be
`readOnlyRootFilesystem` in the `ActorTemplate`, and that is the improvement noted at the end
of §3.4.

#### 3.4.5 · A task with no `harness` activates nothing new

The seam is *opt-in*, and this is what the code shows: `ResolveHarness("")` returns the
**Antigravity** harness (the one from before), `HarnessImage(workspaces)` returns `""` when
no workspace declares `harness.image` (so the task keeps its `spec.image`) and, with no
bound `Model`, the credential still comes from the legacy branch (`GEMINI_API_KEY`, from
`gemini-api-secret` or the controller's environment).

```bash
cat > /tmp/plain-task.yaml <<EOF
apiVersion: ax.io/v1alpha1
kind: Task
metadata: {name: plain-task, atespace: default}
spec:
  # The §3.2 image (the one the node can pull) but with NO harness block:
  # what is being verified is that AX activates nothing from the new path.
  image: "${DSH_IMAGE%:*}@${DIGEST}"
  command: ["sh", "-c", "env | grep -E '^(AX_MODEL|GEMINI|DEEPSEEK)' || echo 'no model variables'; ls /ax/dsh/settings.yaml 2>/dev/null || echo 'does not exist'"]
  debug: true
EOF

./bin/ax apply -f /tmp/plain-task.yaml
./bin/ax get task plain-task
./bin/ax ssh plain-task -- sh -c 'env | grep -E "^(AX_MODEL|GEMINI|DEEPSEEK)" || echo "no model variables"; ls /ax/dsh/settings.yaml 2>/dev/null || echo "does not exist"'
./bin/ax delete task plain-task
```

Expected: the task reaches `Running` and inside you get `no model variables` and
`does not exist`: with no `harness` block there is no `settings.yaml`, no model credential
and no image substitution. The image is named on purpose (the examples' one cannot be
pulled): what is being checked is that AX changes nothing, not what the image carries.

The pure Antigravity path (with a `goal` and its bootstrap) and the legacy Gemini branch are
also covered by the suite: `TestRun_SetsUpEveryWorkspace` and the reconciler tests. To
exercise them in the cluster with a real key: `examples/task.yaml` plus a
`gemini-api-secret` secret holding your Gemini key.

#### 3.4.6 · Key rotation

```bash
# 0. the new key, only in this shell
export NEW=...   # never in a manifest and never in the shell history

# 1. rotate the secret with the value
kubectl create secret generic deepseek-api-secret \
  --from-literal=DEEPSEEK_API_KEY="$NEW" --dry-run=client -o yaml | kubectl apply -f -

# 2. fingerprints to compare without printing the key: the secret's and the actor's
#    (`shasum -a 256` on macOS; `sha256sum` on Linux)
kubectl get secret deepseek-api-secret -o jsonpath='{.data.DEEPSEEK_API_KEY}' | base64 -d | shasum -a 256 | cut -c1-8
./bin/ax ssh dsh-goal -- sh -c 'printf %s "$DEEPSEEK_API_KEY" | sha256sum | cut -c1-8'

# 3. reprovision the actor: delete the task and apply it again
./bin/ax delete task dsh-goal && ./bin/ax apply -f examples/workspace-dsh.yaml
for i in $(seq 1 30); do ./bin/ax get tasks | grep -q "dsh-goal.*Running" && break; sleep 5; done

# 4. repeat the actor's fingerprint: it now matches the new secret's
./bin/ax ssh dsh-goal -- sh -c 'printf %s "$DEEPSEEK_API_KEY" | sha256sum | cut -c1-8'
```

Expected: after step 1 the actor **still holds the old value** — that is correct, not a
failure — and after step 3 its fingerprint is the new secret's, with no code or manifest
change. That is what the rotation criterion asks for.

Why the actor has to be recreated: the worker consumes **task events** (Redis), not secret
changes, so rotating the secret alone triggers nothing; and `EnsureActor`, on
`AlreadyExists`, returns the existing actor **without updating its template**. Since the
template name is derived from `sha256(image + environment)` (`taskTemplateName`), the
rotation creates a new template that only a new actor points at. An alternative to deleting
the task: if the actor *crashes*, the next reconcile recreates it with the new template.

- [X] 1 · `settings.yaml` inside the sandbox
- [X] 2 · credential and endpoint in the environment
- [X] 3 · `/model` route reachable from inside
- [X] 4 · the actor does not escape the sandbox (gVisor kernel different from the node's; **not** that `/etc` is read-only)
- [X] 5 · with no `harness` nothing new is activated (no settings, no model credential, no image substitution)
- [X] 6 · key rotation by recreating the actor, with no code or manifest changes

> **Improvement noted (not included).** Restricting the actor's rootfs (for example
> `readOnlyRootFilesystem` in the `ActorTemplate` AX builds) would make the strong promise
> "the agent only writes to `/workspace`" true. That is an AX design change, not part of this
> feature, and it deserves its own upstream PR.

---

## 4. If you cannot stand up Substrate: Phase 0

A short path to validate that DSH fits on an existing deployment, with zero AX changes:
`docs/dsh-phase-0.md` + `examples/task-dsh-demo.yaml`. It runs DSH inside the sandbox
through `spec.command` and your own image, without touching the new seam. Useful as a first
contact and to rule out network/credential problems before level 2.

- [ ] Phase 0 recipe executed (optional)

---

## 5. Results record

The criteria below are the ones this feature is accepted on: end-to-end behaviour, loud
failures, apply-time validation, rotation, backward compatibility, version
interoperability, isolation, first boot without registry access, and not disturbing the
Antigravity path. Each row says where it was checked and what was observed.

| Criterion | How it is checked | Result | Notes |
|---|---|---|---|
| DSH goal end-to-end | §3.3 | ✅ pass | DSH boots inside the actor (`dsh --version` → `0.1.5-rc.3`), reads the generated `settings.yaml` and reaches the `Model` endpoint; with a real key the goal is answered. The provider call needs egress, which the deployment has to provide at the Substrate level (§3.3). Before the packaging fix the same command died with `Duplicate type name 'DSH_STARTUPINFOW'` |
| loud failures, no fabrication | §1.1 (`TestClient_NoFallbackWithoutOptIn`, `TestGoogle_ErrorsAreTyped`, `TestAnthropic_TypedErrors`) | ✅ pass | a failing provider returns a typed error; no response is invented outside the explicit `DisableRemote` opt-in |
| invalid provider rejected at apply | §2 (1) | ✅ pass | `unknown provider "gemini-flash" (valid providers: anthropic, google, openai)` |
| rotation with no changes | §3.4.6 | ✅ pass | rotating the secret and recreating the actor puts the new value in the container, with no code or manifest change |
| backward compatibility | §1.1 + §3.4.5 | ✅ pass | suite green, and a task with no `harness` block gets no settings and no model variables |
| version interoperability | §1.1 (`TestWire_RoundTrip_PreservesUnknownFields`) | ✅ pass | the round trip preserves unknown fields |
| isolation | §3.4.4 | ✅ pass | the actor runs its own gVisor kernel (`4.19.0-gvisor` against the node's `7.0.12-linuxkit`); a writable rootfs is the expected result of this check, not a failure |
| first boot without npm | §3.3 (the `headless` profile ships inside the package) | ✅ pass | the image pre-bakes the CLI and the profile, so the sandbox needs no npm registry access |
| Antigravity untouched | §3.4.5 | ✅ pass | with no `harness` declared, the resolved harness is Antigravity: the path from before |

---

## 6. Cleanup

```bash
# Substrate ships its own teardown, which also deletes the local registry:
cd /tmp/ax-substrate && hack/delete-kind-cluster.sh && cd -

# If you created a bare kind cluster instead (§3.1, alternative route):
# kind delete cluster --name ax-test

docker rmi "$DSH_IMAGE" 2>/dev/null
git checkout -- examples/workspace-dsh.yaml    # reverts the image you edited
rm -rf /tmp/ax-runbook /tmp/ax-substrate

unset KUBECONFIG
rm -f /tmp/ax-test.kubeconfig
```

Check with `kubectl config current-context` that you are back on your remote cluster (and
that `kubectl config get-contexts` is unchanged) before carrying on.

## 7. What to do with failures

When something fails, what helps is the commit that introduced it, and the commits are cut
along the seam precisely for that: `feat(model)` (registry and adapters), `feat(server)`
(apply-time validation), `feat(controller)` (credentials and metadata),
`refactor(workspace)` (the harness seam) and `feat(workspace)` (DSH). A failure generating
the binding or booting `dsh` points at the last two; a credential or `/model` route failure
points at `feat(controller)`; a provider that does not answer, at `feat(model)`.

The series has to keep passing the cherry-pick test (`git format-patch upstream/main..<branch>`
+ `git am` on a clean `upstream/main`), which is how it is proven to still apply upstream.
