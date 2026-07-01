package agentcompose

import (
	"agent-compose/pkg/agentcompose/domain"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

const (
	defaultAgentProvider = domain.DefaultAgentProvider

	agentSessionTagSource    = domain.AgentSessionTagSource
	agentSessionTagSourceVal = domain.AgentSessionTagSourceVal
	agentSessionTagID        = domain.AgentSessionTagID
	agentSessionTagName      = domain.AgentSessionTagName
)

type (
	AgentDefinition            = domain.AgentDefinition
	AgentDefinitionListOptions = domain.AgentDefinitionListOptions
	AgentDefinitionListResult  = domain.AgentDefinitionListResult
	AgentCurrentRunSummary     = domain.AgentCurrentRunSummary
	AgentLatestRunSummary      = domain.AgentLatestRunSummary
)

type AgentValidationResult struct {
	Availability agentcomposev1.AgentAvailabilityStatus
	Health       agentcomposev1.AgentHealthStatus
	Warnings     []string
	Errors       []string
}
