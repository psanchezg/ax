# Contract: Model Provider Interface (`internal/model`)

**Date**: 2026-09-24 | **Consumers**: `internal/workspace/planner.go` (today), any future
`Model` consumer | **Decisions**: research.md R3–R7, R18

## Go interface

```go
// internal/model/provider.go
package model

// Provider renders one generation against a specific model API.
type Provider interface {
    // Name is the lowercase registry key ("google", "openai", "anthropic").
    Name() string
    // Generate issues a single generation. Failures are always *ProviderError.
    Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)
}

// Register installs a provider factory under a lowercase name.
// Called from provider init() functions.
func Register(name string, factory func(cfg Config) Provider)

// Known returns the sorted list of registered provider names (apply-time
// validation source of truth, FR-007).
func Known() []string

// NewClient resolves cfg.Provider (strings.ToLower; "" ≡ "google") through the
// registry and returns the built client. Unknown ⇒ typed error listing Known().
func NewClient(cfg Config) (*Client, error)
```

```go
type GenerateRequest struct {
    Prompt     string
    Parameters map[string]any // free-form, pass-through (FR-005)
    Model      string         // model id
}

type GenerateResponse struct {
    Text     string
    Provider string // echo, for logs/telemetry
    Model    string // echo
}
```

`Config` gains `BaseURL` population from `ModelSpec.base_url` in `ConfigFromSpec`
(FR-004). All HTTP calls take the caller's `context.Context` and honor its cancellation
(Constitution V).

## Error taxonomy (FR-006, SC-002)

```go
// ProviderError is the ONLY failure shape returned by Generate.
type ProviderError struct {
    Provider   string // "openai", ...
    StatusCode int    // HTTP status, 0 for transport/local errors
    Body       string // provider response body (truncated to a bounded size)
    Err        error  // underlying cause (wrapped, %w)
}
func (e *ProviderError) Error() string
func (e *ProviderError) Unwrap() error
```

| Trigger | Result |
|---|---|
| Empty/missing API key | `*ProviderError{StatusCode: 0}` — `missing API key` |
| HTTP 4xx/5xx | `*ProviderError{StatusCode, Body}` — no fallback text |
| Transport failure/timeout | `*ProviderError{Err: <wrapped>}` |
| Malformed/unexpected response | `*ProviderError{Err: <wrapped shape error>}` |
| `DisableRemote` test opt-in | Deterministic canned response — **the only path that can return text without a real call** (explicit test opt-in, never a production fallback) |

**Invariants**: no fabricated completion ever leaves `Generate` without the
`DisableRemote` opt-in; a wrong key is indistinguishable from an outage but never from
success.

## Registered providers

| `Name()` | Wire API | Endpoint | Credential |
|---|---|---|---|
| `google` (default for `""`) | Gemini `generateContent` (unchanged, `generationConfig` verbatim) | `Config.BaseURL` or built-in default | query-string key (unchanged) |
| `openai` | `POST {baseURL}/chat/completions` | `ModelSpec.base_url` (required for self-hosted; provider default only where documented) | `Authorization: Bearer <key>` |
| `anthropic` | `POST {baseURL or https://api.anthropic.com}/v1/messages` | `ModelSpec.base_url` optional | `x-api-key: <key>` + `anthropic-version: 2023-06-01` |

### `openai` request/response mapping

- Request: `{"model": <Model>, "messages": [{"role": "user", "content": <Prompt>}], <translated params>}`
- Response: `choices[0].message.content` → `GenerateResponse.Text`

### `anthropic` request/response mapping

- Request: `{"model": <Model>, "max_tokens": <4096 default>, "messages": [{"role": "user", "content": <Prompt>}], <translated params>}`
- Response: first `content[]` block with `type == "text"` → `GenerateResponse.Text`

## Parameter translation (R5)

`parameters` keys are translated at the adapter boundary; unknown keys pass through
verbatim. Gemini spellings keep working against any provider.

| `parameters` key | google | openai | anthropic |
|---|---|---|---|
| `maxOutputTokens` / `maxTokens` | `maxOutputTokens` | `max_tokens` | `max_tokens` |
| `temperature` | `temperature` | `temperature` | `temperature` |
| `topP` | `topP` | `top_p` | `top_p` |
| `topK` | `topK` | `top_k` | `top_k` |
| `stopSequences` | `stopSequences` | `stop` | `stop_sequences` |
| `candidateCount` | `candidateCount` | `n` | *(unsupported — typed error)* |
| anything else | verbatim | verbatim | verbatim |

## Registry ↔ apply-time validation (FR-007)

`internal/server.UpdateModel` MUST call `model.Known()` semantics: `spec.provider`
lowercased must exist in the registry (empty ≡ `google`), else reject the apply with a
message naming the valid values. Validation never blocks on network I/O.

## Test matrix (Constitution III)

- Registry: register/lookup/duplicate-name/unknown-provider table tests.
- Each adapter: `net/http/httptest` covering 2xx mapping, 4xx/5xx → `*ProviderError`
  with status+body, empty key short-circuit, context cancellation, malformed body.
- Translation tables: input-map → wire-body assertions per provider.
- Fallback regression: with the opt-in unset, every failure path yields an error and
  zero fabricated text (SC-002).
