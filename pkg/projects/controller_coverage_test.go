package projects

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
)

func TestControllerValidateApplyDryRunAndResolveWorkflows(t *testing.T) {
	ctx := context.Background()
	raw, err := compose.Parse([]byte(`
name: coverage-project
variables:
  SHARED: value
agents:
  worker:
    provider: codex
    model: gpt-test
    image: guest:latest
    driver:
      boxlite: {}
    env:
      AGENT_ENV: agent
    capset_ids: [dev]
    scheduler:
      script: |
        export default { triggers: [{ name: "daily", cron: "0 0 * * *", prompt: "run" }] }
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	normalizedSpec, err := compose.Normalize(raw, compose.NormalizeOptions{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	hash, err := normalizedSpec.Hash()
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	store := &controllerCoverageStore{
		projects: []domain.ProjectRecord{
			{ID: "project-1", Name: "coverage-project", SourcePath: "/one"},
			{ID: "project-2", Name: "coverage-project", SourcePath: "/two"},
		},
	}
	controller := NewController(ControllerDependencies{
		Config:  &appconfig.Config{RuntimeDriver: "boxlite"},
		Store:   store,
		Loaders: controllerCoverageLoaderValidator{},
	})
	normalized := NormalizedProject{Spec: normalizedSpec, SpecHash: hash, SourcePath: "/repo/agent-compose.yaml"}
	validation, err := controller.ValidateProject(ctx, normalized, nil)
	if err != nil || !validation.Valid || validation.SpecHash != hash {
		t.Fatalf("ValidateProject validation=%#v err=%v", validation, err)
	}
	dryRun, err := controller.ApplyProject(ctx, ApplyRequest{Normalized: normalized, DryRun: true})
	if err != nil {
		t.Fatalf("ApplyProject dry-run returned error: %v", err)
	}
	if dryRun.Applied || len(dryRun.Agents) != 1 || len(dryRun.Schedulers) != 1 || len(dryRun.Changes) < 4 {
		t.Fatalf("dryRun = %#v", dryRun)
	}
	if issue, err := controller.ApplyProject(ctx, ApplyRequest{Issues: []ValidationIssue{{Path: "x", Message: "bad"}}, Normalized: normalized}); err != nil || len(issue.Issues) != 1 {
		t.Fatalf("ApplyProject issues=%#v err=%v", issue, err)
	}
	if missingSpec, err := controller.ValidateProject(ctx, NormalizedProject{}, nil); err != nil || missingSpec.Valid {
		t.Fatalf("ValidateProject missing spec=%#v err=%v", missingSpec, err)
	}
	if _, err := controller.ResolveProjectRef(ctx, ProjectRef{Name: "coverage-project"}); !errors.Is(err, domain.ErrAmbiguous) {
		t.Fatalf("expected ambiguous project error, got %v", err)
	}
	store.projects = []domain.ProjectRecord{{ID: "project-1", Name: "coverage-project", SourcePath: "/repo"}}
	resolved, err := controller.ResolveProjectRef(ctx, ProjectRef{Name: "coverage-project"})
	if err != nil || resolved.ID != "project-1" {
		t.Fatalf("ResolveProjectRef resolved=%#v err=%v", resolved, err)
	}
	if _, err := controller.ResolveProjectRef(ctx, ProjectRef{}); !errors.Is(err, domain.ErrRequired) {
		t.Fatalf("expected required project error, got %v", err)
	}
}

func TestIntegrationControllerValidateApplyDryRunAndResolveWorkflows(t *testing.T) {
	TestControllerValidateApplyDryRunAndResolveWorkflows(t)
}

func TestE2EControllerValidateApplyDryRunAndResolveWorkflows(t *testing.T) {
	TestControllerValidateApplyDryRunAndResolveWorkflows(t)
}

type controllerCoverageLoaderValidator struct{}

func (controllerCoverageLoaderValidator) Validate(context.Context, string, string) (loaders.LoaderValidationResult, error) {
	return loaders.LoaderValidationResult{Triggers: []domain.LoaderTrigger{{ID: "daily", Kind: domain.LoaderTriggerKindCron, Enabled: true, SpecJSON: `{"expr":"0 0 * * *"}`}}}, nil
}

func (controllerCoverageLoaderValidator) Refresh(context.Context) error {
	return nil
}

type controllerCoverageStore struct {
	projects []domain.ProjectRecord
}

func (s *controllerCoverageStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, error) {
	for _, project := range s.projects {
		if project.ID == id {
			return project, nil
		}
	}
	return domain.ProjectRecord{}, sql.ErrNoRows
}

func (s *controllerCoverageStore) GetProjectIfExists(context.Context, string, bool) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{}, false, nil
}

func (s *controllerCoverageStore) ListProjects(context.Context, domain.ProjectListOptions) (domain.ProjectListResult, error) {
	return domain.ProjectListResult{Projects: s.projects}, nil
}

func (s *controllerCoverageStore) UpsertProject(context.Context, domain.ProjectRecord) (domain.ProjectRecord, error) {
	return domain.ProjectRecord{}, nil
}

func (s *controllerCoverageStore) SaveProjectRevision(context.Context, domain.ProjectRevisionRecord) (domain.ProjectRevisionRecord, bool, error) {
	return domain.ProjectRevisionRecord{}, false, nil
}

func (s *controllerCoverageStore) GetProjectAgent(context.Context, string, string) (domain.ProjectAgentRecord, error) {
	return domain.ProjectAgentRecord{}, sql.ErrNoRows
}

func (s *controllerCoverageStore) UpsertProjectAgent(context.Context, domain.ProjectAgentRecord) (domain.ProjectAgentRecord, error) {
	return domain.ProjectAgentRecord{}, nil
}

func (s *controllerCoverageStore) ListProjectAgents(context.Context, string) ([]domain.ProjectAgentRecord, error) {
	return nil, nil
}

func (s *controllerCoverageStore) ListProjectSchedulers(context.Context, string) ([]domain.ProjectSchedulerRecord, error) {
	return nil, nil
}

func (s *controllerCoverageStore) GetAgentDefinitionIfExists(context.Context, string, bool) (domain.AgentDefinition, bool, error) {
	return domain.AgentDefinition{}, false, nil
}

func (s *controllerCoverageStore) UpsertManagedAgentDefinition(context.Context, domain.AgentDefinition) (domain.AgentDefinition, error) {
	return domain.AgentDefinition{}, nil
}

func (s *controllerCoverageStore) ListManagedAgentDefinitions(context.Context, string, bool) ([]domain.AgentDefinition, error) {
	return nil, nil
}

func (s *controllerCoverageStore) SetAgentDefinitionEnabled(context.Context, string, bool) (domain.AgentDefinition, error) {
	return domain.AgentDefinition{}, nil
}

func (s *controllerCoverageStore) GetProjectScheduler(context.Context, string, string) (domain.ProjectSchedulerRecord, error) {
	return domain.ProjectSchedulerRecord{}, sql.ErrNoRows
}

func (s *controllerCoverageStore) UpsertProjectScheduler(context.Context, domain.ProjectSchedulerRecord) (domain.ProjectSchedulerRecord, error) {
	return domain.ProjectSchedulerRecord{}, nil
}

func (s *controllerCoverageStore) SetProjectSchedulerEnabled(context.Context, string, string, bool) (domain.ProjectSchedulerRecord, error) {
	return domain.ProjectSchedulerRecord{}, nil
}

func (s *controllerCoverageStore) GetLoaderIfExists(context.Context, string) (domain.Loader, bool, error) {
	return domain.Loader{}, false, nil
}

func (s *controllerCoverageStore) UpsertManagedLoader(context.Context, domain.Loader) (domain.Loader, error) {
	return domain.Loader{}, nil
}

func (s *controllerCoverageStore) ReplaceLoaderTriggers(context.Context, string, []domain.LoaderTrigger) ([]domain.LoaderTrigger, error) {
	return nil, nil
}

func (s *controllerCoverageStore) SetLoaderEnabled(context.Context, string, bool) error {
	return nil
}
