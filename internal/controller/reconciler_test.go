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

package controller_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/ax/internal/controller"
	"github.com/google/ax/internal/substrate"
	"github.com/google/ax/pkg/apis/v1alpha1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type mockControlServer struct {
	ateapipb.UnimplementedControlServer
	workerIP         string
	createdAtespaces []string
	createdActors    []string
	resumedActors    []string
	suspendedActors  []string
	deletedActors    []string
	actorTemplates   map[string]bool
	deletedTemplates []string
	// templateEnvs records the container environment of every created
	// ActorTemplate, which is how the task container contract is asserted.
	templateEnvs []map[string]string
}

// latestTemplateEnv returns the environment of the most recently created
// ActorTemplate, or nil when none was created.
func (m *mockControlServer) latestTemplateEnv() map[string]string {
	if len(m.templateEnvs) == 0 {
		return nil
	}
	return m.templateEnvs[len(m.templateEnvs)-1]
}

// noSecrets is a SecretResolver for tests: it never finds a key and never touches a cluster.
func noSecrets(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (m *mockControlServer) GetActorTemplate(_ context.Context, req *ateapipb.GetActorTemplateRequest) (*ateapipb.ActorTemplate, error) {
	ref := req.GetActorTemplate()
	if !m.actorTemplates[ref.GetName()] {
		return nil, status.Error(codes.NotFound, "template not found")
	}
	return &ateapipb.ActorTemplate{Metadata: &ateapipb.ResourceMetadata{Name: ref.GetName(), Atespace: ref.GetAtespace()}}, nil
}

func (m *mockControlServer) CreateAtespace(ctx context.Context, req *ateapipb.CreateAtespaceRequest) (*ateapipb.Atespace, error) {
	name := ""
	if req.Atespace != nil && req.Atespace.Metadata != nil {
		name = req.Atespace.Metadata.Name
	}
	m.createdAtespaces = append(m.createdAtespaces, name)
	return &ateapipb.Atespace{Metadata: &ateapipb.ResourceMetadata{Name: name}}, nil
}

func (m *mockControlServer) CreateActor(ctx context.Context, req *ateapipb.CreateActorRequest) (*ateapipb.Actor, error) {
	name := ""
	if req.Actor != nil && req.Actor.Metadata != nil {
		name = req.Actor.Metadata.Name
	}
	m.createdActors = append(m.createdActors, name)
	return &ateapipb.Actor{
		Metadata: &ateapipb.ResourceMetadata{Name: name},
		Status: &ateapipb.ActorStatus{
			State: ateapipb.ActorState_ACTOR_STATE_SUSPENDED,
		},
	}, nil
}

func (m *mockControlServer) ResumeActor(ctx context.Context, req *ateapipb.ResumeActorRequest) (*ateapipb.ResumeActorResponse, error) {
	name := ""
	if req.Actor != nil {
		name = req.Actor.Name
	}
	m.resumedActors = append(m.resumedActors, name)
	wIP := "10.244.1.42"
	if m.workerIP != "" {
		wIP = m.workerIP
	}
	return &ateapipb.ResumeActorResponse{
		Actor: &ateapipb.Actor{
			Metadata: &ateapipb.ResourceMetadata{Name: name},
			Status: &ateapipb.ActorStatus{
				State: ateapipb.ActorState_ACTOR_STATE_RUNNING,
				WorkerAssignment: &ateapipb.WorkerAssignment{
					WorkerPod:   "worker-pod-1",
					WorkerPodIp: wIP,
				},
			},
		},
		Resumed: true,
	}, nil
}

func (m *mockControlServer) SuspendActor(ctx context.Context, req *ateapipb.SuspendActorRequest) (*ateapipb.SuspendActorResponse, error) {
	name := ""
	if req.Actor != nil {
		name = req.Actor.Name
	}
	m.suspendedActors = append(m.suspendedActors, name)
	return &ateapipb.SuspendActorResponse{}, nil
}


func (m *mockControlServer) DeleteActor(ctx context.Context, req *ateapipb.DeleteActorRequest) (*ateapipb.Actor, error) {
	name := req.GetActor().GetName()
	m.deletedActors = append(m.deletedActors, name)
	return &ateapipb.Actor{Metadata: &ateapipb.ResourceMetadata{Name: name}}, nil
}

func (m *mockControlServer) ListActorTemplates(ctx context.Context, req *ateapipb.ListActorTemplatesRequest) (*ateapipb.ListActorTemplatesResponse, error) {
	resp := &ateapipb.ListActorTemplatesResponse{}
	for name := range m.actorTemplates {
		resp.ActorTemplates = append(resp.ActorTemplates, &ateapipb.ActorTemplate{
			Metadata: &ateapipb.ResourceMetadata{Name: name, Atespace: req.GetAtespace()},
		})
	}
	return resp, nil
}

func (m *mockControlServer) DeleteActorTemplate(ctx context.Context, req *ateapipb.DeleteActorTemplateRequest) (*ateapipb.ActorTemplate, error) {
	name := req.GetActorTemplate().GetName()
	delete(m.actorTemplates, name)
	m.deletedTemplates = append(m.deletedTemplates, name)
	return &ateapipb.ActorTemplate{Metadata: &ateapipb.ResourceMetadata{Name: name}}, nil
}

func (m *mockControlServer) CreateActorTemplate(ctx context.Context, req *ateapipb.CreateActorTemplateRequest) (*ateapipb.ActorTemplate, error) {
	tmpl := req.GetActorTemplate()
	if name := tmpl.GetMetadata().GetName(); name != "" {
		if m.actorTemplates == nil {
			m.actorTemplates = map[string]bool{}
		}
		m.actorTemplates[name] = true
	}
	env := map[string]string{}
	for _, container := range tmpl.GetContainers() {
		for _, e := range container.GetEnv() {
			env[e.GetName()] = e.GetValue()
		}
	}
	m.templateEnvs = append(m.templateEnvs, env)
	return tmpl, nil
}

func TestTaskReconciler(t *testing.T) {
	ctx := context.Background()

	// 1. Start in-process mock gRPC Substrate server
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	mockSrv := &mockControlServer{}
	grpcServer := grpc.NewServer()
	ateapipb.RegisterControlServer(grpcServer, mockSrv)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	// 2. Initialize Substrate client
	client, err := substrate.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create substrate client: %v", err)
	}
	defer client.Close()

	// 3. Reconcile Task
	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.SecretResolver = noSecrets
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond

	task := &v1alpha1.Task{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindTask,
		Metadata: &v1alpha1.ObjectMeta{
			Name:     "test-task",
			Atespace: "default",
		},
		Spec: &v1alpha1.TaskSpec{
			Image:   "ghrc.io/my-org/my-image",
			Command: []string{"/bin/task-runner"},
		},
		// A client-supplied actor name must not survive: the actor is always
		// named after the task.
		Status: &v1alpha1.TaskStatus{Actor: "not-the-task"},
	}

	reconciled, err := reconciler.Reconcile(ctx, task)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// 4. Validate reconciliation results
	if reconciled.Status.Phase != "Running" {
		t.Errorf("expected phase 'Running', got %q", reconciled.Status.Phase)
	}
	if reconciled.Status.Actor != "test-task" {
		t.Errorf("expected actor 'test-task', got %q", reconciled.Status.Actor)
	}
	if reconciled.Status.WorkerIp != "10.244.1.42" {
		t.Errorf("expected worker IP '10.244.1.42', got %q", reconciled.Status.WorkerIp)
	}

	// Verify mock was called
	if len(mockSrv.createdAtespaces) != 1 || mockSrv.createdAtespaces[0] != "default" {
		t.Errorf("expected atespace 'default' created, got %v", mockSrv.createdAtespaces)
	}
	if len(mockSrv.createdActors) != 1 || mockSrv.createdActors[0] != "test-task" {
		t.Errorf("expected actor 'test-task' created, got %v", mockSrv.createdActors)
	}
	if len(mockSrv.resumedActors) != 1 || mockSrv.resumedActors[0] != "test-task" {
		t.Errorf("expected actor 'test-task' resumed, got %v", mockSrv.resumedActors)
	}
}

