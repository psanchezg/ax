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
	"reflect"
	"testing"
)

func TestTranslateParameters(t *testing.T) {
	tests := []struct {
		name  string
		table []paramMapping
		in    map[string]any
		want  map[string]any
	}{
		{
			name:  "gemini spellings translate",
			table: openAIParamTable,
			in: map[string]any{
				"maxOutputTokens": float64(8000),
				"topP":            0.9,
				"topK":            float64(40),
				"stopSequences":   []any{"END"},
				"candidateCount":  float64(2),
			},
			want: map[string]any{
				"max_tokens": float64(8000),
				"top_p":      0.9,
				"top_k":      float64(40),
				"stop":       []any{"END"},
				"n":          float64(2),
			},
		},
		{
			name:  "unknown keys pass through verbatim",
			table: openAIParamTable,
			in:    map[string]any{"reasoning_effort": "high", "seed": float64(1)},
			want:  map[string]any{"reasoning_effort": "high", "seed": float64(1)},
		},
		{
			name:  "native wire names win over aliases",
			table: openAIParamTable,
			in:    map[string]any{"max_tokens": float64(1), "maxOutputTokens": float64(2)},
			want:  map[string]any{"max_tokens": float64(1)},
		},
		{
			name:  "first alias wins deterministically",
			table: openAIParamTable,
			in:    map[string]any{"maxOutputTokens": float64(2), "maxTokens": float64(3)},
			want:  map[string]any{"max_tokens": float64(2)},
		},
		{
			name:  "identity mappings keep values",
			table: anthropicParamTable,
			in:    map[string]any{"temperature": 0.5},
			want:  map[string]any{"temperature": 0.5},
		},
		{
			name:  "anthropic stop sequences",
			table: anthropicParamTable,
			in:    map[string]any{"stopSequences": []any{"END"}},
			want:  map[string]any{"stop_sequences": []any{"END"}},
		},
		{
			name:  "system instruction is lifted out",
			table: openAIParamTable,
			in:    map[string]any{"systemInstruction": "hi", "temperature": 0.5},
			want:  map[string]any{"temperature": 0.5},
		},
		{
			name:  "nil parameters",
			table: openAIParamTable,
			in:    nil,
			want:  map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := translateParameters("test", tt.in, tt.table, nil)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("translate(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestTranslateParameters_Unsupported(t *testing.T) {
	_, err := anthropicParameters(map[string]any{"candidateCount": float64(2)})
	if err == nil {
		t.Fatal("expected candidateCount rejection for anthropic")
	}
	perr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if perr.Provider != ProviderAnthropic {
		t.Errorf("unexpected provider %q", perr.Provider)
	}
}
