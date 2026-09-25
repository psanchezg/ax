# Runbook de validación: DSH + providers no-Gemini

Guía paso a paso para probar esta feature antes de exportar nada upstream. Cada paso
lleva el resultado esperado y lo que prueba; si algo falla, la columna «si falla» apunta
al sitio correcto. Marca los checkboxes conforme avances.

**Documento fork-local.** No forma parte de la serie upstream-bound (junto con
`sonar-project.properties`, `.specify/` y `.claude/`).

## Estado de la validación (ejecutada en Apple Silicon)

| Nivel | Estado |
|---|---|
| 0 — suite y runner real en local | ✅ verificado |
| 0.5 — `dsh` real contra el `settings.yaml` generado | ✅ verificado (así apareció el fallo de `agent-default-model`) |
| 1 — plano de control sin clúster | ✅ verificado |
| 2 — sandbox real (Kubernetes + Substrate) | **parcial**: AX reconcilia la tarea, inyecta la credencial del `Model`, escribe `settings.yaml` y sirve `/metadata/v1alpha1/ax/model` dentro del sandbox. La imagen del runner ya **arranca DSH** (el árbol de plugins carga y la petición sale al endpoint, verificado dentro de la imagen publicada y en el sandbox), así que lo que queda es responder un goal con una credencial real |

Cuatro cosas que aprendimos ejecutándolo y que corrigen supuestos previos:

1. **`harness.image` debe ir pinneado por digest.** Substrate rechaza un `ActorTemplate`
   cuya imagen vaya solo con tag (`must be pinned by digest`), y como AX cae entonces al
   template por defecto, el error que ves es un engañoso `actor template not found` (§3.3).
2. **Hace falta crear un `WorkerPool`**: AX no crea capacidad (§3.1).
3. **El agente *sí* puede escribir fuera de `/workspace`** dentro del sandbox. Substrate
   aísla el sandbox (kernel gVisor: el actor no escapa, no ve el host ni otros actores),
   pero no restringe el rootfs del actor. El criterio SC-007 debe leerse como «no puede
   escapar del sandbox», no como «solo escribe en `/workspace`» (§3.4, comprobación 4).
4. **`Running` no significa que el agente siga trabajando.** AX no propaga el código de
   salida del comando de la tarea: si `dsh` muere —por ejemplo con
   `AUTH: 401 ... api key is invalid`—, el `Task` se queda en `Running` y el actor sin
   proceso. Para saber si sigue vivo:
   `./bin/ax ssh <task> -- ps -eo pid,args | grep "[d]sh"`. Anotado como mejora de AX, no
   como parte de esta feature.

## 0. Preparación

```bash
cd /Users/psanchezg/Documents/GitHub/ax
git log --oneline -1                 # el tip debe ser el commit de docs más reciente
git status --short                   # árbol limpio
make build                           # bin/ax, bin/ax-server, bin/ax-controller
```

| Comprobación | Esperado |
|---|---|
| `go version` | 1.27+ |
| `docker info` | daemon accesible (nivel 2 y build de imágenes) |
| `ko version` | instalado (nivel 2: `make deploy`) |
| `kind version` | instalado (nivel 2: lo usa el script de Substrate) |
| `dsh --help` | CLI presente (nivel 0.5) |

### ⚠️ Antes de nada: contra qué clúster apunta kubectl

Los niveles 0 y 1 **no tocan Kubernetes**: el 0 ejecuta el runner en local y el 1 habla
con `ax-server` por `AX_SERVER`. Todo lo del nivel 2 —`kubectl`, `make deploy`, y
cualquier `bin/ax` **sin** `--server`, que resuelve el servidor por el contexto activo—
sigue tu kubeconfig. En esta máquina el contexto activo es un clúster **remoto**, así que
captura el original y comprueba dónde estás *antes* de escribir nada:

```bash
export ORIG_CTX="$(kubectl config current-context)"   # para volver al terminar
echo "contexto actual: $ORIG_CTX"
kubectl config get-contexts | sed -n '1,10p'
```

Si ese contexto es un clúster compartido o de producción, **no** ejecutes el nivel 2
sobre él: `make deploy` instalaría AX (namespace `ax-system`, RBAC de lectura de secrets,
Redis, controller) y aplicaría egress ahí. El nivel 2 va sobre un clúster desechable
local (`kind`, §3.1) y nunca sobre el remoto. Los niveles 0 y 1 puedes hacerlos ya sin
tocar el contexto.

Exporta tus valores una vez por shell (no los commitees):

```bash
export REGISTRY=ghcr.io/<tu-org>            # registry que el clúster pueda tirar
export ATESPACE=default
export DSH_IMAGE="${REGISTRY}/ax-dsh-runner:0.1.5-rc.3"
export DEEPSEEK_API_KEY_SRC=...             # tu clave real, solo en el shell
```

- [X] `make build` sin errores

---

## 1. Nivel 0 — regresión y E2E local (sin infraestructura)

### 1.1 Suite completa

```bash
make test
```

