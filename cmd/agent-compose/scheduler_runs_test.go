package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"

	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestIntegrationCLISchedulerRunMainAndDetach(t *testing.T) {
	composePath := writeComposeFile(t, t.TempDir(), `
name: cli-scheduler-main
agents:
  reviewer:
    provider: codex
    scheduler:
      enabled: true
      script: |
        function main(payload) { return payload; }
`)
	var runRequests []*agentcomposev2.RunSchedulerRequest
	var startRequests []*agentcomposev2.StartSchedulerRunRequest
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		runScheduler: func(_ context.Context, req *connect.Request[agentcomposev2.RunSchedulerRequest]) (*connect.Response[agentcomposev2.RunSchedulerResponse], error) {
			runRequests = append(runRequests, req.Msg)
			return connect.NewResponse(&agentcomposev2.RunSchedulerResponse{Run: &agentcomposev2.SchedulerRun{
				RunId:       "scheduler-run-main",
				ProjectId:   req.Msg.GetProject().GetProjectId(),
				AgentName:   req.Msg.GetAgentName(),
				Status:      agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED,
				ResultJson:  `{"main":true}`,
				PayloadJson: req.Msg.GetPayloadJson(),
			}}), nil
		},
		startSchedulerRun: func(_ context.Context, req *connect.Request[agentcomposev2.StartSchedulerRunRequest]) (*connect.Response[agentcomposev2.StartSchedulerRunResponse], error) {
			startRequests = append(startRequests, req.Msg)
			return connect.NewResponse(&agentcomposev2.StartSchedulerRunResponse{Run: &agentcomposev2.SchedulerRun{
				RunId:     "scheduler-run-detached",
				ProjectId: req.Msg.GetProject().GetProjectId(),
				AgentName: req.Msg.GetAgentName(),
				Status:    agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_RUNNING,
			}}), nil
		},
	}})
	defer server.Close()

	stdout, stderr, _, exitCode := executeCLICommand("scheduler", "run", "--host", server.URL, "--file", composePath, "--payload", ` { "main" : true } `, "reviewer")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "Trigger: main") || !strings.Contains(stdout, `Result: {"main":true}`) {
		t.Fatalf("scheduler run code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
	}
	if len(runRequests) != 1 || runRequests[0].GetTriggerId() != "" || runRequests[0].GetPayloadJson() != `{"main":true}` {
		t.Fatalf("RunScheduler requests = %#v", runRequests)
	}

	detachedOut, detachedErr, _, detachedCode := executeCLICommand("scheduler", "run", "--host", server.URL, "--file", composePath, "--detach", "reviewer")
	if detachedCode != 0 || detachedErr != "" || !strings.Contains(detachedOut, "Run: scheduler-run-detached") || !strings.Contains(detachedOut, "Inspect: ") || !strings.Contains(detachedOut, "inspect run scheduler-run-detached") || !strings.Contains(detachedOut, "scheduler stop scheduler-run-detached") {
		t.Fatalf("scheduler run --detach code/stdout/stderr = %d / %q / %q", detachedCode, detachedOut, detachedErr)
	}
	if len(startRequests) != 1 || startRequests[0].GetTriggerId() != "" {
		t.Fatalf("StartSchedulerRun requests = %#v", startRequests)
	}
}

func TestCLISchedulerExecutionRejectsUnsupportedFlags(t *testing.T) {
	composePath := writeComposeFile(t, t.TempDir(), `
name: cli-scheduler-unsupported
agents:
  reviewer:
    scheduler:
      triggers:
        - name: nightly
          cron: "0 2 * * *"
          prompt: review
`)
	tests := []struct {
		name string
		args []string
		flag string
	}{
		{name: "prompt", args: []string{"--prompt", "override"}, flag: "--prompt"},
		{name: "sandbox", args: []string{"--sandbox", "sandbox-1"}, flag: "--sandbox"},
		{name: "driver", args: []string{"--driver", "docker"}, flag: "--driver"},
		{name: "keep running", args: []string{"--keep-running"}, flag: "--keep-running"},
		{name: "remove false", args: []string{"--rm=false"}, flag: "--rm"},
		{name: "jupyter", args: []string{"--jupyter"}, flag: "--jupyter"},
		{name: "jupyter expose", args: []string{"--jupyter-expose"}, flag: "--jupyter-expose"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"scheduler", "trigger", "--file", composePath}
			args = append(args, test.args...)
			args = append(args, "reviewer", "nightly")
			stdout, stderr, _, exitCode := executeCLICommand(args...)
			if exitCode != exitCodeUsage || stdout != "" || !strings.Contains(stderr, test.flag+" is unsupported for complete scheduler runs") {
				t.Fatalf("unsupported flag code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
			}
		})
	}
	stdout, stderr, _, exitCode := executeCLICommand("scheduler", "run", "--file", composePath, "--prompt", "override", "reviewer")
	if exitCode != exitCodeUsage || stdout != "" || !strings.Contains(stderr, "scheduler run --prompt is unsupported for complete scheduler runs") {
		t.Fatalf("scheduler run unsupported flag code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
	}
}

