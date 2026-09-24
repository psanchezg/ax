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
	// HarnessDeepSeekHarness runs DeepSeek Harness (`dsh`) one-shot headless.
	HarnessDeepSeekHarness = "deepseek-harness"
)

// Harness prepares a workspace and configures the agent that runs in it. The
// program that actually answers the goal is the task command: a harness may
// declare a default one ([HarnessCommand]).
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
	return []string{HarnessAntigravity, HarnessDeepSeekHarness}
}

// ResolveHarness maps a harness kind to its implementation. The empty kind is
// the default (antigravity). Unknown kinds return *UnsupportedHarnessError so
// the caller can fail the workspace instead of running nothing.
func ResolveHarness(kind string) (Harness, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", HarnessAntigravity:
		return antigravityHarness{}, nil
	case HarnessDeepSeekHarness:
		return deepseekHarness{}, nil
	default:
		return nil, &UnsupportedHarnessError{Kind: kind}
	}
}

// TaskHarness returns the harness declared by the first bound workspace that
// declares one, or nil when no workspace selects a harness.
func TaskHarness(workspaces []*v1alpha1.Workspace) *v1alpha1.AgentHarness {
	for _, ws := range workspaces {
		if h := ws.GetSpec().GetHarness(); h != nil {
			return h
		}
	}
	return nil
}

// HarnessImage returns the task-runner image override declared by the first
// bound workspace harness that sets one, or "".
func HarnessImage(workspaces []*v1alpha1.Workspace) string {
	for _, ws := range workspaces {
		if image := ws.GetSpec().GetHarness().GetImage(); image != "" {
			return image
		}
	}
	return ""
}

// HarnessCommand returns the argv that runs a harness for the goal, or nil when
// the harness declares no command of its own. It is used when the task spec
// does not set one, so a workspace can say "run this goal under DSH" while the
// task stays command-free.
//
// For deepseek-harness the default is `dsh --profile headless <prompt>`;
// harness.command replaces the arguments after `dsh`. The prompt is the goal,
// prefixed by harness.systemInstructions when set.
func HarnessCommand(kind string, spec *v1alpha1.AgentHarness, goal string) []string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case HarnessDeepSeekHarness:
		args := []string{"dsh"}
		if cmd := spec.GetCommand(); len(cmd) > 0 {
			args = append(args, cmd...)
		} else {
			args = append(args, "--profile", dshHeadlessProfile)
		}
		if prompt := HarnessPrompt(spec, goal); prompt != "" {
			args = append(args, prompt)
		}
		return args
	default:
		// Antigravity is driven by its bootstrap, not by a command; an explicit
		// command is still honored as an override.
		return spec.GetCommand()
	}
}

// HarnessPrompt combines the harness persona with the workspace goal.
func HarnessPrompt(spec *v1alpha1.AgentHarness, goal string) string {
	instructions := strings.TrimSpace(spec.GetSystemInstructions())
	goal = strings.TrimSpace(goal)
	switch {
	case instructions == "":
		return goal
	case goal == "":
		return instructions
	default:
		return instructions + "\n\n" + goal
	}
}