Esperado: `ok` en los 8 paquetes (`internal/controller`, `internal/metadata`,
`internal/model`, `internal/server`, `internal/tunnel`, `internal/workspace`,
`pkg/apis/v1alpha1`, `runner`). Prueba: registry y adaptadores, validación en apply,
inyección de credenciales, dispatcher de harness y el path Antigravity sin tocar.

Si falla: `go test -count=1 -run <Test> ./<paquete>/... -v`.

- [X] Suite verde

### 1.2 Los manifiestos del repo siguen siendo válidos

```bash
go test -count=1 ./pkg/apis/... -run TestExamples -v
```

Esperado: PASS para `model-deepseek.yaml`, `model-local-qwen.yaml`, `simple.yaml`,
`task-dsh-demo.yaml` (sus 2 documentos) y `task.yaml`. Prueba: ningún ejemplo se ha
quedado obsoleto respecto al esquema estricto.

- [X] PASS

### 1.3 Runner real en local, con harness DSH

Esto ejercita el ciclo completo del contenedor sin clúster:

```bash
W=/tmp/ax-runbook; rm -rf "$W"; mkdir -p "$W/ws" "$W/dsh"

# El runner del host (make build NO lo genera: build-task-runner lo cross-compila
# para linux/amd64, que no se puede ejecutar en macOS).
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
curl -s http://127.0.0.1:18099/metadata/v1alpha1/ax/model     # → el Model, con la referencia al secreto
cat "$W/out.txt"                                             # → runner-ok
cat "$W/dsh/settings.yaml"                                   # → binding llm-pi-ai
kill $RUNNER_PID
```

Esperado en `settings.yaml` (**dos** secciones; la segunda es la que hace que el agente
use la ruta de la primera, y es fácil de olvidar — su ausencia fue el primer hallazgo de
esta validación):

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

Comprueba además que en la salida de `/metadata/v1alpha1/ax/model` **no** aparece
ningún valor de secreto, solo `name`/`key`.

Si falla: `cat "$W/runner.log"`. Un `unsupported harness kind` significa que el kind
está mal escrito; un error de `provider ... has no DeepSeek Harness mapping` viene de un
provider sin adaptador DSH (`google`/`openai`/`anthropic` son los válidos).

- [X] `/readyz` responde `ok`
- [X] `/metadata/v1alpha1/ax/model` sirve el `Model` sin valor de secreto
- [X] `settings.yaml` generado como arriba
- [X] El comando de la tarea se ejecutó

### 1.4 El único supuesto externo: el `dsh` real usa *tu* binding

#### 1.4a Con tu clave real (prueba de extremo a extremo)

Fuera de un clúster nadie inyecta la credencial, así que **exporta tú la variable que
declara el `Model`** (`secretKey.key`) antes de lanzar DSH:

```bash
export DEEPSEEK_API_KEY=...            # el nombre que declara tu Model, no otro
DSH_HOME=/tmp/ax-runbook/dsh DSH_PERMISSION_MODE=danger-full-access \
  dsh --profile headless "lista los ficheros del workspace y di qué ves"
```

Esperado: DSH arranca, resuelve la credencial por `apiKeyEnv`, llama al `baseURL` del
`Model` y termina con una respuesta en stdout.

> Ojo con la interpretación: si tu `Model` apunta a `api.deepseek.com`, un 401 **no**
> distingue tu ruta de la interna de DSH, porque ambas usan ese host por defecto. Prueba
> lo que prueba: que la credencial llega, el endpoint responde y el error se propaga sin
> fabricar nada. Para la ruta, usa 1.4b.

#### 1.4b Sin clave: comprobar que DSH llama al `baseURL` del `Model`

Este es el test que sí discrimina. Sustituye el endpoint por uno local tuyo y mira si DSH
llama ahí:

```bash
W=/tmp/ax-runbook

# 1) Un endpoint falso que registra lo que recibe y devuelve 401.
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
head -3 "$W/fake.log"          # debe estar vacío; si dice "Address already in use", otro proceso tiene el 18999
# Si el puerto está ocupado, cambia el 18999 por otro (18998) aquí y en el baseURL de abajo.

# 2) Apunta el Model al stub y regenera settings.yaml con el runner de verdad.
sed -i '' 's|https://api.deepseek.com/v1|http://127.0.0.1:18999/v1|' "$W/model.yaml"
grep baseURL "$W/model.yaml"                      # → http://127.0.0.1:18999/v1
rm -rf "$W/dsh"; mkdir -p "$W/dsh"
DSH_HOME="$W/dsh" AX_MODEL_YAML="$(cat "$W/model.yaml")" \
  "$W/ax-task-runner" --task-file "$W/task.yaml" --workspace-file "$W/ws.yaml" --port 18099 \
  > "$W/runner.log" 2>&1 &
RUNNER=$!
sleep 2; kill $RUNNER 2>/dev/null || true          # el runner ya escribió settings.yaml
grep -A2 agent-default-model "$W/dsh/settings.yaml"

# 3) Lanza DSH con una clave dummy y mira quién recibe la petición.
DSH_HOME="$W/dsh" DEEPSEEK_API_KEY=dummy-key \
  dsh --profile headless "di hola" > "$W/dsh.log" 2>&1 &
DSH=$!
for i in $(seq 1 12); do sleep 5; grep -q "REQ" "$W/fake.log" 2>/dev/null && break; done
kill $DSH 2>/dev/null || true

echo "== peticiones recibidas por el stub =="; cat "$W/fake.log"
echo "== salida de dsh =="; head -3 "$W/dsh.log"
kill $FAKE 2>/dev/null || true
```

