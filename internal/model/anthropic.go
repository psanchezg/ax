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

const (
	// ProviderAnthropic is the registry key of the Messages API adapter.
	ProviderAnthropic = "anthropic"

	// anthropicVersion pins the Messages API version header.
	anthropicVersion = "2023-06-01"

	// defaultAnthropicBaseURL is used when ModelSpec.base_url is empty.
	// A custom base URL is used verbatim (no /v1 suffix expected; the adapter
	// appends the /v1/messages path itself).
	defaultAnthropicBaseURL = "https://api.anthropic.com"

	// defaultAnthropicMaxTokens satisfies the API's required max_tokens when
	// neither parameters nor the request supply one.
	defaultAnthropicMaxTokens = 4096
)

// anthropic speaks the Anthropic Messages API.
type anthropic struct {
	cfg        Config
	httpClient *http.Client
}

func init() {
	Register(ProviderAnthropic, func(cfg Config, httpClient *http.Client) Provider {
		return &anthropic{cfg: cfg, httpClient: httpClient}
	})
}

// Name implements Provider.
func (a *anthropic) Name() string { return ProviderAnthropic }

// Generate implements Provider.
func (a *anthropic) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	if a.cfg.APIKey == "" {
		return nil, &ProviderError{
			Provider: ProviderAnthropic,
			Err:      errors.New("missing API key (check spec.secretKey and its Kubernetes secret)"),
		}
	}

	baseURL := a.cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultAnthropicBaseURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/messages"

	payload, err := anthropicParameters(a.cfg.Parameters)
	if err != nil {
		return nil, err
	}
	payload["model"] = req.Model
	payload["messages"] = []map[string]string{{"role": "user", "content": req.Prompt}}
	if req.SystemInstruction != "" {
		payload["system"] = req.SystemInstruction
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if _, ok := payload["max_tokens"]; !ok {
		payload["max_tokens"] = defaultAnthropicMaxTokens
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderAnthropic, Err: fmt.Errorf("marshaling request: %w", err)}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderAnthropic, Err: fmt.Errorf("creating http request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderAnthropic, Err: fmt.Errorf("calling messages api: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &ProviderError{
			Provider:   ProviderAnthropic,
			StatusCode: resp.StatusCode,
			Body:       string(b),
		}
	}

	var msgResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
		return nil, &ProviderError{Provider: ProviderAnthropic, Err: fmt.Errorf("decoding response: %w", err)}
	}

	var textBuilder strings.Builder
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			textBuilder.WriteString(block.Text)
			break
		}
	}
	if textBuilder.Len() == 0 {
		return nil, &ProviderError{Provider: ProviderAnthropic, Err: errors.New("decoding response: no text content block")}
	}

	return &GenerateResponse{
		Model:   req.Model,
		Content: textBuilder.String(),
		Usage: UsageStats{
			PromptTokens:     msgResp.Usage.InputTokens,
			CompletionTokens: msgResp.Usage.OutputTokens,
			TotalTokens:      msgResp.Usage.InputTokens + msgResp.Usage.OutputTokens,
		},
	}, nil
}