func TestTaskReconciler_Suspend(t *testing.T) {
	ctx := context.Background()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	mockSrv := &mockControlServer{}
	grpcServer := grpc.NewServer()
	ateapipb.RegisterControlServer(grpcServer, mockSrv)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	client, err := substrate.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create substrate client: %v", err)
	}
	defer client.Close()

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.SecretResolver = noSecrets
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond

	task := &v1alpha1.Task{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindTask,
		Metadata: &v1alpha1.ObjectMeta{
			Name:     "suspend-task",
			Atespace: "default",
		},
		Spec: &v1alpha1.TaskSpec{
			Suspend: true,
			Image:   "ghrc.io/my-org/my-image",
		},
	}

	reconciled, err := reconciler.Reconcile(ctx, task, nil)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if reconciled.Status.Phase != "Suspended" {
		t.Errorf("expected phase 'Suspended', got %q", reconciled.Status.Phase)
	}
	if reconciled.Status.WorkerIp != "" {
		t.Errorf("expected empty worker IP, got %q", reconciled.Status.WorkerIp)
	}
	if len(mockSrv.suspendedActors) != 1 || mockSrv.suspendedActors[0] != "suspend-task" {
		t.Errorf("expected actor 'suspend-task' suspended, got %v", mockSrv.suspendedActors)
	}
}