Esperado:

```text
== peticiones recibidas por el stub ==
REQ /v1/chat/completions auth=Bearer dummy-key b'{"model":"deepseek-chat","messages":[{"role":"system",...
== salida de dsh ==
dsh: AUTH: 401: {"message":"fake server reached"}
```

El error viene de **tu** endpoint, no de DeepSeek: eso prueba que la ruta del `Model` está
seleccionada. Si el stub no recibe nada y ves
`MISSING_CREDENTIAL ... llm-deepseek: ... route "deepseek-official"`, DSH está usando su
ruta interna: comprueba que `settings.yaml` incluye la sección `agent-default-model`
apuntando a la ruta de `llm-pi-ai` (y que regeneraste con el runner actual).

Recuerda revertir el `baseURL` del `model.yaml` después:

```bash
sed -i '' 's|http://127.0.0.1:18999/v1|https://api.deepseek.com/v1|' "$W/model.yaml"
```

Si falla: `MISSING_CREDENTIAL` para **tu** ruta → no exportaste la variable del `Model`;
error HTTP del proveedor → la clave o el `baseURL`; timeout → endpoint/egress de tu red.

- [X] 1.4a: DSH responde con una clave real
- [X] 1.4b: el stub local recibe la petición (la ruta del `Model` está seleccionada)

---

## 2. Nivel 1 — plano de control sin clúster

Prueba la validación real de `ax apply` y la persistencia, sin ejecutar tareas. Los
puertos van desplazados (16379/18080) para no chocar con un Redis o un 8080 que ya
tengas levantados. El `AX_SERVER` de la primera línea es lo que mantiene este nivel fuera
de Kubernetes: sin él, `bin/ax` resolvería el servidor por tu contexto activo (tu clúster
remoto).

```bash
docker run -d --rm --name ax-runbook-redis -p 16379:6379 redis:7-alpine
./bin/ax-server --addr :18080 --redis-addr localhost:16379 &
SERVER_PID=$!
export AX_SERVER=127.0.0.1:18080
sleep 1

# 1) Validación: un provider con typo debe ser rechazado en apply, no después.
#    ✅ ESTE PASO PASA CUANDO FALLA. Un "Error: ... unknown provider" aquí es el
#    criterio de éxito (SC-003), no un problema.
cat <<'EOF' | ./bin/ax apply -f -
apiVersion: ax.io/v1alpha1
kind: Model
metadata: {name: typo-model, atespace: default}
spec: {provider: gemini-flash, model: x}
EOF
# Esperado: salida no-cero, y el mensaje nombra google, openai, anthropic:
#   Error: applying document 1: rpc error: code = InvalidArgument desc =
#   invalid model: unknown provider "gemini-flash" (valid providers: anthropic, google, openai)
# Ojo: ese apply devuelve 1 a propósito; si tu shell tiene `set -e`, envuélvelo en `|| true`
# o sigue con el paso 2 en una línea aparte.

# 2) Los manifiestos reales deben aceptarse
./bin/ax apply -f examples/model-deepseek.yaml
./bin/ax apply -f examples/workspace-dsh.yaml
./bin/ax get models
./bin/ax get tasks
./bin/ax get workspaces
```

Esperado (salidas reales de esta receta):

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

El `Task` se queda en `Pending`: no hay controller ni Substrate, es lo normal en este
nivel. Lo que sí ha quedado probado es que el servidor **valida el provider en apply** y
que `Workspace.spec.harness` viaja y se persiste correctamente.

Limpieza:

```bash
kill $SERVER_PID; docker stop ax-runbook-redis
```

- [X] Provider con typo rechazado nombrando los válidos
- [X] `Model`, `Workspace` y `Task` creados y leídos

---

## 3. Nivel 2 — sandbox real (Kubernetes + Agent Substrate)

El único nivel que prueba el objetivo completo. **Substrate es un proyecto aparte**: AX
no lo instala (`deploy/` solo trae Redis, server y controller). Sin una Control API
alcanzable en `api.ate-system.svc.cluster.local:443`, este nivel no arranca.

### 3.1 Clúster y Substrate

No crees el clúster a mano: Substrate trae el script que este nivel necesita, y crea
además el **registry local** que luego usaremos para las imágenes de AX (los nodos de
kind ya vienen configurados para tirar de él, con los feature gates que necesita su
`podcertcontroller`).

