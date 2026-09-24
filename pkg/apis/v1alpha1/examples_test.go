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

package v1alpha1_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/ax/pkg/apis/v1alpha1"
	"gopkg.in/yaml.v3"
)

// TestExamples_ParseStrictly keeps every shipped manifest honest: each document
// must decode through the same strict path `ax apply` uses (unknown or
// misspelled fields are errors) and pass the same validation the API server
// applies before saving.
func TestExamples_ParseStrictly(t *testing.T) {
	examples, err := filepath.Glob(filepath.Join("..", "..", "..", "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 {
		t.Fatal("no example manifests found")
	}

	for _, path := range examples {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(data))
			for docIndex := 1; ; docIndex++ {
				var doc yaml.Node
				if err := decoder.Decode(&doc); err != nil {
					if errors.Is(err, io.EOF) {
						return
					}
					t.Fatalf("document %d: decoding yaml: %v", docIndex, err)
				}
				if doc.Kind == 0 || (doc.Kind == yaml.DocumentNode && len(doc.Content) == 0) {
					continue
				}
				var head struct {
					Kind string `yaml:"kind"`
				}
				if err := doc.Decode(&head); err != nil {
					t.Fatalf("document %d: reading kind: %v", docIndex, err)
				}
				switch head.Kind {
				case v1alpha1.KindTask:
					var task v1alpha1.Task
					if err := doc.Decode(&task); err != nil {
						t.Fatalf("document %d: decoding Task: %v", docIndex, err)
					}
					if err := v1alpha1.ValidateTask(&task); err != nil {
						t.Errorf("document %d: invalid Task: %v", docIndex, err)
					}
				case v1alpha1.KindWorkspace:
					var ws v1alpha1.Workspace
					if err := doc.Decode(&ws); err != nil {
						t.Fatalf("document %d: decoding Workspace: %v", docIndex, err)
					}
				case v1alpha1.KindModel:
					var m v1alpha1.Model
					if err := doc.Decode(&m); err != nil {
						t.Fatalf("document %d: decoding Model: %v", docIndex, err)
					}
					if err := v1alpha1.ValidateModel(&m); err != nil {
						t.Errorf("document %d: invalid Model: %v", docIndex, err)
					}
				default:
					t.Errorf("document %d: unknown kind %q", docIndex, head.Kind)
				}
			}
		})
	}
}
