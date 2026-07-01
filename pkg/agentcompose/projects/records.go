package projects

import (
	"agent-compose/pkg/agentcompose/domain"
	"agent-compose/pkg/compose"
	"encoding/json"
	"fmt"
	"strings"
)

func NewRecordFromSpec(spec *compose.NormalizedProjectSpec, sourcePath string) (domain.ProjectRecord, error) {
	if spec == nil {
		return domain.ProjectRecord{}, fmt.Errorf("project spec is required")
	}
	sourcePath = domain.NormalizeProjectSourcePath(sourcePath)
	projectID, err := domain.StableProjectID(spec.Name, sourcePath)
	if err != nil {
		return domain.ProjectRecord{}, err
	}
	specHash, err := spec.Hash()
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("hash project spec: %w", err)
	}
	sourceJSON, err := EncodeSourceJSON(sourcePath)
	if err != nil {
		return domain.ProjectRecord{}, err
	}
	return domain.ProjectRecord{
		ID:         projectID,
		Name:       strings.TrimSpace(spec.Name),
		SourcePath: sourcePath,
		SourceJSON: sourceJSON,
		SpecHash:   specHash,
	}, nil
}

func NewAgentRecordFromSpec(projectID string, revision int64, agent compose.NormalizedAgentSpec) (domain.ProjectAgentRecord, error) {
	managedAgentID, err := domain.StableManagedAgentID(projectID, agent.Name)
	if err != nil {
		return domain.ProjectAgentRecord{}, err
	}
	specJSON, err := MarshalCanonicalJSON(agent)
	if err != nil {
		return domain.ProjectAgentRecord{}, fmt.Errorf("marshal project agent %s spec: %w", agent.Name, err)
	}
	driver := ""
	if agent.Driver != nil {
		driver = agent.Driver.Name
	}
	return domain.ProjectAgentRecord{
		ProjectID:        strings.TrimSpace(projectID),
		AgentName:        strings.TrimSpace(agent.Name),
		ManagedAgentID:   managedAgentID,
		Revision:         revision,
		Provider:         strings.TrimSpace(agent.Provider),
		Model:            strings.TrimSpace(agent.Model),
		Image:            strings.TrimSpace(agent.Image),
		Driver:           strings.TrimSpace(driver),
		SchedulerEnabled: agent.Scheduler != nil && agent.Scheduler.Enabled,
		SpecJSON:         string(specJSON),
	}, nil
}

func NewSchedulerRecordFromSpec(projectID string, revision int64, agent compose.NormalizedAgentSpec) (domain.ProjectSchedulerRecord, bool, error) {
	if agent.Scheduler == nil {
		return domain.ProjectSchedulerRecord{}, false, nil
	}
	schedulerID, err := domain.StableProjectSchedulerID(projectID, agent.Name, "")
	if err != nil {
		return domain.ProjectSchedulerRecord{}, false, err
	}
	loaderID, err := domain.StableManagedLoaderID(projectID, agent.Name, "")
	if err != nil {
		return domain.ProjectSchedulerRecord{}, false, err
	}
	specJSON, err := MarshalCanonicalJSON(agent.Scheduler)
	if err != nil {
		return domain.ProjectSchedulerRecord{}, false, fmt.Errorf("marshal project scheduler %s spec: %w", agent.Name, err)
	}
	return domain.ProjectSchedulerRecord{
		ProjectID:       strings.TrimSpace(projectID),
		SchedulerID:     schedulerID,
		AgentName:       strings.TrimSpace(agent.Name),
		ManagedLoaderID: loaderID,
		Revision:        revision,
		Enabled:         agent.Scheduler.Enabled,
		TriggerCount:    len(agent.Scheduler.Triggers),
		SpecJSON:        string(specJSON),
	}, true, nil
}

func EncodeSourceJSON(sourcePath string) (string, error) {
	data, err := json.Marshal(struct {
		ComposePath string `json:"compose_path,omitempty"`
	}{ComposePath: domain.NormalizeProjectSourcePath(sourcePath)})
	if err != nil {
		return "", fmt.Errorf("marshal project source: %w", err)
	}
	return string(data), nil
}

func MarshalCanonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return data, nil
}
