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

// TestGoogle_RequestShape pins the Gemini wire format byte-for-byte: same
// endpoint shape, same contents payload, and verbatim generationConfig
// pass-through with per-request overrides.
func TestGoogle_RequestShape(t *testing.T) {
	var gotPath, gotQuery string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]string{{"text": "ok"}}}},
			},
			"usageMetadata": map[string]int{
				"promptTokenCount":     1,
				"candidatesTokenCount": 2,
				"totalTokenCount":      3,
			},
		})
	}))
	defer ts.Close()

	client := model.NewClient(model.Config{
		Provider: "google",
		Model:    "gemini-3.8-flash",
		BaseURL:  ts.URL,
		APIKey:   "test-api-key",
		Parameters: map[string]any{
			"maxOutputTokens":   float64(4096),
			"stopSequences":     []any{"END"},
			"systemInstruction": "be brief",
		},
	}, model.WithHTTPClient(ts.Client()))

	resp, err := client.Generate(context.Background(), &model.GenerateRequest{
		Prompt:      "Plan Go setup",
		MaxTokens:   512,
		Temperature: 0.4,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "ok" || resp.Usage.TotalTokens != 3 {
		t.Errorf("unexpected response %+v", resp)
	}
	if gotPath != "/v1beta/models/gemini-3.8-flash:generateContent" {
		t.Errorf("unexpected path %q", gotPath)
	}
	if gotQuery != "key=test-api-key" {
		t.Errorf("unexpected query %q", gotQuery)
	}
	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("unexpected contents %v", gotBody["contents"])
	}
	genConfig, _ := gotBody["generationConfig"].(map[string]any)
	// Gemini spellings pass through verbatim; per-request values override.
	if genConfig["maxOutputTokens"] != float64(512) {
		t.Errorf("expected per-request maxOutputTokens override, got %v", genConfig)
	}
	if genConfig["temperature"] != 0.4 {
		t.Errorf("expected per-request temperature override, got %v", genConfig)
	}
	if gotConfig, ok := genConfig["stopSequences"].([]any); !ok || len(gotConfig) != 1 {
		t.Errorf("expected verbatim stopSequences pass-through, got %v", genConfig)
	}
	if _, leaked := genConfig["systemInstruction"]; leaked {
		t.Errorf("expected systemInstruction lifted out of generationConfig, got %v", genConfig)
	}
	if sysInst, _ := gotBody["systemInstruction"].(map[string]any); sysInst == nil {
		t.Errorf("expected top-level systemInstruction, got %v", gotBody)
	}
}

// TestGoogle_ErrorsAreTyped is the FR-006 regression: statuses that used to
// fabricate a fallback completion now surface as *ProviderError with the
// provider's status and body.
func TestGoogle_ErrorsAreTyped(t *testing.T) {
	for _, code := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusServiceUnavailable,
	} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`boom`))
		}))
		client := model.NewClient(model.Config{
			Provider: "google",
			Model:    "m",
			BaseURL:  ts.URL,
			APIKey:   "k",
		}, model.WithHTTPClient(ts.Client()))

		_, err := client.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
		ts.Close()
		var perr *model.ProviderError
		if !errors.As(err, &perr) {
			t.Fatalf("status %d: expected *ProviderError, got %v", code, err)
		}
		if perr.StatusCode != code || !strings.Contains(perr.Body, "boom") {
			t.Errorf("status %d: expected status+body carried, got %+v", code, perr)
		}
	}

	// Empty key: typed error, no call, no fabrication.
	empty := model.NewClient(model.Config{Provider: "google", Model: "m"})
	_, err := empty.Generate(context.Background(), &model.GenerateRequest{Prompt: "x"})
	if !errors.As(err, new(*model.ProviderError)) || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("expected missing-key *ProviderError, got %v", err)
	}
}
