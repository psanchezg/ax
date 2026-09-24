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

- [ ] `make build` sin errores

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

- [ ] Suite verde

### 1.2 Los manifiestos del repo siguen siendo válidos

```bash
go test -count=1 ./pkg/apis/... -run TestExamples -v
```

Esperado: PASS para `model-deepseek.yaml`, `model-local-qwen.yaml`, `simple.yaml`,
`task-dsh-demo.yaml` (sus 2 documentos) y `task.yaml`. Prueba: ningún ejemplo se ha
quedado obsoleto respecto al esquema estricto.

- [ ] PASS

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

Esperado en `settings.yaml`:

```yaml
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

- [ ] `/readyz` responde `ok`
- [ ] `/metadata/v1alpha1/ax/model` sirve el `Model` sin valor de secreto
- [ ] `settings.yaml` generado como arriba
- [ ] El comando de la tarea se ejecutó

### 1.4 El único supuesto externo: el `dsh` real entiende ese `settings.yaml`

```bash
DSH_HOME=/tmp/ax-runbook/dsh DSH_PERMISSION_MODE=danger-full-access \
  dsh --profile headless "lista los ficheros del workspace y di qué ves"
```

Esperado: DSH arranca, resuelve la credencial por `apiKeyEnv`, llama a tu endpoint
(DeepSeek en el ejemplo) y termina con una respuesta en stdout. Prueba: el formato del
binding que generamos es correcto y el provider responde.

Si falla: `MISSING_CREDENTIAL` → exporta `DEEPSEEK_API_KEY` en el shell; error HTTP del
proveedor → la clave o el `baseURL`; timeout → endpoint/egress de tu red.

- [ ] DSH responde usando el binding generado

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
cat <<'EOF' | ./bin/ax apply -f -
apiVersion: ax.io/v1alpha1
kind: Model
metadata: {name: typo-model, atespace: default}
spec: {provider: gemini-flash, model: x}
EOF
# Esperado: salida no-cero, y el mensaje nombra google, openai, anthropic:
#   Error: applying document 1: rpc error: code = InvalidArgument desc =
#   invalid model: unknown provider "gemini-flash" (valid providers: anthropic, google, openai)
# Ojo: ese apply devuelve 1 a propósito; si tu shell tiene `set -e`, envuélvelo en `|| true`.

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

- [ ] Provider con typo rechazado nombrando los válidos
- [ ] `Model`, `Workspace` y `Task` creados y leídos

---

## 3. Nivel 2 — sandbox real (Kubernetes + Agent Substrate)

El único nivel que prueba el objetivo completo. **Substrate es un proyecto aparte**: AX
no lo instala (`deploy/` solo trae Redis, server y controller). Sin una Control API
alcanzable en `api.ate-system.svc.cluster.local:443`, este nivel no arranca.

### 3.1 Clúster y AX

**Opción A (recomendada): kubeconfig dedicado.** Tu configuración normal no se toca —ni
el contexto activo, ni la lista de clústeres—, y todo lo de este nivel queda contenido en
un fichero temporal que borras al final:

```bash
export KUBECONFIG=/tmp/ax-test.kubeconfig   # este shell ya no ve tu clúster remoto
kind create cluster --name ax-test          # escribe el contexto en ESE fichero
# equivalente explícito: kind create cluster --name ax-test --kubeconfig /tmp/ax-test.kubeconfig
kubectl config current-context              # → kind-ax-test
kubectl cluster-info >/dev/null && echo "cluster ok"
```

`KUBECONFIG` es por shell: las secciones §3.3, §3.4 y §6 deben ejecutarse en el mismo
shell (o reexportar la variable). Si abres otra terminal, repite el `export` antes de
cualquier `kubectl` o `bin/ax`.

**Opción B: usar tu kubeconfig y cambiar de contexto.** Válida, pero tu contexto activo
cambia y hay que acordarse de volver:

```bash
kind create cluster --name ax-test
kubectl config use-context kind-ax-test
CTX="$(kubectl config current-context)"
if [ "$CTX" = "kind-ax-test" ]; then echo "ok: $CTX"; else echo "ABORTA: estás en $CTX, no en kind-ax-test"; fi
```

Si la guardia imprime `ABORTA`, cambia de contexto antes de seguir. Recuerda que `bin/ax`
también resuelve el servidor por el contexto activo (hace port-forward a `ax-system`): un
`ax` sin `--server` desde el contexto remoto apuntaría al AX de *ese* clúster.

Después, en cualquiera de las dos opciones:

```bash
# Instala Agent Substrate en ESTE clúster y comprueba su Control API:
kubectl get svc -n ate-system

export AX_IMAGE_REPO=$REGISTRY        # ko publica aquí las imágenes del plano de control
make deploy                           # redis + controller + server en ax-system
kubectl get pods -n ax-system
```

- [ ] `kubectl config current-context` = `kind-ax-test`
- [ ] Substrate accesible
- [ ] Pods de `ax-system` en `Running`

### 3.2 Imagen del runner con DSH

```bash
make build-task-runner                # bin/linux_amd64/ax-task-runner
docker build --platform linux/amd64 -f Dockerfile.task-runner-dsh -t "$DSH_IMAGE" .
docker push "$DSH_IMAGE"
```

- [ ] Imagen publicada

### 3.3 Recursos de prueba

```bash
kubectl create secret generic deepseek-api-secret \
  --from-literal=DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY_SRC"

# Edita la imagen en el ejemplo antes de aplicarlo:
sed -i '' "s|ghcr.io/<org>/ax-dsh-runner:1|$DSH_IMAGE|" examples/workspace-dsh.yaml

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
kind delete cluster --name ax-test
docker rmi "$DSH_IMAGE" 2>/dev/null
git checkout -- examples/workspace-dsh.yaml    # revierte la imagen que editaste
rm -rf /tmp/ax-runbook

# Según la opción que elegiste en §3.1:
unset KUBECONFIG
rm -f /tmp/ax-test.kubeconfig                  # Opción A: nada quedó en tu kubeconfig
# kubectl config use-context "$ORIG_CTX"       # Opción B: vuelve a tu contexto de siempre
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
