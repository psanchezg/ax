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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// defaultGoogleBaseURL is the Google Generative Language API endpoint.
const defaultGoogleBaseURL = "https://generativelanguage.googleapis.com"

// google speaks the Google Generative Language API (Gemini generateContent).
// Its request shape and generationConfig pass-through are unchanged from the
// original single-provider client.
type google struct {
	cfg        Config
	httpClient *http.Client
}

func init() {
	Register(ProviderGoogle, func(cfg Config, httpClient *http.Client) Provider {
		return &google{cfg: cfg, httpClient: httpClient}
	})
}

// Name implements Provider.
func (g *google) Name() string { return ProviderGoogle }

// Generate implements Provider.
func (g *google) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	if g.cfg.APIKey == "" {
		return nil, &ProviderError{
			Provider: ProviderGoogle,
			Err:      errors.New("missing API key (check spec.secretKey and its Kubernetes secret)"),
		}
	}

	baseURL := g.cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultGoogleBaseURL
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", baseURL, req.Model, g.cfg.APIKey)

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": req.Prompt},
				},
			},
		},
	}

	if req.SystemInstruction != "" {
		payload["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{
				{"text": req.SystemInstruction},
			},
		}
	}

	// Configured parameters go through as-is; per-request values override them.
	genConfig := map[string]interface{}{}
	for k, v := range g.cfg.Parameters {
		if k != systemInstructionParam {
			genConfig[k] = v
		}
	}
	if req.Temperature > 0 {
		genConfig["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.MaxTokens
	}
	if len(genConfig) > 0 {
		payload["generationConfig"] = genConfig
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGoogle, Err: fmt.Errorf("marshaling request: %w", err)}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGoogle, Err: fmt.Errorf("creating http request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderGoogle, Err: fmt.Errorf("calling generative language api: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &ProviderError{
			Provider:   ProviderGoogle,
			StatusCode: resp.StatusCode,
			Body:       string(b),
		}
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, &ProviderError{Provider: ProviderGoogle, Err: fmt.Errorf("decoding response: %w", err)}
	}

	var textBuilder strings.Builder
	if len(geminiResp.Candidates) > 0 {
		for _, p := range geminiResp.Candidates[0].Content.Parts {
			textBuilder.WriteString(p.Text)
		}
	}

	return &GenerateResponse{
		Model:   req.Model,
		Content: textBuilder.String(),
		Usage: UsageStats{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		},
	}, nil
}