```bash
# Aísla el kubeconfig para no tocar tu clúster remoto (§0). El script respeta $KUBECONFIG.
export KUBECONFIG=/tmp/ax-test.kubeconfig
export KIND_CLUSTER_NAME=ax-test        # evita que borre un kind llamado "kind" que ya tengas

git clone https://github.com/agent-substrate/substrate.git /tmp/ax-substrate
cd /tmp/ax-substrate
hack/create-kind-cluster.sh             # clúster + registry localhost:5001 (KIND_REGISTRY_PORT)
hack/install-ate-kind.sh --deploy-ate-system

cd -                                    # vuelve al repo de AX
kubectl config current-context          # → kind-ax-test
kubectl get svc -n ate-system           # la Control API que AX espera (api.ate-system…)
kubectl get pods -n ate-system
```

Notas del script, leídas de su fuente: el clúster por defecto se llama `kind`
(`KIND_CLUSTER_NAME` lo cambia), el registry local es `kind-registry` en
`127.0.0.1:5001`, `IP_FAMILY` es `ipv4` por defecto, y **si no hay `/dev/kvm` —lo normal
en Docker Desktop sobre macOS— desactiva micro-VMs y sigue con gVisor**. El desmontaje es
`hack/delete-kind-cluster.sh`.

Si la Control API no se llama `api` o no escucha en 443, no hace falta tocar código: el
`ax-controller` acepta `--substrate-endpoint` y `--substrate-authority`.

#### ⚠️ La trampa de macOS que cuesta una hora: `DOCKER_DEFAULT_PLATFORM`

En Apple Silicon, kind usa la plataforma por defecto de Docker para la imagen del nodo. Si
tienes `DOCKER_DEFAULT_PLATFORM=linux/amd64` (es fácil que esté en `~/.zshrc` por otro
proyecto), el nodo se crea **amd64 y emulado**, y `containerd`/`kubelet` entran en
crash-loop: el clúster queda en `NotReady` y parece un timeout de bootstrap.

```bash
echo "$DOCKER_DEFAULT_PLATFORM"                     # ¿dice linux/amd64?
docker logs ax-test-control-plane 2>&1 | grep -m1 "Detected architecture"
#   amd64 → nodo emulado (mal);  arm64 → correcto

# Arréglalo solo para esta shell y rehaz el clúster:
unset DOCKER_DEFAULT_PLATFORM
kind delete cluster --name ax-test
docker rmi -f kindest/node:v1.37.0
docker pull --platform linux/arm64 kindest/node:v1.37.0
hack/create-kind-cluster.sh
```

Merece la pena quitar ese `export` del `~/.zshrc` y pasarlo por comando
(`DOCKER_DEFAULT_PLATFORM=linux/amd64 docker build …`) cuando lo necesites: si no, afecta
también a la imagen del runner y a cualquier `docker build` de este flujo.

Y una advertencia sobre kind: su `--wait` por defecto **no espera al control-plane**, así
que un nodo roto se reporta como creación correcta. Comprueba siempre con
`kubectl get nodes` que el nodo está `Ready` antes de seguir.

#### ⚠️ Si el install falla con `gcloud.auth.docker-helper`

No hace falta ningún login de Google para instalar Substrate en kind — el script incluso
hace `unset GCE_REGION ... PROJECT_ID`. Pero si tu `~/.docker/config.json` mapea `gcr.io`
al credential helper de `gcloud` y tu token está caducado, **ko no podrá bajar ni una
imagen pública** (`gcr.io/distroless/...`) y el install aborta con:

```text
ERROR: (gcloud.auth.docker-helper) ... Reauthentication failed
error getting credentials - err: exit status 1
```

Dos salidas:

```bash
# A) Reautenticar gcloud una vez (interactivo, abre el navegador)
gcloud auth login

# B) No tocar tu configuración: darle a esta ejecución un DOCKER_CONFIG sin helpers
mkdir -p /tmp/docker-nogcloud && echo '{}' > /tmp/docker-nogcloud/config.json
export DOCKER_CONFIG=/tmp/docker-nogcloud
```

La B basta para todo este flujo (todas las imágenes son públicas y el registry local no
pide credenciales), y también aplica al `ko apply` del plano de control de AX, cuyas
imágenes base salen de `gcr.io/distroless`.

Después, AX. El registry local del clúster es lo que permite desplegar sin publicar nada:

```bash
# Arquitectura de los nodos: en Apple Silicon kind usa nodos arm64, así que las
# imágenes deben ser arm64 (ko y docker build, ambos, con --platform).
NODE_ARCH="$(docker exec ax-test-control-plane uname -m)"
case "$NODE_ARCH" in aarch64|arm64) PLATFORM=linux/arm64 ;; *) PLATFORM=linux/amd64 ;; esac
echo "nodos $NODE_ARCH → $PLATFORM"

export AX_IMAGE_REPO=localhost:5001/ax            # registry del clúster
KO_DOCKER_REPO=$AX_IMAGE_REPO ko apply --platform=$PLATFORM -f deploy/redis.yaml
KO_DOCKER_REPO=$AX_IMAGE_REPO ko apply --platform=$PLATFORM -f deploy/ax-server.yaml
KO_DOCKER_REPO=$AX_IMAGE_REPO ko apply --platform=$PLATFORM -f deploy/ax-controller.yaml
kubectl get pods -n ax-system
```

