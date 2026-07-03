package runs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/execution"
	"agent-compose/pkg/images"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sessionstore"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestRunsCoordinatorAndHelperWorkflows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)
	store := &fakeRunStore{
		project: domain.ProjectRecord{ID: "project-1", Name: "Project", CurrentRevision: 3},
		projectAgent: domain.ProjectAgentRecord{
			ProjectID: "project-1", AgentName: "worker", ManagedAgentID: "agent-1", Driver: "boxlite", Image: "agent-image:latest",
		},
		agent: ManagedAgentDefinition{
			ID: "agent-1", Enabled: true, Driver: "docker", GuestImage: "guest:latest", ManagedProjectID: "project-1", ManagedAgentName: "worker",
		},
		runs: map[string]domain.ProjectRunRecord{},
	}
	coord := NewCoordinator(store, func(projectID, agentName, source, idempotencyKey string) (string, error) {
		return strings.Join([]string{projectID, agentName, source, idempotencyKey}, ":"), nil
	})
	coord.SetNow(func() time.Time { return now })
	run, err := coord.BeginRun(ctx, StartRequest{ProjectID: "project-1", AgentName: "worker", Source: domain.ProjectRunSourceAPI, ClientRequestID: "request-1", SchedulerID: "scheduler", TriggerID: "trigger", Prompt: "do work"})
	if err != nil {
		t.Fatalf("BeginRun returned error: %v", err)
	}
	if run.Status != domain.ProjectRunStatusPending || run.Driver != "docker" || run.ImageRef != "guest:latest" {
		t.Fatalf("run = %#v", run)
	}
	if existing, err := coord.BeginRun(ctx, StartRequest{ProjectID: "project-1", AgentName: "worker", Source: domain.ProjectRunSourceAPI, ClientRequestID: "request-1"}); err != nil || existing.RunID != run.RunID {
		t.Fatalf("idempotent BeginRun existing=%#v err=%v", existing, err)
	}
	if running, err := coord.MarkRunning(ctx, run.RunID, "session-1"); err != nil || running.Status != domain.ProjectRunStatusRunning || running.SessionID != "session-1" {
		t.Fatalf("MarkRunning run=%#v err=%v", running, err)
	}
	if succeeded, err := coord.MarkSucceeded(ctx, TransitionRequest{RunID: run.RunID, ExitCode: 0, Output: "ok", ResultJSON: `{"ok":true}`, LogsPath: "/logs", ArtifactsDir: "/artifacts"}); err != nil || succeeded.Status != domain.ProjectRunStatusSucceeded || succeeded.DurationMs < 0 {
		t.Fatalf("MarkSucceeded run=%#v err=%v", succeeded, err)
	}
	if _, err := coord.MarkFailed(ctx, TransitionRequest{RunID: run.RunID, Error: "late"}); err == nil {
		t.Fatalf("expected terminal transition error")
	}
	if _, err := (*Coordinator)(nil).BeginRun(ctx, StartRequest{}); err == nil {
		t.Fatalf("expected nil coordinator error")
	}
	if _, err := NewCoordinator(store, nil).BeginRun(ctx, StartRequest{ProjectID: "project-1", AgentName: "worker"}); err == nil {
		t.Fatalf("expected missing stable id function error")
	}
	store.agent.Enabled = false
	if _, err := coord.BeginRun(ctx, StartRequest{ProjectID: "project-1", AgentName: "worker", ClientRequestID: "request-2"}); err == nil {
		t.Fatalf("expected disabled agent error")
	}

	for _, status := range []string{domain.ProjectRunStatusPending, domain.ProjectRunStatusRunning, domain.ProjectRunStatusSucceeded, domain.ProjectRunStatusFailed, domain.ProjectRunStatusCanceled, "bad"} {
		_ = NormalizeStatus(status)
		_ = StatusIsTerminal(status)
	}
	for _, source := range []string{domain.ProjectRunSourceScheduler, domain.ProjectRunSourceAPI, domain.ProjectRunSourceManual, "bad"} {
		_ = NormalizeSource(source)
	}

	title := SessionTitle(run)
	tags := MergeSessionTags([]domain.SessionTag{{Name: "project", Value: "project-1"}}, SessionTags(run))
	if title == "" || len(tags) < 4 || WorkspaceID(run, "git") == "" || WorkspaceName(run, "git") == "" {
		t.Fatalf("session helpers title=%q tags=%#v", title, tags)
	}
	cell := domain.NotebookCell{ID: "cell-1", Type: execution.CellTypeAgent, Agent: "codex", AgentSessionID: "agent-session", Output: "output", Success: false, ExitCode: 0, Stderr: "stderr"}
	transition := TransitionFromAgentCell(run, &domain.Session{Summary: domain.SessionSummary{ID: "session-1", WorkspacePath: t.TempDir()}}, cell, nil)
	if transition.ExitCode == 0 || !strings.Contains(transition.Error, "stderr") || transition.ArtifactsDir == "" {
		t.Fatalf("transition from failed cell = %#v", transition)
	}
	transition = TransitionFromAgentCell(run, nil, cell, errors.New("boom"))
	if transition.ExitCode == 0 || !strings.Contains(transition.Error, "boom") {
		t.Fatalf("transition from exec error = %#v", transition)
	}
	if !CleanupPolicyStopsSession(agentcomposev2.RunSessionCleanupPolicy_RUN_SESSION_CLEANUP_POLICY_STOP_ON_COMPLETION) || CleanupPolicyStopsSession(agentcomposev2.RunSessionCleanupPolicy_RUN_SESSION_CLEANUP_POLICY_KEEP_RUNNING) {
		t.Fatalf("cleanup policy mapping failed")
	}
}

