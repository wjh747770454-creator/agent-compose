package main

import (
	"context"
	"strings"
	"time"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func latestSchedulerRunsBySandbox(ctx context.Context, client agentcomposev2connect.ProjectServiceClient, project *agentcomposev2.Project, sessions []*agentcomposev2.Sandbox) (map[string]composeSchedulerRunItem, error) {
	projectID := strings.TrimSpace(project.GetSummary().GetProjectId())
	schedulerAgents := make(map[string]bool)
	for _, session := range sessions {
		tags := sessionTagsMap(session.GetTags())
		if tags["origin"] == "scheduler" && tags["project_id"] == projectID && tags["agent"] != "" {
			schedulerAgents[tags["agent"]] = true
		}
	}
	result := make(map[string]composeSchedulerRunItem)
	for _, scheduler := range project.GetSchedulers() {
		agentName := strings.TrimSpace(scheduler.GetAgentName())
		if !schedulerAgents[agentName] {
			continue
		}
		loaderID, err := domain.StableManagedLoaderID(projectID, agentName, "")
		if err != nil {
			return nil, err
		}
		runs, err := listSchedulerRuntimeRuns(ctx, client, projectID, agentName, scheduler.GetSchedulerId(), loaderID, 500)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			for _, sandboxID := range run.SandboxIDs {
				current, ok := result[sandboxID]
				if !ok || runTimestampAfter(schedulerRunSortTime(run), schedulerRunSortTime(current)) {
					result[sandboxID] = run
				}
			}
		}
	}
	return result, nil
}

func schedulerRunIsNewer(schedulerRun composeSchedulerRunItem, projectRun *agentcomposev2.RunSummary) bool {
	if strings.TrimSpace(schedulerRun.RunID) == "" {
		return false
	}
	if projectRun == nil {
		return true
	}
	return runTimestampAfter(schedulerRunSortTime(schedulerRun), projectRunAssociationSortTime(projectRun))
}

func schedulerRunSortTime(run composeSchedulerRunItem) string {
	return firstNonEmptyString(run.CompletedAt, run.StartedAt)
}

func projectRunAssociationSortTime(run *agentcomposev2.RunSummary) string {
	return firstNonEmptyString(run.GetUpdatedAt(), run.GetCompletedAt(), run.GetStartedAt(), run.GetCreatedAt())
}

func runTimestampAfter(candidate, current string) bool {
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	if candidateErr == nil && currentErr == nil {
		return candidateTime.After(currentTime)
	}
	return candidate > current
}
