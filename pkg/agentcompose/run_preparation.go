package agentcompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"agent-compose/pkg/agentcompose/capabilities"
	"agent-compose/pkg/agentcompose/runs"
	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type ProjectRunPreparation = runs.Preparation

func (s *Service) prepareProjectRun(ctx context.Context, run ProjectRunRecord, requestEnv []*agentcomposev2.EnvVarSpec) (ProjectRunPreparation, error) {
	if s == nil || s.configDB == nil {
		return ProjectRunPreparation{}, fmt.Errorf("config store is required")
	}
	project, err := s.configDB.GetProject(ctx, run.ProjectID)
	if err != nil {
		return ProjectRunPreparation{}, fmt.Errorf("resolve project %s: %w", run.ProjectID, err)
	}
	revision, err := s.configDB.GetProjectRevision(ctx, run.ProjectID, run.ProjectRevision)
	if err != nil {
		return ProjectRunPreparation{}, fmt.Errorf("resolve project revision %s/%d: %w", run.ProjectID, run.ProjectRevision, err)
	}
	spec, err := runs.DecodeRevisionSpec(revision.SpecJSON)
	if err != nil {
		return ProjectRunPreparation{}, err
	}
	agentSpec, ok := runs.AgentSpecByName(spec, run.AgentName)
	if !ok {
		return ProjectRunPreparation{}, fmt.Errorf("project revision %s/%d missing agent %s", run.ProjectID, run.ProjectRevision, run.AgentName)
	}
	agent, err := s.configDB.GetAgentDefinition(ctx, run.ManagedAgentID)
	if err != nil {
		return ProjectRunPreparation{}, fmt.Errorf("resolve managed agent definition %s: %w", run.ManagedAgentID, err)
	}
	globalEnv, err := s.configDB.ListGlobalEnv(ctx)
	if err != nil {
		return ProjectRunPreparation{}, fmt.Errorf("list global env: %w", err)
	}
	envItems := runs.MergeEnvItems(
		globalEnv,
		runs.EnvItemsFromV2(spec.GetVariables()),
		agent.EnvItems,
		runs.EnvItemsFromV2(requestEnv),
	)
	providerEnvItems := envItems
	envItems = filterPersistedRuntimeEnv(envItems)
	workspace, err := s.prepareProjectRunWorkspace(ctx, run, project, runs.ComposeWorkspaceSpecFromV2(spec.GetWorkspace()), runs.ComposeWorkspaceSpecFromV2(agentSpec.GetWorkspace()))
	if err != nil {
		return ProjectRunPreparation{}, err
	}
	prepared := ProjectRunPreparation{EnvItems: envItems, ProviderEnvItems: providerEnvItems, CapsetIDs: capabilities.NormalizeCapsetIDs(agent.CapsetIDs)}
	if workspace != nil {
		prepared.WorkspaceConfig = workspace
		prepared.Workspace = toSessionWorkspaceSnapshot(*workspace)
	}
	return prepared, nil
}

func (s *Service) prepareProjectRunWorkspace(ctx context.Context, run ProjectRunRecord, project ProjectRecord, projectWorkspace, agentWorkspace *compose.WorkspaceSpec) (*WorkspaceConfig, error) {
	_ = ctx
	workspace := projectWorkspace
	if agentWorkspace != nil {
		workspace = agentWorkspace
	}
	if workspace == nil {
		return nil, nil
	}
	provider := strings.ToLower(strings.TrimSpace(workspace.Provider))
	switch provider {
	case "local":
		config, err := s.materializeLocalProjectRunWorkspace(run, project, workspace)
		if err != nil {
			return nil, err
		}
		return &config, nil
	case "git":
		config, err := projectRunGitWorkspaceConfig(run, workspace)
		if err != nil {
			return nil, err
		}
		return &config, nil
	default:
		if provider == "" {
			return nil, fmt.Errorf("workspace provider is required")
		}
		return nil, fmt.Errorf("unsupported workspace provider %q", workspace.Provider)
	}
}

func (s *Service) materializeLocalProjectRunWorkspace(run ProjectRunRecord, project ProjectRecord, workspace *compose.WorkspaceSpec) (WorkspaceConfig, error) {
	if s == nil || s.config == nil {
		return WorkspaceConfig{}, fmt.Errorf("config is required")
	}
	sourceDir, err := runs.ResolveLocalProjectWorkspacePath(project, workspace.Path)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	workspaceID := projectRunWorkspaceID(run, "local")
	configJSON := defaultFileWorkspaceConfigJSON(s.config, workspaceID)
	if _, err := validateFileWorkspaceConfig(s.config, workspaceID, configJSON); err != nil {
		return WorkspaceConfig{}, err
	}
	if err := resetFileWorkspaceSnapshotContent(s.config, workspaceID); err != nil {
		return WorkspaceConfig{}, err
	}
	config := WorkspaceConfig{
		ID:         workspaceID,
		Name:       projectRunWorkspaceName(run, "local"),
		Type:       "file",
		ConfigJSON: configJSON,
		Comment:    fmt.Sprintf("project run %s local workspace snapshot", run.RunID),
	}
	content, err := openFileWorkspaceContent(s.config, config)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	defer func() { _ = content.Root.Close() }()
	sourceRoot, err := os.OpenRoot(sourceDir)
	if err != nil {
		return WorkspaceConfig{}, fmt.Errorf("open local workspace source %s: %w", sourceDir, err)
	}
	defer func() { _ = sourceRoot.Close() }()
	if err := copyRootDirectoryContents(sourceRoot, content.AbsRoot); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("materialize local workspace snapshot: %w", err)
	}
	return config, nil
}

func projectRunGitWorkspaceConfig(run ProjectRunRecord, workspace *compose.WorkspaceSpec) (WorkspaceConfig, error) {
	workspaceID := projectRunWorkspaceID(run, "git")
	if strings.TrimSpace(workspace.URL) == "" {
		return WorkspaceConfig{}, fmt.Errorf("git workspace url is required")
	}
	if _, err := normalizeGitCloneTarget(workspaceID, workspace.Path); err != nil {
		return WorkspaceConfig{}, err
	}
	payload, err := json.Marshal(gitWorkspaceConfig{
		URL:         strings.TrimSpace(workspace.URL),
		Branch:      strings.TrimSpace(workspace.Branch),
		CloneTarget: strings.TrimSpace(workspace.Path),
	})
	if err != nil {
		return WorkspaceConfig{}, fmt.Errorf("encode git workspace config: %w", err)
	}
	return WorkspaceConfig{
		ID:         workspaceID,
		Name:       projectRunWorkspaceName(run, "git"),
		Type:       "git",
		ConfigJSON: string(payload),
		Comment:    fmt.Sprintf("project run %s git workspace snapshot", run.RunID),
	}, nil
}

func projectRunWorkspaceID(run ProjectRunRecord, provider string) string {
	return runs.WorkspaceID(run, provider)
}

func projectRunWorkspaceName(run ProjectRunRecord, provider string) string {
	return runs.WorkspaceName(run, provider)
}

func resetFileWorkspaceSnapshotContent(config *appconfig.Config, workspaceID string) error {
	dataRoot, err := openFileWorkspaceDataRoot(config)
	if err != nil {
		return err
	}
	defer func() { _ = dataRoot.Close() }()
	relRoot, err := fileWorkspaceContentRelRoot(workspaceID)
	if err != nil {
		return err
	}
	if err := dataRoot.RemoveAll(relRoot); err != nil {
		return fmt.Errorf("reset local workspace snapshot %s: %w", workspaceID, err)
	}
	return nil
}
