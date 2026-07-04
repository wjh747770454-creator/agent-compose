package main

import (
	"bytes"
	"strings"
	"testing"

	"agent-compose/pkg/compose"
	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestCLIConfigAndOutputWorkflow(t *testing.T) {
	testCLIConfigAndOutputWorkflow(t)
}

func TestIntegrationCLIConfigAndOutputWorkflow(t *testing.T) {
	testCLIConfigAndOutputWorkflow(t)
}

func TestE2ECLIConfigAndOutputWorkflow(t *testing.T) {
	testCLIConfigAndOutputWorkflow(t)
}

func testCLIConfigAndOutputWorkflow(t *testing.T) {
	t.Helper()
	TestRootCommandPrintsHelpWithoutStartingDaemon(t)
	TestUnknownCommandFailsWithoutStartingDaemon(t)
	TestVersionCommandPrintsBuildVersionWithoutStartingDaemon(t)
	TestConfigCommandPrintsNormalizedJSONWithoutStartingDaemon(t)
	TestConfigCommandPrintsNormalizedYAMLWithoutStartingDaemon(t)
	TestConfigCommandQuietOnlyValidates(t)
	TestConfigCommandQuietFailureIncludesComposePathAndField(t)
	TestConfigCommandMissingComposeFileWritesStderrAndExitCode(t)
	TestConfigCommandUsesGlobalFileProjectNameAndJSON(t)
	TestCLIClientConfigPriority(t)
	TestCLIClientConfigRejectsInvalidHost(t)
	TestStatusCommandUsesHostFlagBeforeEnvironment(t)
	TestStatusCommandUsesEnvironmentHost(t)
	TestStatusCommandUsesDefaultUnixSocket(t)
	TestStatusCommandUnavailableWritesStderrAndExitCode(t)
	TestStatusCommandReportsUnreadableDaemon(t)
	TestCLIDownFirstRepeatedPartialAndJSON(t)
	TestComposeImageOutputFromProtoAcceptsOCIStatus(t)
	TestLogsJSONFollowIsUsageError(t)
	TestInvalidHostWritesStderrAndUsageExitCode(t)
	testComposeProjectPureHelpers(t)
}

func testComposeProjectPureHelpers(t *testing.T) {
	t.Helper()
	project := &compose.NormalizedProjectSpec{Agents: []compose.NormalizedAgentSpec{
		{Image: " guest:latest "},
		{Image: "guest:latest"},
		{Image: "worker:latest"},
		{Image: " "},
	}}
	if refs := projectImageRefs(project); len(refs) != 2 || refs[0] != "guest:latest" || refs[1] != "worker:latest" {
		t.Fatalf("projectImageRefs = %#v", refs)
	}

	item := composeProjectListItemFromSummary(&agentcomposev2.ProjectSummary{
		ProjectId:       "project-1",
		Name:            "Project",
		SourcePath:      "/tmp/project/agent-compose.yml",
		CurrentRevision: 3,
		SpecHash:        "hash",
		AgentCount:      2,
		SchedulerCount:  1,
		RunningRunCount: 4,
		LatestRunId:     "run-1",
		UpdatedAt:       "2026-07-04T00:00:00Z",
		RemovedAt:       "2026-07-05T00:00:00Z",
	})
	if item.ProjectDir != "/tmp/project" || projectListStatus(item) != "removed" {
		t.Fatalf("project list item = %#v", item)
	}
	count := uint32(5)
	item.ServiceCount = &count
	var out bytes.Buffer
	if err := writeProjectListText(&out, []composeProjectListItem{item}, true); err != nil {
		t.Fatalf("writeProjectListText verbose returned error: %v", err)
	}
	if !strings.Contains(out.String(), "SERVICES") || !strings.Contains(out.String(), "removed") || projectServiceCountText(item.ServiceCount) != "5" {
		t.Fatalf("verbose project list output = %q", out.String())
	}
	out.Reset()
	item.RemovedAt = ""
	item.ServiceCount = nil
	if err := writeProjectListText(&out, []composeProjectListItem{item}, false); err != nil {
		t.Fatalf("writeProjectListText returned error: %v", err)
	}
	if !strings.Contains(out.String(), "-") || projectListStatus(item) != "active" || projectServiceCountText(nil) != "-" {
		t.Fatalf("project list output = %q", out.String())
	}

	issues := formatProjectValidationIssues([]*agentcomposev2.ProjectValidationIssue{
		{Path: "agents[0].image", Message: "required"},
		{Message: "top-level error"},
	})
	if issues != "agents[0].image: required; top-level error" {
		t.Fatalf("formatProjectValidationIssues = %q", issues)
	}
	run := &agentcomposev2.RunSummary{CreatedAt: "created", UpdatedAt: "updated", StartedAt: "started", CompletedAt: "completed"}
	if runSortTime(run) != "updated" {
		t.Fatalf("runSortTime = %q", runSortTime(run))
	}
	if execResultExitCode(&agentcomposev2.ExecResult{ExitCode: 7}) != 7 || execResultExitCode(&agentcomposev2.ExecResult{ExitCode: 126}) != exitCodeGeneral {
		t.Fatalf("execResultExitCode mismatch")
	}
	platform, err := parseImagePlatform("linux/amd64/v3")
	if err != nil || platform.GetOs() != "linux" || platform.GetArchitecture() != "amd64" || platform.GetVariant() != "v3" {
		t.Fatalf("parseImagePlatform platform=%#v err=%v", platform, err)
	}
	if platform, err := parseImagePlatform(" "); err != nil || platform != nil {
		t.Fatalf("empty parseImagePlatform platform=%#v err=%v", platform, err)
	}
	if _, err := parseImagePlatform("linux"); err == nil {
		t.Fatalf("expected invalid platform error")
	}
	if imageStoreText(agentcomposev2.ImageStoreKind_IMAGE_STORE_KIND_DOCKER_DAEMON) != "docker" ||
		imageStoreText(agentcomposev2.ImageStoreKind_IMAGE_STORE_KIND_OCI_CACHE) != "oci-cache" ||
		imageAvailabilityStatusText(agentcomposev2.ImageAvailabilityStatus_IMAGE_AVAILABILITY_STATUS_ERROR) != "error" ||
		imageOperationStatusText(agentcomposev2.ImageOperationStatus_IMAGE_OPERATION_STATUS_SUCCEEDED) != "succeeded" {
		t.Fatalf("image text helpers returned unexpected values")
	}

	projectSummary := &agentcomposev2.ProjectSummary{
		ProjectId:       "project-1",
		Name:            "Project",
		SourcePath:      "/tmp/project/agent-compose.yml",
		CurrentRevision: 4,
		SpecHash:        "hash-2",
		AgentCount:      2,
		SchedulerCount:  1,
	}
	changes := []*agentcomposev2.ProjectChange{
		{Action: agentcomposev2.ProjectChangeAction_PROJECT_CHANGE_ACTION_CREATED, ResourceType: "agent", ResourceId: "agent-1", Name: "worker"},
		{Action: agentcomposev2.ProjectChangeAction_PROJECT_CHANGE_ACTION_UPDATED, ResourceType: "scheduler", ResourceId: "scheduler-1", Name: "timer"},
		{Action: agentcomposev2.ProjectChangeAction_PROJECT_CHANGE_ACTION_REMOVED, ResourceType: "session", ResourceId: "session-1", Name: "old"},
		{Action: agentcomposev2.ProjectChangeAction_PROJECT_CHANGE_ACTION_UNCHANGED, ResourceType: "session", ResourceId: "session-2", Message: "stop failed"},
	}
	upResp := &agentcomposev2.ApplyProjectResponse{
		Project: &agentcomposev2.Project{Summary: projectSummary},
		Revision: &agentcomposev2.ProjectRevision{
			Revision: 4,
			SpecHash: "hash-2",
		},
		Applied: true,
		Changes: changes,
	}
	upOutput := composeUpOutputFromResponse(upResp)
	if !upOutput.Applied || upOutput.Project.ID != "project-1" || len(upOutput.Changes) != len(changes) {
		t.Fatalf("composeUpOutputFromResponse = %#v", upOutput)
	}
	out.Reset()
	if err := writeComposeUpText(&out, upResp); err != nil {
		t.Fatalf("writeComposeUpText returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Status: applied") || !strings.Contains(out.String(), "created") {
		t.Fatalf("compose up text = %q", out.String())
	}
	out.Reset()
	upResp.Applied = false
	if err := writeComposeUpText(&out, upResp); err != nil || !strings.Contains(out.String(), "Status: not-applied") {
		t.Fatalf("compose up not-applied text = %q err=%v", out.String(), err)
	}
	out.Reset()
	upResp.Unchanged = true
	if err := writeComposeUpText(&out, upResp); err != nil || !strings.Contains(out.String(), "Status: unchanged") {
		t.Fatalf("compose up unchanged text = %q err=%v", out.String(), err)
	}

	downOutput := composeDownOutputFromResponse(&agentcomposev2.RemoveProjectResponse{
		Project: &agentcomposev2.Project{Summary: projectSummary},
		Changes: changes,
	})
	if downOutput.Status != "partial-failure" || downOutput.FailedSessionStops != 1 || len(composeChangeOutputs(changes)) != len(changes) {
		t.Fatalf("composeDownOutputFromResponse = %#v", downOutput)
	}
	out.Reset()
	if err := writeComposeDownText(&out, downOutput); err != nil {
		t.Fatalf("writeComposeDownText returned error: %v", err)
	}
	if !strings.Contains(out.String(), "partial-failure") || !strings.Contains(out.String(), "stop failed") {
		t.Fatalf("compose down text = %q", out.String())
	}
	unchangedDown := composeDownOutputFromResponse(&agentcomposev2.RemoveProjectResponse{Project: &agentcomposev2.Project{Summary: projectSummary}})
	if unchangedDown.Status != "unchanged" {
		t.Fatalf("unchanged down output = %#v", unchangedDown)
	}
	if projectChangeActionText(agentcomposev2.ProjectChangeAction_PROJECT_CHANGE_ACTION_UNSPECIFIED) != "unspecified" ||
		countProjectDownFailedSessionStops(changes) != 1 {
		t.Fatalf("project change helpers returned unexpected values")
	}
	_ = domain.SessionSummary{ID: "compile-check"}
}
