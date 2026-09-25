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
	"fmt"
	"sort"
	"strings"
)

// Wire protocols a Model's endpoint can speak. ModelSpec.api names one of these;
// when it is unset the provider's default applies. The protocol is what decides
// how a request is shaped, which is why it is separate from the provider family:
// an OpenAI-compatible service may only implement the Responses API, and a
// gateway may speak the Anthropic Messages protocol in front of another vendor.
const (
	// APIOpenAICompletions is the OpenAI-compatible chat-completions API.
	APIOpenAICompletions = "openai-completions"
	// APIOpenAIResponses is the OpenAI-compatible Responses API.
	APIOpenAIResponses = "openai-responses"
	// APIAnthropicMessages is the Anthropic Messages API.
	APIAnthropicMessages = "anthropic-messages"
	// APIGoogleGenerateContent is the Gemini generateContent API.
	APIGoogleGenerateContent = "google-generate-content"
)

// defaultAPIs maps a provider to the protocol it speaks when a Model leaves api
// unset, preserving the behavior of manifests written before the field existed.
var defaultAPIs = map[string]string{
	ProviderGoogle:    APIGoogleGenerateContent,
	ProviderOpenAI:    APIOpenAICompletions,
	ProviderAnthropic: APIAnthropicMessages,
}

// KnownAPIs returns the sorted list of protocols a Model may declare.
func KnownAPIs() []string {
	apis := []string{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages, APIGoogleGenerateContent}
	sort.Strings(apis)
	return apis
}

// IsKnownAPI reports whether api names a protocol this build knows.
func IsKnownAPI(api string) bool {
	switch strings.ToLower(strings.TrimSpace(api)) {
	case APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages, APIGoogleGenerateContent:
		return true
	default:
		return false
	}
}

// DefaultAPI returns the protocol a provider speaks when a Model does not declare
// one, or "" when the provider is unknown. The empty provider is the default
// provider (google), matching the rest of the client.
func DefaultAPI(provider string) string {
	key := strings.ToLower(strings.TrimSpace(provider))
	if key == "" {
		key = ProviderGoogle
	}
	return defaultAPIs[key]
}

// ResolveAPI returns the protocol a Model speaks: api when set, otherwise the
// provider default. An unknown api, or an empty api with an unknown provider, is
// an error so callers fail closed instead of guessing.
func ResolveAPI(provider, api string) (string, error) {
	if api = strings.ToLower(strings.TrimSpace(api)); api != "" {
		if !IsKnownAPI(api) {
			return "", fmt.Errorf("unknown api %q (valid protocols: %s)", api, strings.Join(KnownAPIs(), ", "))
		}
		return api, nil
	}
	if def := DefaultAPI(provider); def != "" {
		return def, nil
	}
	return "", fmt.Errorf("cannot tell which protocol provider %q speaks; set spec.api to one of: %s",
		provider, strings.Join(KnownAPIs(), ", "))
}

// ValidateAPI rejects an api naming no known protocol. The empty string is valid
// and means "whatever the provider speaks by default". It is called by the API
// server before saving, so a typo fails at apply time.
func ValidateAPI(api string) error {
	if api = strings.TrimSpace(api); api == "" || IsKnownAPI(api) {
		return nil
	}
	return fmt.Errorf("unknown api %q (valid protocols: %s)", api, strings.Join(KnownAPIs(), ", "))
}

// adapterForAPI maps a protocol onto the control-plane adapter implementing it,
// and reports whether this build has one. The control plane speaks the chat
// protocols its own components need; the Responses API is supported by the agent
// harness, not by the workspace planner, and a Model that declares it fails with
// a typed error instead of being sent to the wrong adapter.
func adapterForAPI(api string) (string, bool) {
	switch api {
	case APIOpenAICompletions:
		return ProviderOpenAI, true
	case APIAnthropicMessages:
		return ProviderAnthropic, true
	case APIGoogleGenerateContent:
		return ProviderGoogle, true
	default:
		return "", false
	}
}
