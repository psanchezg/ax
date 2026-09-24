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

package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/ax/pkg/apis/v1alpha1"
)

// Harness kinds. The zero value means the default harness, so an existing
// Workspace keeps today's behavior without a spec change.
const (
	// HarnessAntigravity is the default in-container agent.
	HarnessAntigravity = "antigravity"
)

// Harness prepares a workspace and configures the agent that runs in it.
type Harness interface {
	// Kind is the registry key of the harness.
	Kind() string
	// Setup prepares the workspace for this harness and reports whether the
	// harness's own bootstrap ran (Antigravity's goal bootstrap).
	Setup(ctx context.Context, spec *v1alpha1.AgentHarness, goal, workspacePath string) (bool, error)
}

// UnsupportedHarnessError reports a harness kind this build does not implement.
// It exists so an unknown kind fails loudly instead of silently skipping setup.
type UnsupportedHarnessError struct {
	Kind string
}

// Error implements error.
func (e *UnsupportedHarnessError) Error() string {
	return fmt.Sprintf("unsupported harness kind %q (valid kinds: %s)", e.Kind, strings.Join(HarnessKinds(), ", "))
}

// HarnessKinds returns the sorted list of implemented harness kinds.
func HarnessKinds() []string {
	return []string{HarnessAntigravity}
}

// ResolveHarness maps a harness kind to its implementation. The empty kind is
// the default (antigravity). Unknown kinds return *UnsupportedHarnessError so
// the caller can fail the workspace instead of running nothing.
func ResolveHarness(kind string) (Harness, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", HarnessAntigravity:
		return antigravityHarness{}, nil
	default:
		return nil, &UnsupportedHarnessError{Kind: kind}
	}
}