func TestIntegrationRunsCoordinatorAndHelperWorkflows(t *testing.T) {
	TestRunsCoordinatorAndHelperWorkflows(t)
}

func TestE2ERunsCoordinatorAndHelperWorkflows(t *testing.T) {
	TestRunsCoordinatorAndHelperWorkflows(t)
}

func TestRunsPreparationWorkspaceAndStatusWorkflows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := `{"variables":[{"name":"PROJECT_VAR","value":"project"}],"workspace":{"provider":"git","url":"https://example.test/repo.git","branch":"main","path":"."},"agents":[{"name":"worker","workspace":{"provider":"local","path":"."}}]}`
	store := &fakePreparationStore{
		project:  domain.ProjectRecord{ID: "project-1", Name: "Project", SourcePath: sourceDir},
		revision: domain.ProjectRevisionRecord{ProjectID: "project-1", Revision: 1, SpecJSON: spec},
		agent:    domain.AgentDefinition{ID: "agent-1", Name: "Agent", EnvItems: []domain.SessionEnvVar{{Name: "AGENT_VAR", Value: "agent"}}, CapsetIDs: []string{"dev"}},
		global:   []domain.SessionEnvVar{{Name: "GLOBAL_VAR", Value: "global"}},
	}
	controller := &Controller{config: &appconfig.Config{DataRoot: root}}
	run := domain.ProjectRunRecord{RunID: "run-1", ProjectID: "project-1", ProjectRevision: 1, ProjectName: "Project", AgentName: "worker", ManagedAgentID: "agent-1"}
	prepared, err := PrepareProjectRun(ctx, store, projectRunWorkspaceResolver{controller: controller}, run, []*agentcomposev2.EnvVarSpec{{Name: "REQUEST_VAR", Value: "request"}})
	if err != nil {
		t.Fatalf("PrepareProjectRun returned error: %v", err)
	}
	if len(prepared.EnvItems) < 3 || prepared.Workspace == nil || prepared.WorkspaceConfig == nil || len(prepared.CapsetIDs) != 1 {
		t.Fatalf("prepared = %#v", prepared)
	}
	decoded, err := DecodeRevisionSpec(spec)
	if err != nil || decoded.GetAgents()[0].GetName() != "worker" {
		t.Fatalf("DecodeRevisionSpec decoded=%#v err=%v", decoded, err)
	}
	if agent, ok := AgentSpecByName(decoded, "worker"); !ok || agent.GetName() != "worker" {
		t.Fatalf("AgentSpecByName failed")
	}
	if _, ok := AgentSpecByName(nil, "worker"); ok {
		t.Fatalf("nil AgentSpecByName should not match")
	}
	if env := EnvItemsFromV2([]*agentcomposev2.EnvVarSpec{nil, {Name: "A", Value: "B"}}); len(env) != 1 {
		t.Fatalf("EnvItemsFromV2 env=%#v", env)
	}
	if ComposeWorkspaceSpecFromV2(nil) != nil || ComposeWorkspaceSpecFromV2(&agentcomposev2.WorkspaceSpec{Provider: "git", Url: "url"}).Provider != "git" {
		t.Fatalf("ComposeWorkspaceSpecFromV2 failed")
	}
	if merged := MergeEnvItems([]domain.SessionEnvVar{{Name: "A", Value: "1"}}, []domain.SessionEnvVar{{Name: "A", Value: "2"}}); len(merged) != 1 || merged[0].Value != "2" {
		t.Fatalf("MergeEnvItems merged=%#v", merged)
	}
	if clean, err := CleanLocalWorkspacePath("."); err != nil || clean != "." {
		t.Fatalf("CleanLocalWorkspacePath clean=%q err=%v", clean, err)
	}
	for _, raw := range []string{"", "/abs", "../escape"} {
		if _, err := CleanLocalWorkspacePath(raw); err == nil {
			t.Fatalf("expected CleanLocalWorkspacePath error for %q", raw)
		}
	}
	if path, err := ResolveLocalProjectWorkspacePath(store.project, "."); err != nil || path == "" {
		t.Fatalf("ResolveLocalProjectWorkspacePath path=%q err=%v", path, err)
	}
	if _, err := projectRunGitWorkspaceConfig(run, &compose.WorkspaceSpec{Provider: "git", URL: "https://example.test/repo.git", Branch: "main", Path: "."}); err != nil {
		t.Fatalf("projectRunGitWorkspaceConfig returned error: %v", err)
	}
	if _, err := projectRunGitWorkspaceConfig(run, &compose.WorkspaceSpec{Provider: "git"}); err == nil {
		t.Fatalf("expected git workspace url error")
	}
	if snapshot := toSessionWorkspaceSnapshot(domain.WorkspaceConfig{ID: "workspace", Name: "Workspace", Type: "file", ConfigJSON: "{}"}); snapshot.ID != "workspace" {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	statusStore := fakeProjectSessionRunStore{runs: []domain.ProjectRunRecord{{RunID: "run-1", SessionID: "session-1"}, {RunID: "run-2", SessionID: "session-1"}, {RunID: "run-3", SessionID: "missing"}}}
	statuses, err := ListProjectSessionStatuses(ctx, statusStore, fakeSessionStatusStore{sessions: map[string]*domain.Session{"session-1": {Summary: domain.SessionSummary{ID: "session-1"}}}}, domain.ProjectSessionRelationFilter{})
	if err != nil || len(statuses) != 2 || statuses[1].SessionMissing != true {
		t.Fatalf("ListProjectSessionStatuses statuses=%#v err=%v", statuses, err)
	}
	if _, err := ListProjectSessionStatuses(ctx, nil, fakeSessionStatusStore{}, domain.ProjectSessionRelationFilter{}); err == nil {
		t.Fatalf("expected nil run store error")
	}
	if _, err := ListProjectSessionStatuses(ctx, statusStore, nil, domain.ProjectSessionRelationFilter{}); err == nil {
		t.Fatalf("expected nil session store error")
	}
}