#### Capacidad: hay que crear un `WorkerPool` (AX no lo hace)

AX pide a Substrate atespaces, plantillas y actores, pero **no crea capacidad**. Sin un
`WorkerPool` el actor se queda sin sitio y la tarea no arranca. AX fija la clase
`SANDBOX_CLASS_GVISOR`, así que el pool debe ser gVisor (el `SandboxConfig` que instala
Substrate ya se llama `gvisor-default`), y la imagen del worker se construye con ko:

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

El `namespace` del pool no restringe la selección (el emparejamiento es por etiquetas);
`spec.template.resources.limits` es la **capacidad por actor**, así que dimensiónalo por
encima de lo que pidas en la tarea.

- [X] `kubectl config current-context` = `kind-ax-test`
- [X] Substrate instalado y su Control API visible en `ate-system`
- [X] Pods de `ax-system` en `Running`
- [X] La plataforma de las imágenes coincide con la de los nodos
- [X] `WorkerPool` con 2 réplicas `READY`

### 3.2 Imagen del runner con DSH

```bash
export DSH_IMAGE="localhost:5001/ax-dsh-runner:0.1.5-rc.3"   # el registry local del clúster

# El target compila el runner y la imagen para la MISMA arquitectura
# (TASK_RUNNER_GOARCH, por defecto la del host, que es la de los nodos de kind aquí)
# y con el tag que le digas: separa $DSH_IMAGE en repo y tag, o el Makefile
# publicaría :latest y luego no encontrarías la etiqueta que usas en el manifiesto.
unset DOCKER_DEFAULT_PLATFORM
export DOCKER_CONFIG=/tmp/docker-nogcloud      # si el install necesitó esta variable
make push-task-runner-dsh \
  TASK_RUNNER_GOARCH="${PLATFORM#linux/}" \
  DSH_TASK_RUNNER_REPO="${DSH_IMAGE%:*}" \
  DSH_TASK_RUNNER_TAG="${DSH_IMAGE##*:}"
```

`push` publica en el registry local (`localhost:5001`), que es de donde tiran los nodos;
`build-task-runner-dsh` solo construye en local.

Comprueba la arquitectura antes de aplicar nada:

```bash
docker image inspect "$DSH_IMAGE" --format 'imagen: {{.Architecture}}/{{.Os}}'
docker run --rm --entrypoint sh "$DSH_IMAGE" -c 'uname -m'     # → aarch64 en Apple Silicon
```

Si vuelves a construir con la **misma** etiqueta, los nodos pueden quedarse con la copia
cacheada: sube la etiqueta (`:test2`) al repetir.

> **Cómo se arregló el arranque de DSH (esta validación).** El CLI tiene que instalarse
> **global** (`npm install -g --prefix /opt/dsh`): así npm aplana todo el bundle dentro del
> `node_modules` del propio CLI y no duplica nada. Una instalación de proyecto anida una
> segunda copia completa del cierre bajo `dsh-base` y el loader muere con `Duplicate type
> name 'DSH_STARTUPINFOW'`. npm además se deja fuera un plugin,
> `@deepseek-ai/dsh-sandbox-local`, por un conflicto de peers (`plugin(s) failed to load:
> @deepseek-ai/dsh-sandbox-local`), así que se nombra explícitamente en el install. Quedan
> ~16 paquetes transitivos duplicados (los reporta el build) y **no** impiden el arranque.
> Como el fallo sólo aparece al arrancar dentro de una tarea, el `Dockerfile` arranca el
> CLI una vez contra un puerto cerrado y exige que pase del árbol de plugins. Detalle en
> `docs/deepseek-harness.md`.

- [X] Imagen publicada en el registry del clúster, con la arquitectura correcta

### 3.3 Recursos de prueba

```bash
# Substrate exige el DIGEST: rechaza un template cuya imagen vaya solo con tag
# ("must be pinned by digest"), y si el template falla AX cae al default y el
# error que ves es un confuso "actor template not found".
DIGEST=$(docker image inspect "$DSH_IMAGE" --format '{{index .RepoDigests 0}}' 2>/dev/null | sed 's/.*@//')
[ -n "$DIGEST" ] || DIGEST=$(docker push "$DSH_IMAGE" 2>&1 | awk '/digest: sha256/{print $3; exit}')
echo "imagen pinneada: ${DSH_IMAGE%:*}@$DIGEST"

kubectl create secret generic deepseek-api-secret \
  --from-literal=DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY_SRC"

# Edita la imagen en el ejemplo con el digest, no con el tag. El patrón sustituye
# cualquier digest previo: si no, validarías la imagen anterior creyendo que es la nueva.
sed -i '' -E "s|^([[:space:]]*image: \").*(\")$|\1${DSH_IMAGE%:*}@${DIGEST}\2|" examples/workspace-dsh.yaml
grep -n 'image:' examples/workspace-dsh.yaml    # tu repo y el digest recién publicado

./bin/ax apply -f examples/model-deepseek.yaml
./bin/ax apply -f examples/workspace-dsh.yaml
./bin/ax get task dsh-goal
./bin/ax describe task dsh-goal
```