func TestTaskReconciler_WorkspaceReady(t *testing.T) {
	ctx := context.Background()

	// 1. Mock Substrate Control Server
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	// 2. Mock Worker readyz HTTP server
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen http: %v", err)
	}
	defer httpLis.Close()

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	httpServer := &http.Server{Handler: httpMux}
	go httpServer.Serve(httpLis)
	defer httpServer.Close()

	workerHost, workerPortStr, _ := net.SplitHostPort(httpLis.Addr().String())
	// In our mock, the worker IP returned by ResumeActor will have our mock ready server listening.
	// But our reconciler connects to port 9999 by default: fmt.Sprintf("http://%s:9999/readyz", workerIP).
	// If workerIP includes a port or is a host, let's verify how it handles it.
	_ = workerHost
	_ = workerPortStr

	mockSrv := &mockControlServer{}
	grpcServer := grpc.NewServer()
	ateapipb.RegisterControlServer(grpcServer, mockSrv)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	client, err := substrate.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create substrate client: %v", err)
	}
	defer client.Close()

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.SecretResolver = noSecrets
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond

	task := &v1alpha1.Task{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindTask,
		Metadata: &v1alpha1.ObjectMeta{
			Name:     "ready-task",
			Atespace: "default",
		},
		Spec: &v1alpha1.TaskSpec{},
	}

	// Case 1: Worker not responding on readyz -> WorkspaceReady=False and Ready=False.
	reconciled, err := reconciler.Reconcile(ctx, task, nil)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	assertCondition(t, reconciled, "WorkspaceReady", "False", "Initializing")
	assertCondition(t, reconciled, "Ready", "False", "WorkspaceInitializing")

	// Case 2: Worker readyz endpoint succeeds -> WorkspaceReady=True and Ready=True.
	mockSrv.workerIP = httpLis.Addr().String()
	reconciledReady, err := reconciler.Reconcile(ctx, task, nil)
	if err != nil {
		t.Fatalf("Reconcile with ready worker failed: %v", err)
	}
	assertCondition(t, reconciledReady, "WorkspaceReady", "True", "SetupComplete")
	assertCondition(t, reconciledReady, "Ready", "True", "TaskRunning")

	// Case 3: Suspending the task -> Ready=False (TaskSuspended), but the workspace was
	// already initialized so WorkspaceReady stays True.
	task = reconciledReady
	task.Spec.Suspend = true
	reconciledSuspended, err := reconciler.Reconcile(ctx, task, nil)
	if err != nil {
		t.Fatalf("Reconcile with suspend failed: %v", err)
	}
	if reconciledSuspended.Status.Phase != "Suspended" {
		t.Errorf("expected phase Suspended, got %s", reconciledSuspended.Status.Phase)
	}
	assertCondition(t, reconciledSuspended, "Ready", "False", "TaskSuspended")
	assertCondition(t, reconciledSuspended, "WorkspaceReady", "True", "SetupComplete")

	// Case 4: Resuming with the worker unreachable -> the reconciler trusts the recorded
	// WorkspaceReady instead of re-polling, so the task is Ready again immediately.
	mockSrv.workerIP = "127.0.0.1:1"
	task = reconciledSuspended
	task.Spec.Suspend = false
	reconciledResumed, err := reconciler.Reconcile(ctx, task, nil)
	if err != nil {
		t.Fatalf("Reconcile with resume failed: %v", err)
	}
	assertCondition(t, reconciledResumed, "WorkspaceReady", "True", "SetupComplete")
	assertCondition(t, reconciledResumed, "Ready", "True", "TaskRunning")
	if got := len(mockSrv.actorTemplates); got != 1 {
		t.Fatalf("readiness updates and suspend/resume created %d templates, want 1", got)
	}

	// A launch configuration change must still get a distinct template.
	task.Spec.Command = []string{"python3", "agent.py"}
	if _, err := reconciler.Reconcile(ctx, task, nil); err != nil {
		t.Fatalf("Reconcile with changed command failed: %v", err)
	}
	if got := len(mockSrv.actorTemplates); got != 2 {
		t.Errorf("command change left %d templates, want 2", got)
	}
}

