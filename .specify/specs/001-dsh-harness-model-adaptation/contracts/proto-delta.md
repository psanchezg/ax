# Contract: `ax.v1alpha1` Proto Delta

**Date**: 2026-09-24 | **Source of truth**: `pkg/apis/v1alpha1/ax.proto`

Additive-only evolution within `ax.v1alpha1` (FR-021). `reserved` slots stay reserved.
Regenerated `pkg/apis/v1alpha1/*.pb.go` lands in the same commit as the `.proto` change
(Constitution II).

## Change 1 — `ModelSpec.base_url`

```proto
message ModelSpec {
  string provider = 1;
  string model = 2;
  reserved 3, 4, 5;
  reserved "goal", "policies";          // existing discipline, untouched
  SecretKeyRef secret_key = 6;
  google.protobuf.Struct parameters = 7;
  string base_url = 8 [json_name = "baseURL"];   // NEW
}
```

| Property | Value |
|---|---|
| Field number | 8 (next free) |
| Proto name | `base_url` |
| JSON/YAML name | `baseURL` (explicit `json_name`; protoc's default would be `baseUrl` — R8) |
| Also accepted on input | `base_url` (protojson dual-spelling) |
| Semantics | HTTP(S) base for the provider endpoint, verbatim (including `/v1` when the provider needs it). Empty ⇒ provider default. |
| Manifest example | `baseURL: https://api.deepseek.com/v1` |

## Change 2 — `AgentHarness` message + `WorkspaceSpec.harness`

```proto
message AgentHarness {
  // kind selects the built-in integration: "antigravity" or "deepseek-harness".
  // Empty means "antigravity" for backward compatibility.
  string kind = 1;
  // image overrides the task runner image for this workspace's tasks.
  string image = 2;
  // command overrides the harness invocation; defaults per kind.
  repeated string command = 3;
  // model_ref names the Model resource whose credentials and endpoint this
  // harness uses.
  string model_ref = 4 [json_name = "modelRef"];
  // env is merged into the harness process environment.
  repeated EnvVar env = 5;
  // system_instructions overrides the harness persona.
  string system_instructions = 6 [json_name = "systemInstructions"];
}

message WorkspaceSpec {
  repeated GitRepo git = 1;
  MCPConfig mcp = 2;
  SkillsConfig skills = 3;
  AgentHarness harness = 4;   // NEW
}
```

| Property | Value |
|---|---|
| `WorkspaceSpec.harness` number | 4 (next free after `skills = 3`) |
| `AgentHarness` numbers | 1–6, first release |
| JSON/YAML block | `harness:` under `spec` |
| EnvVar | existing `ax.v1alpha1.EnvVar` message (`name`, `value`) — no change |
| Semantics | fail-closed resolution (FR-013); unset `kind` preserves today's Antigravity bootstrap exactly |

Manifest example:

```yaml
apiVersion: ax.io/v1alpha1
kind: Workspace
metadata:
  name: dsh-ws
  atespace: default
spec:
  harness:
    kind: deepseek-harness
    image: "ghcr.io/<org>/ax-dsh-runner:<tag>"
    modelRef: deepseek
    env:
      - name: DSH_LOG_LEVEL
        value: info
```

## Wire and tooling compatibility (SC-006)

| Combination | Behavior |
|---|---|
| Old binary ↔ new fields on the wire | Unknown fields round-trip without loss (proto3); no wire errors. |
| New `ax` CLI → old server | Fields accepted client-side, ignored by old server behavior; manifests work with reduced semantics. |
| Old `ax` CLI → manifest with `baseURL`/`harness` | **Client-side parse error** (strict protojson YAML decode, `types.go` — "unknown fields are errors"). Accepted, documented fail-closed skew (R17). |
| New `ax` CLI → new server | Full semantics. |
| Typos in field names | Still rejected at parse time (existing tested behavior preserved). |

Round-trip tests with unknown fields and dual-spelling decode tests (`baseURL` /
`base_url`, `modelRef` / `model_ref`) live in `pkg/apis/v1alpha1/types_test.go`.

## Non-goals (explicitly not in the delta)

- No `ax.v1alpha2` package split (R17).
- No `TaskSpec` change (`spec.command`, `spec.image`, `spec.env` already suffice for
  Phase 0; `spec.env` stays plain `name`/`value`).
- No change to `MCPConfig` / `SkillsConfig` (Phase 4 owns their consumption).
