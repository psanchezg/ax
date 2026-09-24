// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package model

import (
	"errors"
	"fmt"
)

// Parameter translation.
//
// The free-form `parameters` map stays a pass-through: Gemini receives it
// verbatim, and other providers translate the known Gemini spellings at the
// adapter boundary while unknown keys keep flowing through untouched. That way
// one manifest keeps working across providers, and power users can spell
// native parameter names directly.

// paramMapping translates one known `parameters` key to a provider's wire name.
type paramMapping struct {
	from, to string
}

// openAIParamTable maps Gemini-style spellings onto the OpenAI-compatible
// chat-completions body. Ordered: when several sources map to the same wire
// name, the first one wins deterministically.
var openAIParamTable = []paramMapping{
	{"maxOutputTokens", "max_tokens"},
	{"maxTokens", "max_tokens"},
	{"temperature", "temperature"},
	{"topP", "top_p"},
	{"topK", "top_k"},
	{"stopSequences", "stop"},
	{"candidateCount", "n"},
}

// anthropicParamTable maps Gemini-style spellings onto the Messages API body.
var anthropicParamTable = []paramMapping{
	{"maxOutputTokens", "max_tokens"},
	{"maxTokens", "max_tokens"},
	{"temperature", "temperature"},
	{"topP", "top_p"},
	{"topK", "top_k"},
	{"stopSequences", "stop_sequences"},
}

// anthropicUnsupportedParams are `parameters` keys with no Messages API
// equivalent. Carrying one is a typed error rather than a silent drop.
var anthropicUnsupportedParams = map[string]error{
	"candidateCount": errors.New("candidateCount is not supported: the anthropic Messages API generates one completion per request"),
}

// translateParameters maps known spellings through table and passes every
// other key through verbatim. Keys carrying the system instruction are lifted
// out (they are not generation parameters). Native wire names already present
// in params win over their translated aliases.
func translateParameters(provider string, params map[string]any, table []paramMapping, unsupported map[string]error) (map[string]any, error) {
	out := make(map[string]any, len(params))
	if params == nil {
		return out, nil
	}
	for k, v := range params {
		if k == systemInstructionParam {
			continue
		}
		if err, bad := unsupported[k]; bad {
			return nil, &ProviderError{Provider: provider, Err: fmt.Errorf("parameter %q: %w", k, err)}
		}
		out[k] = v
	}
	for _, m := range table {
		v, ok := params[m.from]
		if !ok {
			continue
		}
		if _, exists := out[m.to]; !exists {
			out[m.to] = v
		}
		if m.from != m.to {
			delete(out, m.from)
		}
	}
	return out, nil
}

// openAIParameters translates `parameters` for the OpenAI-compatible adapter.
func openAIParameters(params map[string]any) (map[string]any, error) {
	return translateParameters(ProviderOpenAI, params, openAIParamTable, nil)
}

// anthropicParameters translates `parameters` for the Messages API adapter.
func anthropicParameters(params map[string]any) (map[string]any, error) {
	return translateParameters(ProviderAnthropic, params, anthropicParamTable, anthropicUnsupportedParams)
}