// assertCondition fails the test unless the task has a condition of the given type with
// the expected status and reason.
func assertCondition(t *testing.T, task *v1alpha1.Task, condType, wantStatus, wantReason string) {
	t.Helper()
	for _, c := range task.Status.Conditions {
		if c.Type != condType {
			continue
		}
		if c.Status != wantStatus || c.Reason != wantReason {
			t.Errorf("expected %s=%s (%s), got Status=%s Reason=%s", condType, wantStatus, wantReason, c.Status, c.Reason)
		}
		return
	}
	t.Errorf("expected %s condition to be set", condType)
}

func TestReconcileDelete_RemovesActorAndTemplates(t *testing.T) {
	ctx := context.Background()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	mockSrv := &mockControlServer{actorTemplates: map[string]bool{
		"job-tmpl-0a1b2c3d":               true, // current revision of task "job"
		"job-tmpl-deadbeef":               true, // stale revision of task "job"
		"job-tmpl-deadbeef-tmpl-01234567": true, // belongs to a task literally named "job-tmpl-deadbeef"
		"jobs-tmpl-0a1b2c3d":              true, // belongs to task "jobs"
		"default-template":                true,
	}}
	grpcServer := grpc.NewServer()
	ateapipb.RegisterControlServer(grpcServer, mockSrv)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	client, err := substrate.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create substrate client: %v", err)
	}
	defer client.Close()

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.SecretResolver = noSecrets
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond

	if err := reconciler.ReconcileDelete(ctx, "default", "job"); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	if len(mockSrv.deletedActors) != 1 || mockSrv.deletedActors[0] != "job" {
		t.Errorf("expected actor 'job' to be deleted, got %v", mockSrv.deletedActors)
	}

	wantDeleted := map[string]bool{"job-tmpl-0a1b2c3d": true, "job-tmpl-deadbeef": true}
	if len(mockSrv.deletedTemplates) != len(wantDeleted) {
		t.Errorf("expected %d templates deleted, got %v", len(wantDeleted), mockSrv.deletedTemplates)
	}
	for _, name := range mockSrv.deletedTemplates {
		if !wantDeleted[name] {
			t.Errorf("unexpected template deleted: %s", name)
		}
	}
	for _, keep := range []string{"job-tmpl-deadbeef-tmpl-01234567", "jobs-tmpl-0a1b2c3d", "default-template"} {
		if !mockSrv.actorTemplates[keep] {
			t.Errorf("template %s should not have been deleted", keep)
		}
	}
}

// startMockSubstrate starts an in-process Substrate mock and returns it with a client.
func startMockSubstrate(t *testing.T) (*mockControlServer, *substrate.Client) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	mockSrv := &mockControlServer{}
	grpcServer := grpc.NewServer()
	ateapipb.RegisterControlServer(grpcServer, mockSrv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		lis.Close()
	})

	client, err := substrate.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create substrate client: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return mockSrv, client
}

// modelBoundTask returns a task whose single workspace references modelRef.
func modelBoundTask(modelRef string) (*v1alpha1.Task, *v1alpha1.Workspace) {
	task := &v1alpha1.Task{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindTask,
		Metadata:   &v1alpha1.ObjectMeta{Name: "dsh-task", Atespace: "default"},
		Spec: &v1alpha1.TaskSpec{
			Image:   "ghcr.io/org/ax-dsh-runner:1",
			Command: []string{"dsh", "--profile", "headless", "do the thing"},
		},
	}
	ws := &v1alpha1.Workspace{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindWorkspace,
		Metadata:   &v1alpha1.ObjectMeta{Name: "dsh-ws", Atespace: "default"},
		Spec:       &v1alpha1.WorkspaceSpec{},
	}
	if modelRef != "" {
		ws.Spec.Harness = &v1alpha1.AgentHarness{Kind: "deepseek-harness", ModelRef: modelRef}
	}
	return task, ws
}

