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

package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/ax/internal/model"
)

func TestResolveAPI(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		api      string
		want     string
		wantErr  bool
	}{
		{name: "empty provider keeps the google default", want: model.APIGoogleGenerateContent},
		{name: "google default", provider: "google", want: model.APIGoogleGenerateContent},
		{name: "openai default", provider: "openai", want: model.APIOpenAICompletions},
		{name: "anthropic default", provider: "anthropic", want: model.APIAnthropicMessages},
		{
			name:     "explicit protocol overrides the provider default",
			provider: "openai",
			api:      model.APIOpenAIResponses,
			want:     model.APIOpenAIResponses,
		},
		{
			name:     "provider family and protocol are independent",
			provider: "openai",
			api:      model.APIAnthropicMessages,
			want:     model.APIAnthropicMessages,
		},
		{name: "unknown protocol", provider: "openai", api: "openai-chat", wantErr: true},
		{name: "unknown provider without a protocol", provider: "bedrock", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.ResolveAPI(tt.provider, tt.api)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveAPI(%q, %q) = %q, want %q", tt.provider, tt.api, got, tt.want)
			}
		})
	}
}

func TestValidateAPI(t *testing.T) {
	for _, ok := range []string{"", "openai-completions", "openai-responses", "anthropic-messages", "google-generate-content", " OpenAI-Responses "} {
		if err := model.ValidateAPI(ok); err != nil {
			t.Errorf("expected %q to validate, got %v", ok, err)
		}
	}
	err := model.ValidateAPI("openai-chat")
	if err == nil {
		t.Fatal("expected an unknown protocol to be rejected")
	}
	for _, want := range []string{"openai-chat", model.APIOpenAIResponses, model.APIAnthropicMessages} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to mention %q, got %v", want, err)
		}
	}
}

// TestGenerate_ProtocolDrivesTheAdapter proves the protocol, not the provider
// family, selects the request shape: the same provider reaches a Messages
// endpoint when the Model says so.
func TestGenerate_ProtocolDrivesTheAdapter(t *testing.T) {
	var gotPath, gotKeyHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKeyHeader = r.Header.Get("x-api-key")
		_, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider: model.ProviderOpenAI, // provider family
		API:      model.APIAnthropicMessages,
		Model:    "some-model",
		BaseURL:  ts.URL,
		APIKey:   "k",
	}, model.WithHTTPClient(ts.Client()))

	resp, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("unexpected content %q", resp.Content)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("expected the Messages protocol, got path %q", gotPath)
	}
	if gotKeyHeader != "k" {
		t.Errorf("expected the Messages auth header, got %q", gotKeyHeader)
	}
}

// TestGenerate_UnsupportedProtocolFailsClosed: the harness speaks the Responses
// API, the control plane does not, and that must be a typed error rather than a
// request to the wrong endpoint.
func TestGenerate_UnsupportedProtocolFailsClosed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP call should be made for a protocol the control plane cannot speak")
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider: model.ProviderOpenAI,
		API:      model.APIOpenAIResponses,
		Model:    "mimo-v2.5-tts",
		BaseURL:  ts.URL,
		APIKey:   "k",
	}, model.WithHTTPClient(ts.Client()))

	_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "hi"})
	var perr *model.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *ProviderError, got %v", err)
	}
	if !strings.Contains(err.Error(), model.APIOpenAIResponses) {
		t.Errorf("expected the error to name the protocol, got %v", err)
	}
}
