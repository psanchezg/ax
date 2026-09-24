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

// ProviderOpenAI is the registry key of the OpenAI-compatible adapter. The one
// wire format covers DeepSeek, OpenRouter, Together, Groq, Fireworks, Baseten,
// and OpenAI-compatible local servers (vLLM, Ollama, LM Studio, llama.cpp).
const ProviderOpenAI = "openai"

// defaultOpenAIBaseURL is used when ModelSpec.base_url is empty.
const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// openai speaks the OpenAI-compatible chat-completions API.
type openai struct {
	cfg        Config
	httpClient *http.Client
}

func init() {
	Register(ProviderOpenAI, func(cfg Config, httpClient *http.Client) Provider {
		return &openai{cfg: cfg, httpClient: httpClient}
	})
}

// Name implements Provider.
func (o *openai) Name() string { return ProviderOpenAI }

// Generate implements Provider.
func (o *openai) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	if o.cfg.APIKey == "" {
		return nil, &ProviderError{
			Provider: ProviderOpenAI,
			Err:      errors.New("missing API key (check spec.secretKey and its Kubernetes secret)"),
		}
	}

	baseURL := o.cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"

	messages := make([]map[string]string, 0, 2)
	if req.SystemInstruction != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.SystemInstruction})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.Prompt})

	payload, err := openAIParameters(o.cfg.Parameters)
	if err != nil {
		return nil, err
	}
	payload["model"] = req.Model
	payload["messages"] = messages
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderOpenAI, Err: fmt.Errorf("marshaling request: %w", err)}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, &ProviderError{Provider: ProviderOpenAI, Err: fmt.Errorf("creating http request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{Provider: ProviderOpenAI, Err: fmt.Errorf("calling chat completions api: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &ProviderError{
			Provider:   ProviderOpenAI,
			StatusCode: resp.StatusCode,
			Body:       string(b),
		}
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, &ProviderError{Provider: ProviderOpenAI, Err: fmt.Errorf("decoding response: %w", err)}
	}
	if len(chatResp.Choices) == 0 {
		return nil, &ProviderError{Provider: ProviderOpenAI, Err: errors.New("decoding response: no choices returned")}
	}

	return &GenerateResponse{
		Model:   req.Model,
		Content: chatResp.Choices[0].Message.Content,
		Usage: UsageStats{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		},
	}, nil
}