func TestIntegrationRunsPreparationWorkspaceAndStatusWorkflows(t *testing.T) {
	TestRunsPreparationWorkspaceAndStatusWorkflows(t)
}

func TestE2ERunsPreparationWorkspaceAndStatusWorkflows(t *testing.T) {
	TestRunsPreparationWorkspaceAndStatusWorkflows(t)
}

func TestRunsControllerRunProjectAgentSuccessWorkflow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:      root,
		SessionRoot:   filepath.Join(root, "sessions"),
		RuntimeDriver: "boxlite",
		DefaultImage:  "guest:latest",
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	configDB := &fakeControllerStore{
		project: domain.ProjectRecord{ID: "project-1", Name: "Project", CurrentRevision: 1},
		projectAgent: domain.ProjectAgentRecord{
			ProjectID: "project-1", AgentName: "worker", ManagedAgentID: "agent-1", Driver: "boxlite", Image: "guest:latest",
		},
		managed: ManagedAgentDefinition{
			ID: "agent-1", Enabled: true, Driver: "boxlite", GuestImage: "guest:latest", ManagedProjectID: "project-1", ManagedAgentName: "worker",
		},
		revision: domain.ProjectRevisionRecord{
			ProjectID: "project-1",
			Revision:  1,
			SpecJSON:  `{"agents":[{"name":"worker"}],"variables":[{"name":"PROJECT_VAR","value":"project"}]}`,
		},
		agent:  domain.AgentDefinition{ID: "agent-1", Provider: "codex", Model: "gpt", EnvItems: []domain.SessionEnvVar{{Name: "AGENT_VAR", Value: "agent"}}},
		global: []domain.SessionEnvVar{{Name: "GLOBAL_VAR", Value: "global"}},
		runs:   map[string]domain.ProjectRunRecord{},
	}
	driver := &fakeControllerDriver{}
	executor := &fakeControllerExecutor{}
	bus := &fakeControllerPublisher{}
	dashboard := &fakeControllerDashboard{}
	controller := NewController(ControllerDependencies{
		Config:    config,
		Store:     store,
		ConfigDB:  configDB,
		Driver:    driver,
		Executor:  executor,
		Images:    fakeControllerImages{},
		Bus:       bus,
		Dashboard: dashboard,
	})
	var started bool
	var chunks []domain.ExecChunk
	run, execErr, err := controller.RunProjectAgent(ctx, RunAgentRequest{
		ProjectID:       "project-1",
		AgentName:       "worker",
		Prompt:          "do work",
		Source:          domain.ProjectRunSourceAPI,
		ClientRequestID: "request-1",
		CleanupPolicy:   agentcomposev2.RunSessionCleanupPolicy_RUN_SESSION_CLEANUP_POLICY_STOP_ON_COMPLETION,
	}, &StreamSink{
		SendStarted: func(domain.ProjectRunRecord, time.Time) error {
			started = true
			return nil
		},
		SendChunk: func(_ string, chunk domain.ExecChunk, _ time.Time) error {
			chunks = append(chunks, chunk)
			return nil
		},
	})
	if err != nil || execErr != nil {
		t.Fatalf("RunProjectAgent err=%v execErr=%v run=%#v", err, execErr, run)
	}
	if run.Status != domain.ProjectRunStatusSucceeded || run.SessionID == "" || run.Output != "done" {
		t.Fatalf("run = %#v", run)
	}
	if !started || len(chunks) != 1 || !driver.started || !driver.stopped || executor.request.Message != "do work" {
		t.Fatalf("started=%v chunks=%#v driver=%#v request=%#v", started, chunks, driver, executor.request)
	}
	if len(bus.events) == 0 || len(dashboard.reasons) == 0 {
		t.Fatalf("bus=%#v dashboard=%#v", bus.events, dashboard.reasons)
	}
}