Esperado: el `Task` pasa por `Running` y el goal lo responde DSH. Si usas un `Gateway`
endurecido, añade `api.deepseek.com:443` a su allowlist (por defecto `*:443`).

- [X] `Task` alcanza `Running`
- [X] El `Task` llega a completarse y DSH imprime su mensaje final en stdout del contenedor
      (`./bin/ax ssh dsh-goal -- ps aux | grep dsh` lo confirma en vivo)

### 3.4 Las seis comprobaciones que importan

| # | Criterio | Procedimiento |
|---|---|---|
| 1 | `settings.yaml` con el binding del `Model` | §3.4.1 |
| 2 | credencial y endpoint en el entorno del actor | §3.4.2 |
| 3 | `/metadata/v1alpha1/ax/model` desde dentro | §3.4.3 |
| 4 | aislamiento del sandbox (SC-007) | §3.4.4 |
| 5 | camino sin `harness` intacto (SC-005, SC-009) | §3.4.5 |
| 6 | rotación de clave sin cambios (SC-004) | §3.4.6 |

Dos precondiciones que no son de esta feature pero bloquean cualquier `Task` nuevo (y por
tanto las comprobaciones 5 y 6):

- **Un worker libre.** Cada `Task` en `Running` ocupa un worker del pool (§3.1) y el pool
  tiene dos. Con el pool lleno el task nuevo no arranca y la condición dice:
  `Ready False ActorResumeFailed ... rpc error: code = ResourceExhausted desc = no free workers available`.
  Libera uno antes: `./bin/ax delete task dsh-e2e` (o el que no estés usando). Un task que
  quedó en `Failed` no se recupera con otro `apply`: hay que borrarlo y volver a aplicarlo.
- **Una imagen que el nodo pueda tirar.** `examples/simple.yaml` y `examples/task.yaml`
  usan `gcr.io/ax-substrate/ate-images/...`, que es privada; el actor muere al crear el
  bundle con
  `DENIED: Unauthenticated request ... artifactregistry.repositories.downloadArtifacts`.
  Para un task nuevo usa la imagen del §3.2 (registry local) o un runner plano en tu
  propio registry.

#### 3.4.1 · `settings.yaml` dentro del sandbox

```bash
./bin/ax ssh dsh-goal -- ls -l /ax/dsh
./bin/ax ssh dsh-goal -- cat /ax/dsh/settings.yaml
```

Esperado: `settings.yaml` (y `profiles/`, `sessions/`, `storages/`, que DSH crea en su
primer arranque) con las **dos** secciones que escribe el harness:
`llm-pi-ai.providers.<modelo>` con `api`, `apiKeyEnv`, `baseURL` y `models`, y
`agent-default-model` apuntando a ese proveedor. Si falta la segunda, DSH tira de su ruta
interna y el síntoma es `MISSING_CREDENTIAL`.

#### 3.4.2 · Credencial y endpoint en el entorno del actor

```bash
./bin/ax ssh dsh-goal -- sh -c 'env | grep -E "^(DEEPSEEK_API_KEY|AX_MODEL_BASE_URL|DSH_HOME|DSH_PERMISSION_MODE)=" | sed "s/=\(.\{4\}\).*/=\1.../"'
```

Esperado: la credencial **bajo el nombre que declara el `Model`** (`DEEPSEEK_API_KEY` aquí),
`AX_MODEL_BASE_URL` con el endpoint, `DSH_HOME=/ax/dsh` y
`DSH_PERMISSION_MODE=danger-full-access`. El `sed` recorta el valor a cuatro caracteres:
no imprimas claves en el registro de la validación.

#### 3.4.3 · La ruta de metadata, desde dentro

```bash
./bin/ax ssh dsh-goal -- curl -s http://127.0.0.1:80/metadata/v1alpha1/ax/model
```

Esperado: el `Model` completo, con `secretKey.name` y `secretKey.key` pero **sin el valor**
del secreto. Tanto `ax ssh` como esa ruta dependen de `debug: true` en el `Task` (el
ejemplo ya lo trae); sin él, el runner no sirve los servicios de invitado.

#### 3.4.4 · Aislamiento (SC-007): el actor no escapa del sandbox

```bash
./bin/ax ssh dsh-goal -- uname -r
kubectl get node -o jsonpath='{.items[0].status.nodeInfo.kernelVersion}'; echo
./bin/ax ssh dsh-goal -- sh -c 'echo x > /etc/x; echo exit=$?; ls -l /etc/x'
./bin/ax ssh dsh-goal -- mount | grep -E " / |workspace"
```

