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
	"github.com/google/ax/pkg/apis/v1alpha1"
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

	h, err := workspace.ResolveHarness("deepseek-harness")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Kind() != workspace.HarnessDeepSeekHarness {
		t.Errorf("expected deepseek-harness, got %q", h.Kind())
	}

	// An unknown kind must fail closed and name the valid kinds.
	_, err = workspace.ResolveHarness("codex")
	var unsupported *workspace.UnsupportedHarnessError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedHarnessError, got %v", err)
	}
	if unsupported.Kind != "codex" {
		t.Errorf("expected the kind echoed, got %q", unsupported.Kind)
	}
	for _, want := range []string{"codex", "antigravity", "deepseek-harness"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to mention %q, got %v", want, err)
		}
	}
}

func TestHarnessKinds(t *testing.T) {
	want := []string{"antigravity", "deepseek-harness"}
	if got := workspace.HarnessKinds(); !reflect.DeepEqual(got, want) {
		t.Errorf("HarnessKinds() = %v, want %v", got, want)
	}
}

func TestHarnessCommand(t *testing.T) {
	tests := []struct {
		name string
		kind string
		spec *v1alpha1.AgentHarness
		goal string
		want []string
	}{
		{
			name: "deepseek default profile",
			kind: workspace.HarnessDeepSeekHarness,
			spec: &v1alpha1.AgentHarness{},
			goal: "set up the workspace",
			want: []string{"dsh", "--profile", "headless", "set up the workspace"},
		},
		{
			name: "deepseek without a goal",
			kind: workspace.HarnessDeepSeekHarness,
			spec: &v1alpha1.AgentHarness{},
			want: []string{"dsh", "--profile", "headless"},
		},
		{
			name: "command override replaces the arguments after dsh",
			kind: workspace.HarnessDeepSeekHarness,
			spec: &v1alpha1.AgentHarness{Command: []string{"--max-turns", "3"}},
			goal: "do it",
			want: []string{"dsh", "--max-turns", "3", "do it"},
		},
		{
			name: "system instructions prefix the goal",
			kind: workspace.HarnessDeepSeekHarness,
			spec: &v1alpha1.AgentHarness{SystemInstructions: "Be terse."},
			goal: "do it",
			want: []string{"dsh", "--profile", "headless", "Be terse.\n\ndo it"},
		},
		{
			name: "antigravity declares no command",
			kind: "",
			spec: &v1alpha1.AgentHarness{},
			goal: "do it",
			want: nil,
		},
		{
			name: "antigravity honors an explicit command",
			kind: workspace.HarnessAntigravity,
			spec: &v1alpha1.AgentHarness{Command: []string{"my-agent", "--run"}},
			want: []string{"my-agent", "--run"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workspace.HarnessCommand(tt.kind, tt.spec, tt.goal)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("HarnessCommand() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaskHarnessAndImage(t *testing.T) {
	plain := &v1alpha1.Workspace{Metadata: &v1alpha1.ObjectMeta{Name: "plain"}, Spec: &v1alpha1.WorkspaceSpec{}}
	dsh := &v1alpha1.Workspace{
		Metadata: &v1alpha1.ObjectMeta{Name: "dsh"},
		Spec: &v1alpha1.WorkspaceSpec{
			Harness: &v1alpha1.AgentHarness{Kind: workspace.HarnessDeepSeekHarness, Image: "ghcr.io/org/ax-dsh-runner:1"},
		},
	}

	if h := workspace.TaskHarness([]*v1alpha1.Workspace{plain}); h != nil {
		t.Errorf("expected no harness, got %+v", h)
	}
	if h := workspace.TaskHarness([]*v1alpha1.Workspace{plain, dsh}); h.GetKind() != workspace.HarnessDeepSeekHarness {
		t.Errorf("expected the declared harness, got %+v", h)
	}
	if got := workspace.HarnessImage([]*v1alpha1.Workspace{plain, dsh}); got != "ghcr.io/org/ax-dsh-runner:1" {
		t.Errorf("unexpected image %q", got)
	}
	if got := workspace.HarnessImage([]*v1alpha1.Workspace{plain}); got != "" {
		t.Errorf("expected no image override, got %q", got)
	}
}

func TestHarnessPrompt(t *testing.T) {
	cases := []struct{ instructions, goal, want string }{
		{"", "goal", "goal"},
		{"inst", "", "inst"},
		{"inst", "goal", "inst\n\ngoal"},
		{"", "", ""},
	}
	for _, tc := range cases {
		spec := &v1alpha1.AgentHarness{SystemInstructions: tc.instructions}
		if got := workspace.HarnessPrompt(spec, tc.goal); got != tc.want {
			t.Errorf("HarnessPrompt(%q, %q) = %q, want %q", tc.instructions, tc.goal, got, tc.want)
		}
	}
}