func TestIntegrationRunsControllerRunProjectAgentSuccessWorkflow(t *testing.T) {
	TestRunsControllerRunProjectAgentSuccessWorkflow(t)
}

func TestE2ERunsControllerRunProjectAgentSuccessWorkflow(t *testing.T) {
	TestRunsControllerRunProjectAgentSuccessWorkflow(t)
}

type fakeRunStore struct {
	project      domain.ProjectRecord
	projectAgent domain.ProjectAgentRecord
	agent        ManagedAgentDefinition
	runs         map[string]domain.ProjectRunRecord
}

func (s *fakeRunStore) GetProject(context.Context, string) (domain.ProjectRecord, error) {
	return s.project, nil
}

func (s *fakeRunStore) GetProjectAgent(context.Context, string, string) (domain.ProjectAgentRecord, error) {
	return s.projectAgent, nil
}

func (s *fakeRunStore) GetManagedAgentDefinition(context.Context, string) (ManagedAgentDefinition, error) {
	return s.agent, nil
}

func (s *fakeRunStore) CreateProjectRun(_ context.Context, run domain.ProjectRunRecord) (domain.ProjectRunRecord, error) {
	if _, ok := s.runs[run.RunID]; ok {
		return domain.ProjectRunRecord{}, sql.ErrNoRows
	}
	s.runs[run.RunID] = run
	return run, nil
}