Esperado, y cómo se lee:

| Comando | Salida medida aquí | Qué significa |
|---|---|---|
| `uname -r` en el actor | `4.19.0-gvisor` | el actor trae **su propio kernel**, gVisor |
| kernel del nodo | `7.0.12-linuxkit` | no comparten kernel: no es «otro contenedor» y no hay escape al host |
| escritura en `/etc` | `exit=0` y el fichero existe | el rootfs **no** es de solo lectura |
| `mount` | `/` como overlay `rw`, `/workspace` como `9p` | `/workspace` es la superficie **durable** (sobrevive a suspender/reanudar) |

Ojo con la lectura: esta comprobación **no** demuestra que el agente solo escriba en
`/workspace`; demuestra lo contrario. SC-007 pide que el actor no alcance el host ni a otros
actores, y eso lo aporta Substrate (gVisor más sus políticas), con el kernel distinto como
evidencia visible. La promesa fuerte («solo escribe en `/workspace`») sería
`readOnlyRootFilesystem` en el `ActorTemplate`, y es la mejora anotada al final de §3.4.

#### 3.4.5 · Un task sin `harness` no activa nada nuevo (SC-005, SC-009)

La costura es *opt-in*, y esto es lo que hay que ver en el código: `ResolveHarness("")`
devuelve el harness **Antigravity** (el de siempre), `HarnessImage(workspaces)` devuelve
`""` cuando ningún workspace declara `harness.image` (así el task conserva su `spec.image`)
y, sin `Model` enlazado, la credencial sigue saliendo por la rama legacy
(`GEMINI_API_KEY`, de `gemini-api-secret` o del entorno del controller).

```bash
cat > /tmp/plain-task.yaml <<EOF
apiVersion: ax.io/v1alpha1
kind: Task
metadata: {name: plain-task, atespace: default}
spec:
  # La imagen del §3.2 (la que el nodo sí puede tirar) pero SIN bloque harness:
  # lo que se comprueba es que AX no activa nada del camino nuevo.
  image: "${DSH_IMAGE%:*}@${DIGEST}"
  command: ["sh", "-c", "env | grep -E '^(AX_MODEL|GEMINI|DEEPSEEK)' || echo 'sin variables de modelo'; ls /ax/dsh/settings.yaml 2>/dev/null || echo 'no existe'"]
  debug: true
EOF

./bin/ax apply -f /tmp/plain-task.yaml
./bin/ax get task plain-task
./bin/ax ssh plain-task -- sh -c 'env | grep -E "^(AX_MODEL|GEMINI|DEEPSEEK)" || echo "sin variables de modelo"; ls /ax/dsh/settings.yaml 2>/dev/null || echo "no existe"'
./bin/ax delete task plain-task
```

Esperado: el task llega a `Running` y dentro sale `sin variables de modelo` y `no existe`:
sin bloque `harness` no hay `settings.yaml`, ni credencial de modelo, ni imagen sustituida.
La imagen se nombra a propósito (la de los ejemplos no se puede tirar): lo que se comprueba
es que AX no toca nada, no qué lleva la imagen.

El camino Antigravity puro (con `goal` y su bootstrap) y la rama legacy de Gemini los cubre
además la suite: `TestRun_SetsUpEveryWorkspace` y los tests del reconciler. Para
ejercitarlos en el clúster con clave real: `examples/task.yaml` más un secreto
`gemini-api-secret` con tu clave de Gemini.

#### 3.4.6 · Rotación de clave (SC-004)

```bash
# 0. la clave nueva, solo en este shell
export NEW=...   # nunca en un manifiesto ni en el historial del shell

# 1. rota el secreto con el valor
kubectl create secret generic deepseek-api-secret \
  --from-literal=DEEPSEEK_API_KEY="$NEW" --dry-run=client -o yaml | kubectl apply -f -

# 2. huellas para comparar sin imprimir la clave: la del secreto y la de dentro del actor
#    (`shasum -a 256` en macOS; `sha256sum` en Linux)
kubectl get secret deepseek-api-secret -o jsonpath='{.data.DEEPSEEK_API_KEY}' | base64 -d | shasum -a 256 | cut -c1-8
./bin/ax ssh dsh-goal -- sh -c 'printf %s "$DEEPSEEK_API_KEY" | sha256sum | cut -c1-8'

# 3. reprovisiona el actor: borrar el task y volver a aplicarlo
./bin/ax delete task dsh-goal && ./bin/ax apply -f examples/workspace-dsh.yaml
for i in $(seq 1 30); do ./bin/ax get tasks | grep -q "dsh-goal.*Running" && break; sleep 5; done

# 4. repite la huella del actor: ahora coincide con la del secreto nuevo
./bin/ax ssh dsh-goal -- sh -c 'printf %s "$DEEPSEEK_API_KEY" | sha256sum | cut -c1-8'
```

Esperado: tras el paso 1 el actor **sigue con el valor viejo** —es correcto, no es un
fallo—, y tras el paso 3 su huella es la del secreto nuevo, sin tocar código ni
manifiestos. Eso es lo que pide SC-004.

