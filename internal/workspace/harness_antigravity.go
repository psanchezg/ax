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
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/ax/pkg/apis/v1alpha1"
)

// antigravityHarness is the default agent harness: a goal-driven bootstrap that
// hands the workspace goal to the Antigravity agent before the task command
// starts. Its behavior is unchanged from the pre-harness implementation.
type antigravityHarness struct{}

// Kind implements Harness.
func (antigravityHarness) Kind() string { return HarnessAntigravity }

// Setup implements Harness. Without a goal there is nothing to bootstrap, which
// is what keeps a workspace binding without a `goal` a no-op.
func (antigravityHarness) Setup(ctx context.Context, spec *v1alpha1.AgentHarness, goal, workspacePath string) (bool, error) {
	if goal == "" {
		return false, nil
	}
	return runBootstrap(ctx, goal, workspacePath), nil
}

// runBootstrap hands the goal to the Antigravity agent so it can prepare the workspace.
// It reports whether the agent ran to completion. The agent needs the bootstrap script
// installed and an API key in the environment; when either is missing the step is
// skipped with a log line. Failures are logged and otherwise ignored so the task's own
// command still starts.
func runBootstrap(ctx context.Context, goal, targetPath string) bool {
	if _, err := os.Stat(bootstrapScriptPath); err != nil {
		slog.Info("Antigravity bootstrap script not installed; skipping", "script", bootstrapScriptPath)
		return false
	}
	if os.Getenv(bootstrapAPIKeyEnv) == "" {
		slog.Warn("workspace goal set but no API key available; skipping Antigravity bootstrap", "env", bootstrapAPIKeyEnv)
		return false
	}

	timeout := bootstrapTimeout()
	slog.Info("invoking Antigravity workspace bootstrap with goal", "goal", goal, "dir", targetPath, "timeout", timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dataDir := filepath.Join(AXDir, bootstrapDataDir)
	if err := os.MkdirAll(dataDir, dirPerm); err != nil {
		slog.Warn("creating Antigravity data dir", "dir", dataDir, "error", err)
	}

	cmd := exec.CommandContext(ctx, "python3", bootstrapScriptPath,
		"--goal", goal,
		"--workspace", targetPath,
		"--data-dir", dataDir,
	)
	cmd.Dir = targetPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			slog.Warn("Antigravity bootstrap timed out (continuing)", "timeout", timeout)
		} else {
			slog.Warn("Antigravity bootstrap failed (continuing)", "error", err)
		}
		return false
	}
	slog.Info("Antigravity bootstrap completed successfully")
	return true
}

// bootstrapTimeout returns the configured bootstrap timeout, falling back to the default
// when the override is unset or unparsable.
func bootstrapTimeout() time.Duration {
	raw := os.Getenv(bootstrapTimeoutEnv)
	if raw == "" {
		return defaultBootstrapTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("invalid bootstrap timeout; using default", "env", bootstrapTimeoutEnv, "value", raw, "default", defaultBootstrapTimeout)
		return defaultBootstrapTimeout
	}
	return d
}
