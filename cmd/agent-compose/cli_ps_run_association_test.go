package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestSchedulerRunIsNewer(t *testing.T) {
	tests := []struct {
		name         string
		schedulerRun composeSchedulerRunItem
		projectRun   *agentcomposev2.RunSummary
		want         bool
	}{
		{name: "missing scheduler run", projectRun: &agentcomposev2.RunSummary{RunId: "project-run"}},
		{name: "no project run", schedulerRun: composeSchedulerRunItem{RunID: "scheduler-run"}, want: true},
		{
			name:         "scheduler run is newer",
			schedulerRun: composeSchedulerRunItem{RunID: "scheduler-run", CompletedAt: "2026-07-15T12:00:00.1Z"},
			projectRun:   &agentcomposev2.RunSummary{RunId: "project-run", CompletedAt: "2026-07-15T12:00:00Z"},
			want:         true,
		},
		{
			name:         "project run is newer",
			schedulerRun: composeSchedulerRunItem{RunID: "scheduler-run", CompletedAt: "2026-07-15T11:00:00Z"},
			projectRun:   &agentcomposev2.RunSummary{RunId: "project-run", CreatedAt: "2026-07-15T10:00:00Z", CompletedAt: "2026-07-15T12:00:00Z"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := schedulerRunIsNewer(tt.schedulerRun, tt.projectRun); got != tt.want {
				t.Fatalf("schedulerRunIsNewer() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestIntegrationCLIPSDisplaysLatestSchedulerRunForSandbox(t *testing.T) {
	composePath := writeComposeFile(t, t.TempDir(), `
name: cli-ps-scheduler-run
agents:
  reviewer:
    provider: codex
    scheduler:
      triggers:
        - name: nightly
          cron: "0 2 * * *"
`)
	const sandboxID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	projectRunID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	schedulerRunID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	staleTagRunID := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	projectID := ""
	server := newComposeServiceStubServer(t, composeServiceStubs{
		project: projectServiceStub{
			getProject: func(_ context.Context, req *connect.Request[agentcomposev2.GetProjectRequest]) (*connect.Response[agentcomposev2.GetProjectResponse], error) {
				projectID = req.Msg.GetProject().GetProjectId()
				return connect.NewResponse(&agentcomposev2.GetProjectResponse{Project: testCLIProject(projectID, "cli-ps-scheduler-run", composePath)}), nil
			},
			listSchedulerEvents: func(_ context.Context, req *connect.Request[agentcomposev2.ListSchedulerEventsRequest]) (*connect.Response[agentcomposev2.ListSchedulerEventsResponse], error) {
				if req.Msg.GetAgentName() != "reviewer" {
					t.Fatalf("ListSchedulerEvents agent = %q, want reviewer", req.Msg.GetAgentName())
				}
				return connect.NewResponse(&agentcomposev2.ListSchedulerEventsResponse{Events: []*agentcomposev2.SchedulerEvent{
					{Id: "event-started", Type: "loader.run.started", RunId: schedulerRunID, CreatedAt: timestamppb.New(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))},
					{Id: "event-completed", Type: "loader.run.completed", RunId: schedulerRunID, PayloadJson: `{"sandboxId":"` + sandboxID + `"}`, CreatedAt: timestamppb.New(time.Date(2026, 7, 15, 12, 1, 0, 0, time.UTC))},
				}}), nil
			},
		},
		run: runServiceStub{listRuns: func(context.Context, *connect.Request[agentcomposev2.ListRunsRequest]) (*connect.Response[agentcomposev2.ListRunsResponse], error) {
			return connect.NewResponse(&agentcomposev2.ListRunsResponse{Runs: []*agentcomposev2.RunSummary{{
				RunId: projectRunID, ProjectId: projectID, AgentName: "reviewer", SandboxId: sandboxID, CompletedAt: "2026-07-15T11:00:00Z",
			}}}), nil
		}},
		sandbox: sandboxServiceStub{listSandboxes: func(context.Context, *connect.Request[agentcomposev2.ListSandboxesRequest]) (*connect.Response[agentcomposev2.ListSandboxesResponse], error) {
			return connect.NewResponse(&agentcomposev2.ListSandboxesResponse{Sandboxes: []*agentcomposev2.Sandbox{{
				SandboxId: sandboxID,
				Status:    "running",
				Tags: []*agentcomposev2.SandboxTag{
					{Name: "origin", Value: "scheduler"},
					{Name: "project_id", Value: projectID},
					{Name: "agent", Value: "reviewer"},
					{Name: "scheduler_run_id", Value: staleTagRunID},
				},
			}}}), nil
		}},
	})
	defer server.Close()

	stdout, stderr, _, exitCode := executeCLICommand("ps", "--host", server.URL, "--file", composePath)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("ps code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, schedulerRunID[:12]) || strings.Contains(stdout, projectRunID[:12]) || strings.Contains(stdout, staleTagRunID[:12]) {
		t.Fatalf("ps output = %q, want latest scheduler run %s", stdout, schedulerRunID[:12])
	}
}
