package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	domain "agent-compose/pkg/model"
)

type schedulerRunPageCursor struct {
	ProjectID string    `json:"project_id"`
	AgentName string    `json:"agent_name,omitempty"`
	StartedAt time.Time `json:"started_at"`
	LoaderID  string    `json:"loader_id"`
	RunID     string    `json:"run_id"`
}

func encodeSchedulerRunCursor(projectID, agentName string, run domain.LoaderRunSummary) string {
	payload, _ := json.Marshal(schedulerRunPageCursor{
		ProjectID: strings.TrimSpace(projectID),
		AgentName: strings.TrimSpace(agentName),
		StartedAt: run.StartedAt.UTC(),
		LoaderID:  strings.TrimSpace(run.LoaderID),
		RunID:     strings.TrimSpace(run.ID),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeSchedulerRunCursor(token, projectID, agentName string) (schedulerRunPageCursor, error) {
	projectID = strings.TrimSpace(projectID)
	agentName = strings.TrimSpace(agentName)
	if strings.TrimSpace(token) == "" {
		return schedulerRunPageCursor{ProjectID: projectID, AgentName: agentName}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return schedulerRunPageCursor{}, fmt.Errorf("invalid cursor")
	}
	var cursor schedulerRunPageCursor
	if json.Unmarshal(payload, &cursor) != nil ||
		cursor.ProjectID != projectID || cursor.AgentName != agentName ||
		cursor.StartedAt.IsZero() || strings.TrimSpace(cursor.LoaderID) == "" || strings.TrimSpace(cursor.RunID) == "" {
		return schedulerRunPageCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}
