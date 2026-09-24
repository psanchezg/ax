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

package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/ax/pkg/apis/v1alpha1"
	"gopkg.in/yaml.v3"
)

const (
	// DSHHomeEnv is where DeepSeek Harness keeps profiles and session state.
	DSHHomeEnv = "DSH_HOME"
	// dshDataDir is the durable, non-workspace location for DSH state under
	// AXDir, so session history survives suspend and resume and /workspace stays
	// the agent's only file surface.
	dshDataDir = "dsh"

	// DSHPermissionModeEnv drives DSH's own sandbox and approval layers.
	DSHPermissionModeEnv = "DSH_PERMISSION_MODE"
	// dshPermissionMode turns both off. Agent Substrate is the single isolation
	// boundary for an AX task, so a second layer would only leave the agent
	// blocked on approvals nobody can answer. It is set by the harness and
	// cannot be overridden through spec.harness.env.
	dshPermissionMode = "danger-full-access"

	// ModelYAMLEnv carries the bound Model into the task container.
	ModelYAMLEnv = "AX_MODEL_YAML"

	// dshHeadlessProfile is the shipped one-shot profile. The DSH image installs
	// the CLI globally, so the profile auto-initializes from the shipped
	// templates on first use without registry access; AX adds only the provider
	// binding in $DSH_HOME/settings.yaml.
	dshHeadlessProfile = "headless"
	// dshSettingsFilename is the provider binding DSH reads from $DSH_HOME.
	dshSettingsFilename = "settings.yaml"

	// dshDefaultGoogleBaseURL is Gemini's OpenAI-compatible surface, used when a
	// google Model declares no baseURL and is driven through DSH.
	dshDefaultGoogleBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
)

// deepseekHarness runs DeepSeek Harness (`dsh`) as the task command: the runner
// invokes `dsh --profile headless <prompt>` and this Setup gives it the
// provider binding it needs.
type deepseekHarness struct{}

// Kind implements Harness.
func (deepseekHarness) Kind() string { return HarnessDeepSeekHarness }

// Setup implements Harness. It writes the minimal llm-pi-ai provider binding
// derived from the bound Model and fixes the environment DSH runs with. It
// generates no other DSH configuration: patches, presets, AGENTS.md, and skill
// materialization are manifest-driven curation that AX does not do yet.
func (deepseekHarness) Setup(ctx context.Context, spec *v1alpha1.AgentHarness, goal, workspacePath string) (bool, error) {
	home := os.Getenv(DSHHomeEnv)
	if home == "" {
		home = filepath.Join(AXDir, dshDataDir)
	}
	if err := os.MkdirAll(home, dirPerm); err != nil {
		return false, fmt.Errorf("creating DSH home %s: %w", home, err)
	}

	model := BoundModel()
	if model == nil {
		slog.Info("no Model bound; leaving DSH provider configuration to the image profile",
			"home", home, "workspace", workspacePath)
	} else {
		settings, err := dshSettings(model)
		if err != nil {
			return false, err
		}
		settingsPath := filepath.Join(home, dshSettingsFilename)
		if err := os.WriteFile(settingsPath, settings, filePerm); err != nil {
			return false, fmt.Errorf("writing %s: %w", settingsPath, err)
		}
		slog.Info("wrote DSH provider binding",
			"model", model.GetMetadata().GetName(), "provider", model.GetSpec().GetProvider(),
			"path", settingsPath)
	}

	if err := os.Setenv(DSHHomeEnv, home); err != nil {
		return false, fmt.Errorf("setting %s: %w", DSHHomeEnv, err)
	}
	if err := os.Setenv(DSHPermissionModeEnv, dshPermissionMode); err != nil {
		return false, fmt.Errorf("setting %s: %w", DSHPermissionModeEnv, err)
	}
	for _, e := range spec.GetEnv() {
		name := e.GetName()
		if name == "" || name == DSHHomeEnv || name == DSHPermissionModeEnv {
			// The isolation and home contract is not user-overridable.
			continue
		}
		if err := os.Setenv(name, e.GetValue()); err != nil {
			return false, fmt.Errorf("setting %s: %w", name, err)
		}
	}

	return false, nil
}

// BoundModel returns the Model resource passed in AX_MODEL_YAML, or nil when the
// task has no model binding.
func BoundModel() *v1alpha1.Model {
	raw := os.Getenv(ModelYAMLEnv)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var m v1alpha1.Model
	if err := yaml.Unmarshal([]byte(raw), &m); err != nil {
		slog.Warn("ignoring unparsable AX_MODEL_YAML", "error", err)
		return nil
	}
	if m.GetMetadata() == nil {
		return nil
	}
	return &m
}

// dshSettings renders the llm-pi-ai provider binding for a bound Model
// (docs/deepseek-harness-adaptation.md §5.5): one provider entry named after the
// Model, the credential variable DSH resolves from the environment, the API
// protocol that matches AX's provider adapter, the endpoint, and the model id.
func dshSettings(m *v1alpha1.Model) ([]byte, error) {
	provider := strings.ToLower(strings.TrimSpace(m.GetSpec().GetProvider()))
	api, err := dshAPI(provider)
	if err != nil {
		return nil, err
	}

	baseURL := m.GetSpec().GetBaseUrl()
	if baseURL == "" && (provider == "" || provider == "google") {
		baseURL = dshDefaultGoogleBaseURL
	}

	entry := map[string]any{
		"api": api,
		"models": []map[string]any{{
			"id":   m.GetSpec().GetModel(),
			"name": m.GetSpec().GetModel(),
		}},
	}
	if key := m.GetSpec().GetSecretKey().GetKey(); key != "" {
		entry["apiKeyEnv"] = key
	}
	if baseURL != "" {
		entry["baseURL"] = baseURL
	}

	doc := map[string]any{
		"llm-pi-ai": map[string]any{
			"providers": map[string]any{
				m.GetMetadata().GetName(): entry,
			},
		},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshaling DSH settings for model %q: %w", m.GetMetadata().GetName(), err)
	}
	return out, nil
}

// dshAPI maps an AX provider name onto the dsh-llm-pi-ai protocol that speaks
// it. Gemini is reached through its OpenAI-compatible surface.
func dshAPI(provider string) (string, error) {
	switch provider {
	case "", "google", "openai":
		return "openai-completions", nil
	case "anthropic":
		return "anthropic-messages", nil
	default:
		return "", fmt.Errorf("provider %q has no DeepSeek Harness mapping", provider)
	}
}