func (s *fakeRunStore) GetProjectRun(_ context.Context, runID string) (domain.ProjectRunRecord, error) {
	run, ok := s.runs[runID]
	if !ok {
		return domain.ProjectRunRecord{}, sql.ErrNoRows
	}
	return run, nil
}

func (s *fakeRunStore) UpdateProjectRun(_ context.Context, run domain.ProjectRunRecord) (domain.ProjectRunRecord, error) {
	s.runs[run.RunID] = run
	return run, nil
}

type fakePreparationStore struct {
	project  domain.ProjectRecord
	revision domain.ProjectRevisionRecord
	agent    domain.AgentDefinition
	global   []domain.SessionEnvVar
}

func (s *fakePreparationStore) GetProject(context.Context, string) (domain.ProjectRecord, error) {
	return s.project, nil
}

func (s *fakePreparationStore) GetProjectRevision(context.Context, string, int64) (domain.ProjectRevisionRecord, error) {
	return s.revision, nil
}

func (s *fakePreparationStore) GetAgentDefinition(context.Context, string) (domain.AgentDefinition, error) {
	return s.agent, nil
}

func (s *fakePreparationStore) ListGlobalEnv(context.Context) ([]domain.SessionEnvVar, error) {
	return s.global, nil
}

type fakeProjectSessionRunStore struct {
	runs []domain.ProjectRunRecord
}

func (s fakeProjectSessionRunStore) ListProjectSessionRuns(context.Context, domain.ProjectSessionRelationFilter) ([]domain.ProjectRunRecord, error) {
	return s.runs, nil
}

type fakeSessionStatusStore struct {
	sessions map[string]*domain.Session
}