func testModel() *v1alpha1.Model {
	return &v1alpha1.Model{
		ApiVersion: v1alpha1.APIVersion,
		Kind:       v1alpha1.KindModel,
		Metadata:   &v1alpha1.ObjectMeta{Name: "deepseek", Atespace: "default"},
		Spec: &v1alpha1.ModelSpec{
			Provider:  "openai",
			Model:     "deepseek-chat",
			BaseUrl:   "https://api.deepseek.com/v1",
			SecretKey: &v1alpha1.SecretKeyRef{Name: "deepseek-api-secret", Key: "DEEPSEEK_API_KEY"},
		},
	}
}

// TestReconcile_ModelDerivedCredential covers the Model-derived container
// contract: the credential is injected under the model-declared name, plus the
// endpoint and the Model itself, and the legacy Gemini literal is left out.
func TestReconcile_ModelDerivedCredential(t *testing.T) {
	ctx := context.Background()
	mockSrv, client := startMockSubstrate(t)

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond
	reconciler.ModelResolver = func(ctx context.Context, atespace, name string) (*v1alpha1.Model, error) {
		if atespace != "default" || name != "deepseek" {
			t.Errorf("unexpected model lookup %s/%s", atespace, name)
		}
		return testModel(), nil
	}
	var secretNames []string
	reconciler.SecretResolver = func(ctx context.Context, namespace, secretName, key string) (string, error) {
		secretNames = append(secretNames, namespace+"/"+secretName+"/"+key)
		if secretName != "deepseek-api-secret" || key != "DEEPSEEK_API_KEY" {
			t.Errorf("unexpected secret lookup %s/%s/%s", namespace, secretName, key)
		}
		return "sk-test-value", nil
	}

	task, ws := modelBoundTask("deepseek")
	if _, err := reconciler.Reconcile(ctx, task, ws); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	env := mockSrv.latestTemplateEnv()
	if env == nil {
		t.Fatal("expected an ActorTemplate to be created")
	}
	if env["DEEPSEEK_API_KEY"] != "sk-test-value" {
		t.Errorf("expected the credential under the model-declared name, got %q", env["DEEPSEEK_API_KEY"])
	}
	if env["AX_MODEL_BASE_URL"] != "https://api.deepseek.com/v1" {
		t.Errorf("expected AX_MODEL_BASE_URL, got %q", env["AX_MODEL_BASE_URL"])
	}
	if !strings.Contains(env["AX_MODEL_YAML"], "name: deepseek") {
		t.Errorf("expected AX_MODEL_YAML to carry the Model, got %q", env["AX_MODEL_YAML"])
	}
	if strings.Contains(env["AX_MODEL_YAML"], "sk-test-value") {
		t.Errorf("AX_MODEL_YAML must carry the secret reference, never the value: %q", env["AX_MODEL_YAML"])
	}
	if _, leaked := env["GEMINI_API_KEY"]; leaked {
		t.Error("expected no GEMINI_API_KEY when a Model is bound")
	}
	if len(secretNames) != 1 {
		t.Errorf("expected exactly one secret lookup, got %v", secretNames)
	}
}

// TestReconcile_LegacyCredentialPath keeps the pre-Model behavior byte-for-byte:
// with no model reference, the Gemini secret is injected as before.
func TestReconcile_LegacyCredentialPath(t *testing.T) {
	ctx := context.Background()
	mockSrv, client := startMockSubstrate(t)

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond
	modelLookups := 0
	reconciler.ModelResolver = func(context.Context, string, string) (*v1alpha1.Model, error) {
		modelLookups++
		return testModel(), nil
	}
	reconciler.SecretResolver = func(ctx context.Context, namespace, secretName, key string) (string, error) {
		if secretName != "gemini-api-secret" || key != "GEMINI_API_KEY" {
			t.Errorf("unexpected secret lookup %s/%s", secretName, key)
		}
		return "legacy-key", nil
	}

	task, ws := modelBoundTask("")
	if _, err := reconciler.Reconcile(ctx, task, ws); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	env := mockSrv.latestTemplateEnv()
	if env == nil {
		t.Fatal("expected an ActorTemplate to be created")
	}
	if env["GEMINI_API_KEY"] != "legacy-key" {
		t.Errorf("expected the legacy Gemini credential, got %q", env["GEMINI_API_KEY"])
	}
	for _, key := range []string{"AX_MODEL_YAML", "AX_MODEL_BASE_URL"} {
		if _, present := env[key]; present {
			t.Errorf("expected no %s without a model reference", key)
		}
	}
	if modelLookups != 0 {
		t.Errorf("expected no model lookup without a reference, got %d", modelLookups)
	}
}

