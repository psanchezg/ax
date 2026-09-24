# Runbook de validación: DSH + providers no-Gemini

Guía paso a paso para probar esta feature antes de exportar nada upstream. Cada paso
lleva el resultado esperado y lo que prueba; si algo falla, la columna «si falla» apunta
al sitio correcto. Marca los checkboxes conforme avances.

**Documento fork-local.** No forma parte de la serie upstream-bound (junto con
`sonar-project.properties`, `.specify/` y `.claude/`).

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
export DSH_IMAGE="${REGISTRY}/ax-dsh-runner:test1"
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

- [X] `kubectl config current-context` = `kind-ax-test`
- [X] Substrate instalado y su Control API visible en `ate-system`
- [X] Pods de `ax-system` en `Running`
- [X] La plataforma de las imágenes coincide con la de los nodos

### 3.2 Imagen del runner con DSH

```bash
export DSH_IMAGE="localhost:5001/ax-dsh-runner:test1"   # el registry local del clúster

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

> **⚠️ Limitación conocida (encontrada en esta validación).** La instalación npm del CLI
> dentro de una imagen Linux **no arranca**: el loader no resuelve
> `@deepseek-ai/dsh-sandbox-local` con una instalación global, y con una instalación de
> proyecto encuentra dos copias idénticas y muere con `Duplicate type name
> 'DSH_STARTUPINFOW'`. Se probaron npm 10 y 12, `--legacy-peer-deps`, `npm dedupe`,
> borrar la copia anidada, una imagen con pnpm y la instalación desde el tarball; no hay
> imagen oficial de DSH que reutilizar. Para el nivel 2, **usa una imagen construida con
> las herramientas de DeepSeek** (o la que ya te funcione) y apunta `harness.image` a su
> digest: todo lo demás —el `settings.yaml` que escribe el harness, el contrato de
> entorno y las rutas de metadata— es independiente de cómo llegó DSH a la imagen. El
> detalle completo está en `docs/deepseek-harness.md`.

- [ ] Imagen publicada en el registry del clúster, con la arquitectura correcta

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

# Edita la imagen en el ejemplo con el digest, no con el tag:
sed -i '' "s|ghcr.io/<org>/ax-dsh-runner@sha256:<digest>|${DSH_IMAGE%:*}@$DIGEST|" examples/workspace-dsh.yaml

./bin/ax apply -f examples/model-deepseek.yaml
./bin/ax apply -f examples/workspace-dsh.yaml
./bin/ax get task dsh-goal
./bin/ax describe task dsh-goal
```

Esperado: el `Task` pasa por `Running` y el goal lo responde DSH. Si usas un `Gateway`
endurecido, añade `api.deepseek.com:443` a su allowlist (por defecto `*:443`).

- [ ] `Task` alcanza `Running`
- [ ] El `Task` llega a completarse y DSH imprime su mensaje final en stdout del contenedor
      (`./bin/ax ssh dsh-goal -- ps aux | grep dsh` lo confirma en vivo)

### 3.4 Las seis comprobaciones que importan

| # | Comando | Esperado |
|---|---|---|
| 1 | `./bin/ax ssh dsh-goal -- ls -l /ax/dsh` | `settings.yaml` con el binding del `Model`; DSH arrancó contra tu endpoint |
| 2 | `./bin/ax ssh dsh-goal -- env \| grep -E 'DEEPSEEK_API_KEY\|AX_MODEL_BASE_URL'` | la credencial bajo el nombre declarado y el endpoint |
| 3 | `./bin/ax ssh dsh-goal -- curl -s http://127.0.0.1:80/metadata/v1alpha1/ax/model` | el `Model`, sin valor de secreto |
| 4 | `./bin/ax ssh dsh-goal -- sh -c 'echo x > /etc/x'` | **falla** (Substrate); `sh -c 'echo ok > /workspace/ok'` **funciona** |
| 5 | Aplica un workspace **sin** `harness` y ejecuta su tarea | comportamiento Antigravity idéntico al de antes |
| 6 | Rota la clave (`kubectl create secret ... --dry-run=client -o yaml \| kubectl apply -f -`) y reprovisiona | el nuevo valor aparece en el contenedor, sin tocar código ni manifiestos |

- [ ] 1 · `settings.yaml` dentro del sandbox
- [ ] 2 · credencial y endpoint en el entorno
- [ ] 3 · ruta `/model` accesible desde dentro
- [ ] 4 · aislamiento (fuera de `/workspace` falla, dentro funciona)
- [ ] 5 · path Antigravity intacto
- [ ] 6 · rotación de clave sin cambios

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
| SC-001 goal DSH end-to-end | §3.3 | | |
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