func (s fakeSessionStatusStore) GetSession(_ context.Context, id string) (*domain.Session, error) {
	session, ok := s.sessions[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return session, nil
}

type fakeControllerStore struct {
	project      domain.ProjectRecord
	projectAgent domain.ProjectAgentRecord
	managed      ManagedAgentDefinition
	revision     domain.ProjectRevisionRecord
	agent        domain.AgentDefinition
	global       []domain.SessionEnvVar
	runs         map[string]domain.ProjectRunRecord
}

func (s *fakeControllerStore) GetProject(context.Context, string) (domain.ProjectRecord, error) {
	return s.project, nil
}

func (s *fakeControllerStore) GetProjectAgent(context.Context, string, string) (domain.ProjectAgentRecord, error) {
	return s.projectAgent, nil
}

func (s *fakeControllerStore) GetManagedAgentDefinition(context.Context, string) (ManagedAgentDefinition, error) {
	return s.managed, nil
}

func (s *fakeControllerStore) CreateProjectRun(_ context.Context, run domain.ProjectRunRecord) (domain.ProjectRunRecord, error) {
	if s.runs == nil {
		s.runs = map[string]domain.ProjectRunRecord{}
	}
	if _, ok := s.runs[run.RunID]; ok {
		return domain.ProjectRunRecord{}, sql.ErrNoRows
	}
	s.runs[run.RunID] = run
	return run, nil
}

func (s *fakeControllerStore) GetProjectRun(_ context.Context, runID string) (domain.ProjectRunRecord, error) {
	run, ok := s.runs[runID]
	if !ok {
		return domain.ProjectRunRecord{}, sql.ErrNoRows
	}
	return run, nil
}

func (s *fakeControllerStore) UpdateProjectRun(_ context.Context, run domain.ProjectRunRecord) (domain.ProjectRunRecord, error) {
	s.runs[run.RunID] = run
	return run, nil
}

func (s *fakeControllerStore) GetProjectRevision(context.Context, string, int64) (domain.ProjectRevisionRecord, error) {
	return s.revision, nil
}

func (s *fakeControllerStore) GetAgentDefinition(context.Context, string) (domain.AgentDefinition, error) {
	return s.agent, nil
}

func (s *fakeControllerStore) ListGlobalEnv(context.Context) ([]domain.SessionEnvVar, error) {
	return s.global, nil
}

func (s *fakeControllerStore) GetWorkspaceConfig(context.Context, string) (domain.WorkspaceConfig, error) {
	return domain.WorkspaceConfig{}, domain.ErrNotFound
}

type fakeControllerDriver struct {
	started bool
	stopped bool
}

func (d *fakeControllerDriver) StartSessionVM(context.Context, *domain.Session) error {
	d.started = true
	return nil
}

func (d *fakeControllerDriver) StopSessionVM(context.Context, *domain.Session) error {
	d.stopped = true
	return nil
}

type fakeControllerExecutor struct {
	request execution.ExecuteAgentRequest
}

func (e *fakeControllerExecutor) ExecuteAgentRequest(_ context.Context, _ *domain.Session, req execution.ExecuteAgentRequest) (domain.NotebookCell, domain.SessionEvent, domain.SessionEvent, error) {
	e.request = req
	if req.Stream.OnStart != nil {
		if err := req.Stream.OnStart(domain.NotebookCell{ID: "cell-1"}); err != nil {
			return domain.NotebookCell{}, domain.SessionEvent{}, domain.SessionEvent{}, err
		}
	}
	if req.Stream.OnChunk != nil {
		if err := req.Stream.OnChunk("cell-1", domain.ExecChunk{Text: "chunk"}); err != nil {
			return domain.NotebookCell{}, domain.SessionEvent{}, domain.SessionEvent{}, err
		}
	}
	return domain.NotebookCell{ID: "cell-1", Type: execution.CellTypeAgent, Output: "done", Success: true, ExitCode: 0},
		domain.SessionEvent{ID: "user", Type: "user", Message: req.Message},
		domain.SessionEvent{ID: "assistant", Type: "assistant", Message: "done"},
		nil
}

type fakeControllerImages struct{}

func (fakeControllerImages) ListImages(context.Context, images.ListRequest) (images.ListResult, error) {
	return images.ListResult{}, nil
}

func (fakeControllerImages) PullImage(context.Context, images.PullRequest) (images.PullResult, error) {
	return images.PullResult{}, nil
}

func (fakeControllerImages) InspectImage(context.Context, images.InspectRequest) (images.InspectResult, error) {
	return images.InspectResult{}, nil
}

func (fakeControllerImages) RemoveImage(context.Context, images.RemoveRequest) (images.RemoveResult, error) {
	return images.RemoveResult{}, nil
}

type fakeControllerPublisher struct {
	events []domain.LoaderTopicEvent
}

func (p *fakeControllerPublisher) Publish(event domain.LoaderTopicEvent) bool {
	p.events = append(p.events, event)
	return true
}

type fakeControllerDashboard struct {
	reasons []string
}

func (d *fakeControllerDashboard) Notify(reason string) {
	d.reasons = append(d.reasons, reason)
}