// TestReconcile_UnresolvedModelRef fails closed: a reference that cannot be
// resolved must not silently fall back to the legacy credential.
func TestReconcile_UnresolvedModelRef(t *testing.T) {
	ctx := context.Background()
	mockSrv, client := startMockSubstrate(t)

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond
	reconciler.ModelResolver = func(context.Context, string, string) (*v1alpha1.Model, error) {
		return nil, errors.New("model not found")
	}
	reconciler.SecretResolver = func(context.Context, string, string, string) (string, error) {
		return "legacy-key", nil
	}

	task, ws := modelBoundTask("missing")
	reconciled, err := reconciler.Reconcile(ctx, task, ws)
	if err == nil {
		t.Fatal("expected an error for an unresolvable model reference")
	}
	if reconciled.Status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", reconciled.Status.Phase)
	}
	if reason := conditionReason(reconciled, "Ready"); reason != "UnresolvedModelRef" {
		t.Errorf("expected condition reason UnresolvedModelRef, got %q", reason)
	}
	if len(mockSrv.templateEnvs) != 0 {
		t.Errorf("expected no container environment to be provisioned, got %v", mockSrv.templateEnvs)
	}
}

// TestReconcile_UnresolvedModelSecret fails closed with no partial injection.
func TestReconcile_UnresolvedModelSecret(t *testing.T) {
	ctx := context.Background()
	mockSrv, client := startMockSubstrate(t)

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond
	reconciler.ModelResolver = func(context.Context, string, string) (*v1alpha1.Model, error) {
		return testModel(), nil
	}
	reconciler.SecretResolver = func(context.Context, string, string, string) (string, error) {
		return "", errors.New("secret not found")
	}

	task, ws := modelBoundTask("deepseek")
	reconciled, err := reconciler.Reconcile(ctx, task, ws)
	if err == nil {
		t.Fatal("expected an error for an unresolvable secret")
	}
	if reason := conditionReason(reconciled, "Ready"); reason != "UnresolvedModelCredential" {
		t.Errorf("expected condition reason UnresolvedModelCredential, got %q", reason)
	}
	if len(mockSrv.templateEnvs) != 0 {
		t.Errorf("expected no partial injection, got %v", mockSrv.templateEnvs)
	}
}

// conditionReason returns the reason of the named condition, or "".
func conditionReason(task *v1alpha1.Task, condType string) string {
	for _, c := range task.GetStatus().GetConditions() {
		if c.GetType() == condType {
			return c.GetReason()
		}
	}
	return ""
}

// TestReconcile_HarnessImageOverride covers the per-harness runtime: a harness
// can name the image its tasks need, and an explicit task image still wins.
func TestReconcile_HarnessImageOverride(t *testing.T) {
	ctx := context.Background()
	_, client := startMockSubstrate(t)

	reconciler := controller.NewTaskReconciler(client, "test-template", "ax-system")
	reconciler.WorkspaceReadyTimeout = 200 * time.Millisecond
	reconciler.SecretResolver = noSecrets

	task, ws := modelBoundTask("")
	ws.Spec.Harness = &v1alpha1.AgentHarness{Image: "ghcr.io/org/ax-dsh-runner:1"}
	if _, err := reconciler.Reconcile(ctx, task, ws); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if task.Spec.Image != "ghcr.io/org/ax-dsh-runner:1" {
		t.Errorf("expected the harness image, got %q", task.Spec.Image)
	}

	// An explicit task image is not overridden.
	task, ws = modelBoundTask("")
	ws.Spec.Harness = &v1alpha1.AgentHarness{Image: "ghcr.io/org/ax-dsh-runner:1"}
	task.Spec.Image = "ghcr.io/org/custom:9"
	if _, err := reconciler.Reconcile(ctx, task, ws); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if task.Spec.Image != "ghcr.io/org/custom:9" {
		t.Errorf("expected the task image to win, got %q", task.Spec.Image)
	}
}
