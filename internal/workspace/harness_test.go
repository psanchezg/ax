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
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/ax/internal/workspace"
)

func TestResolveHarness(t *testing.T) {
	for _, kind := range []string{"", "antigravity", "Antigravity", " antigravity "} {
		h, err := workspace.ResolveHarness(kind)
		if err != nil {
			t.Fatalf("kind %q: unexpected error: %v", kind, err)
		}
		if h.Kind() != workspace.HarnessAntigravity {
			t.Errorf("kind %q resolved to %q", kind, h.Kind())
		}
	}

	// An unknown kind must fail closed and name the valid kinds.
	_, err := workspace.ResolveHarness("codex")
	var unsupported *workspace.UnsupportedHarnessError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedHarnessError, got %v", err)
	}
	if unsupported.Kind != "codex" {
		t.Errorf("expected the kind echoed, got %q", unsupported.Kind)
	}
	for _, want := range []string{"codex", "antigravity"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to mention %q, got %v", want, err)
		}
	}
}

func TestHarnessKinds(t *testing.T) {
	want := []string{"antigravity"}
	if got := workspace.HarnessKinds(); !reflect.DeepEqual(got, want) {
		t.Errorf("HarnessKinds() = %v, want %v", got, want)
	}
}
