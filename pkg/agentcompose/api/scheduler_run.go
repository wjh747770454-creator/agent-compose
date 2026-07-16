package api

import (
	"strings"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func schedulerRunToProto(run domain.LoaderRunSummary, scheduler domain.ProjectSchedulerRecord) *agentcomposev2.SchedulerRun {
	return &agentcomposev2.SchedulerRun{
		RunId:              run.ID,
		ProjectId:          scheduler.ProjectID,
		AgentName:          scheduler.AgentName,
		SchedulerId:        scheduler.SchedulerID,
		TriggerId:          run.TriggerID,
		TriggerKind:        run.TriggerKind,
		TriggerSource:      run.TriggerSource,
		Status:             schedulerRunStatusToProto(run.Status),
		StartedAt:          projectTimestamp(run.StartedAt),
		CompletedAt:        projectTimestamp(run.CompletedAt),
		DurationMs:         run.DurationMs,
		Error:              run.Error,
		ResultJson:         run.ResultJSON,
		PayloadJson:        run.PayloadJSON,
		SourceScriptSha256: run.SourceScriptHash,
		ArtifactsDir:       run.ArtifactsDir,
	}
}

func schedulerRunStatusToProto(status string) agentcomposev2.SchedulerRunStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case domain.LoaderRunStatusRunning:
		return agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_RUNNING
	case domain.LoaderRunStatusSucceeded:
		return agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED
	case domain.LoaderRunStatusFailed:
		return agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_FAILED
	case domain.LoaderRunStatusCanceled:
		return agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_CANCELED
	case domain.LoaderRunStatusSkipped:
		return agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SKIPPED
	default:
		return agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_UNSPECIFIED
	}
}