func TestIntegrationCLISchedulerRunsPaginatesAndFiltersAgent(t *testing.T) {
	composePath := writeComposeFile(t, t.TempDir(), `
name: cli-scheduler-runs
agents:
  reviewer:
    scheduler:
      enabled: true
      script: function main() {}
`)
	var requests []*agentcomposev2.ListSchedulerRunsRequest
	newRunID := "11111111-1111-4111-8111-111111111111"
	oldRunID := "22222222-2222-4222-8222-222222222222"
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		listSchedulerRuns: func(_ context.Context, req *connect.Request[agentcomposev2.ListSchedulerRunsRequest]) (*connect.Response[agentcomposev2.ListSchedulerRunsResponse], error) {
			requests = append(requests, req.Msg)
			if req.Msg.GetCursor() == "" {
				return connect.NewResponse(&agentcomposev2.ListSchedulerRunsResponse{
					Runs:       []*agentcomposev2.SchedulerRun{{RunId: newRunID, AgentName: "reviewer", Status: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED}},
					NextCursor: "page-2",
				}), nil
			}
			return connect.NewResponse(&agentcomposev2.ListSchedulerRunsResponse{
				Runs: []*agentcomposev2.SchedulerRun{{RunId: oldRunID, AgentName: "reviewer", Status: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SKIPPED}},
			}), nil
		},
	}})
	defer server.Close()

	stdout, stderr, _, exitCode := executeCLICommand("scheduler", "runs", "--host", server.URL, "--file", composePath, "reviewer")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, shortOpaqueID(newRunID)) || !strings.Contains(stdout, shortOpaqueID(oldRunID)) || !strings.Contains(stdout, "skipped") {
		t.Fatalf("scheduler runs code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
	}
	if len(requests) != 2 || requests[0].GetAgentName() != "reviewer" || requests[0].GetCursor() != "" || requests[1].GetCursor() != "page-2" {
		t.Fatalf("ListSchedulerRuns requests = %#v", requests)
	}

	requests = nil
	filteredOut, filteredErr, _, filteredCode := executeCLICommand("scheduler", "runs", "--host", server.URL, "--file", composePath, "--status", "skipped", "reviewer")
	if filteredCode != 0 || filteredErr != "" || !strings.Contains(filteredOut, shortOpaqueID(oldRunID)) || strings.Contains(filteredOut, shortOpaqueID(newRunID)) {
		t.Fatalf("scheduler runs --status skipped code/stdout/stderr = %d / %q / %q", filteredCode, filteredOut, filteredErr)
	}
	if len(requests) != 2 || requests[0].GetCursor() != "" || requests[1].GetCursor() != "page-2" {
		t.Fatalf("filtered ListSchedulerRuns requests = %#v", requests)
	}
}

func TestIntegrationCLISchedulerStop(t *testing.T) {
	composePath := writeComposeFile(t, t.TempDir(), `
name: cli-scheduler-stop
agents:
  reviewer:
    scheduler:
      enabled: true
      script: function main() {}
`)
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		stopSchedulerRun: func(_ context.Context, req *connect.Request[agentcomposev2.StopSchedulerRunRequest]) (*connect.Response[agentcomposev2.StopSchedulerRunResponse], error) {
			if req.Msg.GetRunId() != "scheduler-run-active" || req.Msg.GetReason() != "manual stop" {
				t.Fatalf("StopSchedulerRun request = %#v", req.Msg)
			}
			return connect.NewResponse(&agentcomposev2.StopSchedulerRunResponse{
				Run:           &agentcomposev2.SchedulerRun{RunId: req.Msg.GetRunId(), AgentName: "reviewer", Status: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_CANCELED, Error: req.Msg.GetReason()},
				StopRequested: true,
			}), nil
		},
	}})
	defer server.Close()

	stdout, stderr, _, exitCode := executeCLICommand("scheduler", "stop", "--host", server.URL, "--file", composePath, "--reason", "manual stop", "scheduler-run-active")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "Status: canceled") || !strings.Contains(stdout, "Stop requested: true") {
		t.Fatalf("scheduler stop code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
	}
}

func TestIntegrationCLIInspectRunFallsBackToSchedulerRun(t *testing.T) {
	composePath := writeComposeFile(t, t.TempDir(), `
name: cli-scheduler-inspect-run
agents:
  reviewer:
    scheduler:
      enabled: true
      script: function main() {}
`)
	server := newComposeServiceStubServer(t, composeServiceStubs{
		project: projectServiceStub{
			getSchedulerRun: func(_ context.Context, req *connect.Request[agentcomposev2.GetSchedulerRunRequest]) (*connect.Response[agentcomposev2.GetSchedulerRunResponse], error) {
				return connect.NewResponse(&agentcomposev2.GetSchedulerRunResponse{Run: &agentcomposev2.SchedulerRun{
					RunId: req.Msg.GetRunId(), ProjectId: req.Msg.GetProject().GetProjectId(), AgentName: "reviewer",
					Status: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED, ResultJson: `{"inspected":true}`,
				}}), nil
			},
		},
		run: runServiceStub{
			getRun: func(context.Context, *connect.Request[agentcomposev2.GetRunRequest]) (*connect.Response[agentcomposev2.GetRunResponse], error) {
				return nil, connect.NewError(connect.CodeNotFound, context.Canceled)
			},
		},
	})
	defer server.Close()

	stdout, stderr, _, exitCode := executeCLICommand("inspect", "run", "scheduler-run-inspect", "--host", server.URL, "--file", composePath, "--json")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("inspect scheduler run code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
	}
	var output composeSchedulerRunOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil || output.ID != "scheduler-run-inspect" || output.Status != "succeeded" || output.ResultJSON != `{"inspected":true}` {
		t.Fatalf("inspect scheduler run output=%#v err=%v", output, err)
	}
}