Por qué hay que recrear el actor: el worker consume **eventos de task** (Redis), no cambios
de secreto, así que rotar el secreto por sí solo no dispara nada; y `EnsureActor`, ante
`AlreadyExists`, devuelve el actor existente **sin actualizar su template**. Como el nombre
del template se deriva de `sha256(imagen + entorno)` (`taskTemplateName`), la rotación crea
un template nuevo al que solo apunta un actor nuevo. Alternativa a borrar el task: si el
actor *crashea*, el siguiente reconcile lo recrea con el template nuevo.

- [X] 1 · `settings.yaml` dentro del sandbox
- [X] 2 · credencial y endpoint en el entorno
- [X] 3 · ruta `/model` accesible desde dentro
- [ ] 4 · el actor no escapa del sandbox (kernel gVisor distinto del nodo; **no** que `/etc` sea de solo lectura)
- [ ] 5 · sin `harness` no se activa nada nuevo (ni settings, ni credencial de modelo, ni imagen sustituida)
- [ ] 6 · rotación de clave recreando el actor, sin cambios en código ni manifiestos

> **Mejora anotada (no incluida).** Restringir el rootfs del actor (por ejemplo
> `readOnlyRootFilesystem` en el `ActorTemplate` que construye AX) haría cierta la promesa
> fuerte de «el agente solo escribe en `/workspace`». Es un cambio de diseño de AX, no de
> esta feature, y merece su propio PR upstream.

---

## 4. Si no puedes montar Substrate: Fase 0

Camino corto para validar que DSH encaja sobre un deployment existente, con cero cambios
de AX: `docs/dsh-phase-0.md` + `examples/task-dsh-demo.yaml`. Prueba DSH dentro del
sandbox vía `spec.command` y una imagen propia, sin tocar la costura nueva. Útil como
primer contacto y para descartar problemas de red/credenciales antes del nivel 2.

- [ ] Receta de Fase 0 ejecutada (opcional)

---

## 5. Registro de resultados

| Criterio | Cómo se comprueba | Resultado | Notas |
|---|---|---|---|
| SC-001 goal DSH end-to-end | §3.3 | parcial | DSH **arranca en el sandbox** (imagen `0.1.5-rc.3`, `dsh --version` dentro del actor) y la petición sale al endpoint del `Model`: `./bin/ax ssh dsh-goal -- sh -c 'dsh --profile headless "reply with ok"'` devuelve `AUTH: 401 ... api key: ****-key is invalid`. Falta una clave real en `deepseek-api-secret` para ver la respuesta final; antes de este arreglo el mismo comando moría en `Duplicate type name 'DSH_STARTUPINFOW'` |
| SC-002 fallos ruidosos, cero fabricación | §1.1 | | |
| SC-003 provider inválido rechazado en apply | §2 (1) | | |
| SC-004 rotación sin cambios | §3.4 (6) | | |
| SC-005 compatibilidad hacia atrás | §1.1 + §3.4 (5) | | |
| SC-006 interoperabilidad de versiones | §1.1 (`TestWire_RoundTrip_PreservesUnknownFields`) | | |
| SC-007 aislamiento | §3.4 (4) | | |
| SC-008 primer arranque sin npm | §3.3 (el perfil `headless` viene en el paquete) | | |
| SC-009 Antigravity intacto | §3.4 (5) | | |

---

## 6. Limpieza

```bash
# Substrate trae su propio desmontaje, que además borra el registry local:
cd /tmp/ax-substrate && hack/delete-kind-cluster.sh && cd -

# Si creaste un clúster kind pelado (§3.1, vía alternativa):
# kind delete cluster --name ax-test

docker rmi "$DSH_IMAGE" 2>/dev/null
git checkout -- examples/workspace-dsh.yaml    # revierte la imagen que editaste
rm -rf /tmp/ax-runbook /tmp/ax-substrate

unset KUBECONFIG
rm -f /tmp/ax-test.kubeconfig
```

Comprueba con `kubectl config current-context` que vuelves a ver tu clúster remoto (y que
`kubectl config get-contexts` no ha cambiado) antes de seguir trabajando.

## 7. Qué hacer con los fallos

Si algo falla, lo útil es el commit que lo introdujo, y los commits están cortados por
costura precisamente para eso: `feat(model)` (registry y adaptadores), `feat(server)`
(validación en apply), `feat(controller)` (credenciales y metadata), `refactor(workspace)`
(costura del harness) y `feat(workspace)` (DSH). Un fallo en la generación del binding o
en el arranque de `dsh` apunta a los dos últimos; un fallo de credencial o de ruta
`/model`, a `feat(controller)`; un provider que no responde, a `feat(model)`.

Antes de exportar upstream, recuerda que la serie debe volver a pasar el test de
cherry-pick (`git format-patch upstream/main..<rama>` + `git am` sobre `upstream/main`
limpio), que es lo que verifica la constitución.
