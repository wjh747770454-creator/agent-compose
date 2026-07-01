package execution

import (
	"agent-compose/pkg/agentcompose/domain"
	"time"
)

const (
	CellTypeShell      = "shell"
	CellTypeJavaScript = "javascript"
	CellTypePython     = "python"
	CellTypeAgent      = "agent"
)

type CellExecutionStream struct {
	OnStart func(domain.NotebookCell) error
	OnChunk func(string, domain.ExecChunk) error
}

type AgentExecutionStream struct {
	OnStart func(domain.NotebookCell) error
	OnChunk func(string, domain.ExecChunk) error
}

type ExecuteAgentRequest struct {
	Agent             string
	AgentDefinitionID string
	Model             string
	ProviderEnvItems  []domain.SessionEnvVar
	RunID             string
	Message           string
	Timeout           time.Duration
	OutputSchemaJSON  string
	Stream            AgentExecutionStream
}
