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

func newOpenAIClient(t *testing.T, cfg model.Config, ts *httptest.Server) *model.Client {
	t.Helper()
	return model.NewClient(cfg, model.WithHTTPClient(ts.Client()))
}

func TestOpenAI_Generate(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "workspace ready"}},
			},
			"usage": map[string]int{
				"prompt_tokens":     11,
				"completion_tokens": 22,
				"total_tokens":      33,
			},
		})
	}))
	defer ts.Close()

	client := newOpenAIClient(t, model.Config{
		Provider: model.ProviderOpenAI,
		Model:    "deepseek-chat",
		BaseURL:  ts.URL + "/v1",
		APIKey:   "test-key",
		Parameters: map[string]any{
			"maxOutputTokens": float64(8000),
			"stopSequences":   []any{"END"},
			"customOption":    true,
		},
	}, ts)

	resp, err := client.Generate(context.Background(), &model.GenerateRequest{
		Prompt:            "Set up this workspace",
		SystemInstruction: "Be terse.",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "workspace ready" {
		t.Errorf("unexpected content %q", resp.Content)
	}
	if resp.Usage.TotalTokens != 33 {
		t.Errorf("unexpected usage %+v", resp.Usage)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("unexpected path %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("unexpected authorization %q", gotAuth)
	}
	if gotBody["model"] != "deepseek-chat" {
		t.Errorf("unexpected model %v", gotBody["model"])
	}
	if gotBody["max_tokens"] != float64(8000) {
		t.Errorf("expected maxOutputTokens translated to max_tokens, got %v", gotBody)
	}
	if _, has := gotBody["maxOutputTokens"]; has {
		t.Errorf("expected maxOutputTokens to be translated away, got %v", gotBody)
	}
	if gotBody["stop"] == nil || gotBody["stopSequences"] != nil {
		t.Errorf("expected stopSequences translated to stop, got %v", gotBody)
	}
	if gotBody["customOption"] != true {
		t.Errorf("expected unknown keys to pass through, got %v", gotBody)
	}
	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("expected system + user messages, got %v", gotBody["messages"])
	}
	if first, _ := messages[0].(map[string]any); first["role"] != "system" || first["content"] != "Be terse." {
		t.Errorf("unexpected first message %v", messages[0])
	}
}

func TestOpenAI_TypedErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "bad key"}}`))
	}))
	defer ts.Close()

	client := newOpenAIClient(t, model.Config{
		Provider: model.ProviderOpenAI,
		Model:    "deepseek-chat",
		BaseURL:  ts.URL + "/v1",
		APIKey:   "wrong-key",
	}, ts)

	_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
	var perr *model.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *ProviderError, got %v", err)
	}
	if perr.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %+v", perr)
	}
	if !strings.Contains(perr.Body, "bad key") {
		t.Errorf("expected provider body carried, got %+v", perr)
	}
}

func TestOpenAI_MissingAPIKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("expected no HTTP call without a key")
	}))
	defer ts.Close()

	client := newOpenAIClient(t, model.Config{
		Provider: model.ProviderOpenAI,
		Model:    "deepseek-chat",
		BaseURL:  ts.URL + "/v1",
	}, ts)

	_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
	var perr *model.ProviderError
	if !errors.As(err, &perr) || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("expected missing-key *ProviderError, got %v", err)
	}
}

func TestOpenAI_TransportFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close() // nothing listens anymore

	client := model.NewClient(model.Config{
		Provider: model.ProviderOpenAI,
		Model:    "deepseek-chat",
		BaseURL:  url + "/v1",
		APIKey:   "k",
	})
	_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
	var perr *model.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *ProviderError for transport failure, got %v", err)
	}
	if perr.Unwrap() == nil {
		t.Errorf("expected a wrapped cause, got %+v", perr)
	}
}

func TestOpenAI_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer ts.Close()

	client := newOpenAIClient(t, model.Config{
		Provider: model.ProviderOpenAI,
		Model:    "m",
		BaseURL:  ts.URL + "/v1",
		APIKey:   "k",
	}, ts)

	_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
	var perr *model.ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *ProviderError for malformed response, got %v", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices": []}`))
	}))
	defer empty.Close()
	client = newOpenAIClient(t, model.Config{
		Provider: model.ProviderOpenAI,
		Model:    "m",
		BaseURL:  empty.URL + "/v1",
		APIKey:   "k",
	}, empty)
	if _, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"}); err == nil {
		t.Error("expected error for empty choices")
	}
}
