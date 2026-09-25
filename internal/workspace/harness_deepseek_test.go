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

package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/ax/internal/workspace"
	"github.com/google/ax/pkg/apis/v1alpha1"
	"gopkg.in/yaml.v3"
)

// dshModelYAML renders a Model the way the controller injects it.
func dshModelYAML(t *testing.T, spec *v1alpha1.ModelSpec) string {
	t.Helper()
	m := &v1alpha1.Model{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindModel,
		Metadata:   &v1alpha1.ObjectMeta{Name: "deepseek", Atespace: "default"},
		Spec:       spec,
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// runDSHSetup runs the deepseek harness with an isolated DSH home and returns
// the written settings document, or "" when none was written.
func runDSHSetup(t *testing.T, modelYAML string, spec *v1alpha1.AgentHarness) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "dsh")
	// Register the original values for restoration, then let Setup set them.
	t.Setenv(workspace.DSHHomeEnv, home)
	t.Setenv(workspace.DSHPermissionModeEnv, "workspace-write")
	t.Setenv(workspace.ModelYAMLEnv, modelYAML)

	h, err := workspace.ResolveHarness(workspace.HarnessDeepSeekHarness)
	if err != nil {
		t.Fatal(err)
	}
	wsPath := t.TempDir()
	if _, err := h.Setup(context.Background(), spec, "prepare this workspace", wsPath); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if got := os.Getenv(workspace.DSHPermissionModeEnv); got != "danger-full-access" {
		t.Errorf("expected the harness to own the permission mode, got %q", got)
	}
	if got := os.Getenv(workspace.DSHHomeEnv); got != home {
		t.Errorf("expected DSH_HOME %q, got %q", home, got)
	}

	settingsPath := filepath.Join(home, "settings.yaml")
	data, err := os.ReadFile(settingsPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	// Harness state belongs on the durable volume, never in the workspace.
	if _, err := os.Stat(filepath.Join(wsPath, "settings.yaml")); err == nil {
		t.Error("settings.yaml must not be written into the workspace")
	}
	return string(data)
}

func TestDeepSeekHarness_Settings(t *testing.T) {
	tests := []struct {
		name         string
		spec         *v1alpha1.ModelSpec
		wantAPI      string
		wantBaseURL  string
		wantAPIKeyEn string
	}{
		{
			name: "openai-compatible provider",
			spec: &v1alpha1.ModelSpec{
				Provider:  "openai",
				Model:     "deepseek-chat",
				BaseUrl:   "https://api.deepseek.com/v1",
				SecretKey: &v1alpha1.SecretKeyRef{Name: "deepseek-api-secret", Key: "DEEPSEEK_API_KEY"},
			},
			wantAPI:      "openai-completions",
			wantBaseURL:  "https://api.deepseek.com/v1",
			wantAPIKeyEn: "DEEPSEEK_API_KEY",
		},
		{
			name: "anthropic messages",
			spec: &v1alpha1.ModelSpec{
				Provider:  "anthropic",
				Model:     "claude-sonnet-4-5",
				SecretKey: &v1alpha1.SecretKeyRef{Name: "anthropic-api-secret", Key: "ANTHROPIC_API_KEY"},
			},
			wantAPI:      "anthropic-messages",
			wantAPIKeyEn: "ANTHROPIC_API_KEY",
		},
		{
			name: "google through its openai-compatible surface",
			spec: &v1alpha1.ModelSpec{
				Provider:  "google",
				Model:     "gemini-3.8-flash",
				SecretKey: &v1alpha1.SecretKeyRef{Name: "gemini-api-secret", Key: "GEMINI_API_KEY"},
			},
			wantAPI:      "openai-completions",
			wantBaseURL:  "https://generativelanguage.googleapis.com/v1beta/openai/",
			wantAPIKeyEn: "GEMINI_API_KEY",
		},
		{
			// The protocol is what the endpoint speaks, not what the provider
			// family implies: an OpenAI-compatible service that only implements
			// the Responses API is reachable by declaring it.
			name: "responses api on an openai-compatible route",
			spec: &v1alpha1.ModelSpec{
				Provider:  "openai",
				Api:       "openai-responses",
				Model:     "mimo-v2.5-tts",
				BaseUrl:   "https://api.example.com/v1",
				SecretKey: &v1alpha1.SecretKeyRef{Name: "example-secret", Key: "EXAMPLE_API_KEY"},
			},
			wantAPI:      "openai-responses",
			wantBaseURL:  "https://api.example.com/v1",
			wantAPIKeyEn: "EXAMPLE_API_KEY",
		},
		{
			// Same idea in the other direction: keep the provider family the
			// credential belongs to and speak the Messages protocol through a
			// compatible gateway.
			name: "anthropic protocol over an openai-compatible family",
			spec: &v1alpha1.ModelSpec{
				Provider:  "openai",
				Api:       "anthropic-messages",
				Model:     "some-model",
				BaseUrl:   "https://gateway.example.com",
				SecretKey: &v1alpha1.SecretKeyRef{Name: "gateway-secret", Key: "GATEWAY_API_KEY"},
			},
			wantAPI:      "anthropic-messages",
			wantBaseURL:  "https://gateway.example.com",
			wantAPIKeyEn: "GATEWAY_API_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := runDSHSetup(t, dshModelYAML(t, tt.spec), &v1alpha1.AgentHarness{})
			if raw == "" {
				t.Fatal("expected a settings.yaml to be written")
			}

			var doc map[string]any
			if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatalf("settings.yaml is not valid yaml: %v\n%s", err, raw)
			}
			providers := doc["llm-pi-ai"].(map[string]any)["providers"].(map[string]any)
			entry, ok := providers["deepseek"].(map[string]any)
			if !ok {
				t.Fatalf("expected a provider entry named after the Model:\n%s", raw)
			}
			if entry["api"] != tt.wantAPI {
				t.Errorf("api = %v, want %q", entry["api"], tt.wantAPI)
			}
			if entry["apiKeyEnv"] != tt.wantAPIKeyEn {
				t.Errorf("apiKeyEnv = %v, want %q", entry["apiKeyEnv"], tt.wantAPIKeyEn)
			}
			if tt.wantBaseURL != "" && entry["baseURL"] != tt.wantBaseURL {
				t.Errorf("baseURL = %v, want %q", entry["baseURL"], tt.wantBaseURL)
			}
			if _, present := entry["baseURL"]; present && tt.wantBaseURL == "" {
				t.Errorf("expected no baseURL for an endpoint-less Model, got %v", entry["baseURL"])
			}
			models, ok := entry["models"].([]any)
			if !ok || len(models) != 1 {
				t.Fatalf("expected one model entry, got %v", entry["models"])
			}
			if got := models[0].(map[string]any)["id"]; got != tt.spec.GetModel() {
				t.Errorf("model id = %v, want %q", got, tt.spec.GetModel())
			}
			// Registering the route is not enough: the agent must also be pointed
			// at it, or the profile's own default adapter serves the request.
			defaultModel, ok := doc["agent-default-model"].(map[string]any)
			if !ok {
				t.Fatalf("expected an agent-default-model section:\n%s", raw)
			}
			if defaultModel["provider"] != "deepseek" {
				t.Errorf("default provider = %v, want the Model-named route", defaultModel["provider"])
			}
			if defaultModel["model"] != tt.spec.GetModel() {
				t.Errorf("default model = %v, want %q", defaultModel["model"], tt.spec.GetModel())
			}
			// Only the provider binding is generated: no patches, presets, or
			// AGENTS.md (manifest-driven curation is not implemented yet).
			for _, key := range []string{"runtime.patch.yml", "agent-presets", "AGENTS.md"} {
				if strings.Contains(raw, key) {
					t.Errorf("settings.yaml must not emit %q:\n%s", key, raw)
				}
			}
		})
	}
}

