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

package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/ax/internal/workspace"
	"github.com/google/ax/pkg/apis/v1alpha1"
	"gopkg.in/yaml.v3"
)

// TestRun_DSHHarnessKeepsStateOutOfWorkpace covers the single-isolation-layer
// invariant from the AX side: a DeepSeek Harness run writes its state on the
// durable AX volume, /workspace stays the agent's only file surface, and the
// harness deliberately switches DSH's own sandbox and approval layers off so
// Agent Substrate remains the only boundary. (That the sandbox then refuses
// writes outside /workspace is Substrate's guarantee and is verified against a
// cluster in the quickstart.)
func TestRun_DSHHarnessKeepsStateOutOfWorkpace(t *testing.T) {
	h := newHarness(t)
	root := t.TempDir()
	wsPath := filepath.Join(root, "workspace")
	sentinel := filepath.Join(root, "sentinel")
	if err := os.MkdirAll(sentinel, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(workspace.DSHHomeEnv, "")
	t.Setenv(workspace.DSHPermissionModeEnv, "workspace-write")

	modelYAML, err := yaml.Marshal(&v1alpha1.Model{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindModel,
		Metadata:   &v1alpha1.ObjectMeta{Name: "deepseek", Atespace: "default"},
		Spec: &v1alpha1.ModelSpec{
			Provider:  "openai",
			Model:     "deepseek-chat",
			BaseUrl:   "https://api.deepseek.com/v1",
			SecretKey: &v1alpha1.SecretKeyRef{Name: "deepseek-api-secret", Key: "DEEPSEEK_API_KEY"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(workspace.ModelYAMLEnv, string(modelYAML))

	h.task.Spec.Workspaces = []*v1alpha1.WorkspaceRef{{Name: "dsh", Path: wsPath}}
	h.task.Spec.Command = []string{"sh", "-c", "true"}
	h.cfg.Workspaces = []*v1alpha1.Workspace{{
		Metadata: &v1alpha1.ObjectMeta{Name: "dsh"},
		Spec: &v1alpha1.WorkspaceSpec{
			Harness: &v1alpha1.AgentHarness{Kind: workspace.HarnessDeepSeekHarness},
		},
	}}

	if exit := h.runUntilCommandExits(t); exit.Err != nil || exit.ExitCode != 0 {
		t.Fatalf("unexpected exit: %+v", exit)
	}

	settings := filepath.Join(workspace.AXDir, "dsh", "settings.yaml")
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("expected the DSH provider binding under AXDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "settings.yaml")); err == nil {
		t.Error("harness state must not land in /workspace")
	}
	if entries, err := os.ReadDir(sentinel); err != nil || len(entries) != 0 {
		t.Errorf("harness wrote outside its two allowed surfaces: %v %v", entries, err)
	}
	if got := os.Getenv(workspace.DSHPermissionModeEnv); got != "danger-full-access" {
		t.Errorf("expected Substrate to be the only isolation boundary, got %q", got)
	}
}
