package runs

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-compose/pkg/agentcompose/domain"
	"agent-compose/pkg/compose"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type Preparation struct {
	EnvItems         []domain.SessionEnvVar
	ProviderEnvItems []domain.SessionEnvVar
	CapsetIDs        []string
	WorkspaceConfig  *domain.WorkspaceConfig
	Workspace        *domain.SessionWorkspace
}

func DecodeRevisionSpec(raw string) (*agentcomposev2.ProjectSpec, error) {
	var spec agentcomposev2.ProjectSpec
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &spec); err != nil {
		return nil, fmt.Errorf("decode project revision spec: %w", err)
	}
	return &spec, nil
}

func AgentSpecByName(spec *agentcomposev2.ProjectSpec, name string) (*agentcomposev2.AgentSpec, bool) {
	if spec == nil {
		return nil, false
	}
	name = strings.TrimSpace(name)
	for _, agent := range spec.GetAgents() {
		if agent.GetName() == name {
			return agent, true
		}
	}
	return nil, false
}

func EnvItemsFromV2(items []*agentcomposev2.EnvVarSpec) []domain.SessionEnvVar {
	env := make([]domain.SessionEnvVar, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		env = append(env, domain.SessionEnvVar{
			Name:   item.GetName(),
			Value:  item.GetValue(),
			Secret: item.GetSecret(),
		})
	}
	return domain.NormalizeEnvItems(env)
}

func ComposeWorkspaceSpecFromV2(workspace *agentcomposev2.WorkspaceSpec) *compose.WorkspaceSpec {
	if workspace == nil {
		return nil
	}
	return &compose.WorkspaceSpec{
		Provider: workspace.GetProvider(),
		URL:      workspace.GetUrl(),
		Branch:   workspace.GetBranch(),
		Path:     workspace.GetPath(),
	}
}

func MergeEnvItems(groups ...[]domain.SessionEnvVar) []domain.SessionEnvVar {
	var merged []domain.SessionEnvVar
	for _, group := range groups {
		merged = domain.MergeEnvItems(merged, group)
	}
	return merged
}
