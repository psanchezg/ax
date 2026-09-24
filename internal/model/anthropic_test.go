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

func TestAnthropic_Generate(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "thinking", "thinking": "hmm"},
				{"type": "text", "text": "workspace prepared"},
			},
			"usage": map[string]int{"input_tokens": 7, "output_tokens": 5},
		})
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider: model.ProviderAnthropic,
		Model:    "claude-sonnet-4-5",
		BaseURL:  ts.URL,
		APIKey:   "test-key",
		Parameters: map[string]any{
			"stopSequences": []any{"END"},
		},
	}, model.WithHTTPClient(ts.Client()))

	resp, err := client.Generate(context.Background(), &model.GenerateRequest{
		Prompt:            "Set up this workspace",
		SystemInstruction: "Be terse.",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "workspace prepared" {
		t.Errorf("unexpected content %q", resp.Content)
	}
	if resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 12 {
		t.Errorf("unexpected usage %+v", resp.Usage)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("unexpected path %q", gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("unexpected x-api-key %q", gotKey)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("unexpected anthropic-version %q", gotVersion)
	}
	if gotBody["system"] != "Be terse." {
		t.Errorf("expected system instruction lifted out, got %v", gotBody)
	}
	if gotBody["max_tokens"] != float64(4096) {
		t.Errorf("expected default max_tokens 4096, got %v", gotBody["max_tokens"])
	}
	if gotBody["stop_sequences"] == nil || gotBody["stopSequences"] != nil {
		t.Errorf("expected stopSequences translated to stop_sequences, got %v", gotBody)
	}
	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected a single user message, got %v", gotBody["messages"])
	}
}

func TestAnthropic_ExplicitMaxTokens(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider:   model.ProviderAnthropic,
		Model:      "m",
		BaseURL:    ts.URL,
		APIKey:     "k",
		Parameters: map[string]any{"maxOutputTokens": float64(1234)},
	}, model.WithHTTPClient(ts.Client()))

	if _, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if gotBody["max_tokens"] != float64(1234) {
		t.Errorf("expected translated max_tokens 1234, got %v", gotBody["max_tokens"])
	}
}

func TestAnthropic_UnsupportedParameter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("expected no HTTP call for an unsupported parameter")
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider:   model.ProviderAnthropic,
		Model:      "m",
		BaseURL:    ts.URL,
		APIKey:     "k",
		Parameters: map[string]any{"candidateCount": float64(2)},
	}, model.WithHTTPClient(ts.Client()))

	_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
	var perr *model.ProviderError
	if !errors.As(err, &perr) || !strings.Contains(err.Error(), "candidateCount") {
		t.Fatalf("expected typed unsupported-parameter error, got %v", err)
	}
}

func TestAnthropic_TypedErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`overloaded`))
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider: model.ProviderAnthropic,
		Model:    "m",
		BaseURL:  ts.URL,
		APIKey:   "k",
	}, model.WithHTTPClient(ts.Client()))

	_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
	var perr *model.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *ProviderError, got %v", err)
	}
	if perr.StatusCode != http.StatusServiceUnavailable || !strings.Contains(perr.Body, "overloaded") {
		t.Errorf("expected status+body carried, got %+v", perr)
	}

	// Empty key short-circuits before any HTTP call.
	empty := model.NewClient(model.Config{
		Provider: model.ProviderAnthropic,
		Model:    "m",
		BaseURL:  ts.URL,
	}, model.WithHTTPClient(ts.Client()))
	if _, err := empty.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"}); err == nil {
		t.Error("expected missing-key error")
	}
}

func TestAnthropic_NoTextBlock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "thinking", "thinking": "hmm"}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider: model.ProviderAnthropic,
		Model:    "m",
		BaseURL:  ts.URL,
		APIKey:   "k",
	}, model.WithHTTPClient(ts.Client()))

	if _, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"}); err == nil {
		t.Error("expected error when the response carries no text block")
	}
}