func TestDeepSeekHarness_NoModelBound(t *testing.T) {
	if raw := runDSHSetup(t, "", &v1alpha1.AgentHarness{}); raw != "" {
		t.Errorf("expected no settings.yaml without a bound Model, got:\n%s", raw)
	}
}

func TestDeepSeekHarness_EnvContract(t *testing.T) {
	home := filepath.Join(t.TempDir(), "dsh")
	overrideHome := filepath.Join(t.TempDir(), "elsewhere")
	t.Setenv(workspace.DSHHomeEnv, home)
	t.Setenv(workspace.DSHPermissionModeEnv, "workspace-write")
	t.Setenv(workspace.ModelYAMLEnv, "")

	h, err := workspace.ResolveHarness(workspace.HarnessDeepSeekHarness)
	if err != nil {
		t.Fatal(err)
	}
	spec := &v1alpha1.AgentHarness{
		Env: []*v1alpha1.EnvVar{
			{Name: "DSH_LOG_LEVEL", Value: "debug"},
			// The isolation and home contract is not user-overridable.
			{Name: workspace.DSHPermissionModeEnv, Value: "workspace-write"},
			{Name: workspace.DSHHomeEnv, Value: overrideHome},
		},
	}
	if _, err := h.Setup(context.Background(), spec, "", t.TempDir()); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if got := os.Getenv("DSH_LOG_LEVEL"); got != "debug" {
		t.Errorf("expected harness.env to be applied, got %q", got)
	}
	if got := os.Getenv(workspace.DSHPermissionModeEnv); got != "danger-full-access" {
		t.Errorf("harness.env must not weaken the isolation contract, got %q", got)
	}
	if got := os.Getenv(workspace.DSHHomeEnv); got != home {
		t.Errorf("harness.env must not move DSH_HOME, got %q", got)
	}
}

func TestDeepSeekHarness_UnsupportedProviderMapping(t *testing.T) {
	h, err := workspace.ResolveHarness(workspace.HarnessDeepSeekHarness)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(workspace.DSHHomeEnv, filepath.Join(t.TempDir(), "dsh"))
	t.Setenv(workspace.DSHPermissionModeEnv, "")
	t.Setenv(workspace.ModelYAMLEnv, dshModelYAML(t, &v1alpha1.ModelSpec{Provider: "bedrock", Model: "m"}))

	if _, err := h.Setup(context.Background(), &v1alpha1.AgentHarness{}, "", t.TempDir()); err == nil {
		t.Fatal("expected an error for a provider with no DSH mapping")
	}
}
