package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"agent-compose/pkg/capproxy"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/runs"
)

type delegationSandboxResolverFake struct {
	binding capproxy.SandboxBinding
	err     error
}

func (f delegationSandboxResolverFake) ResolveCapabilitySandbox(context.Context, string) (capproxy.SandboxBinding, error) {
	return f.binding, f.err
}

type delegationRunStoreFake struct {
	runs []domain.ProjectRunRecord
	err  error
}

func (f delegationRunStoreFake) ListProjectRunsForSandbox(context.Context, string) ([]domain.ProjectRunRecord, error) {
	return f.runs, f.err
}

type delegationRunnerFake struct {
	request runs.RunAgentRequest
	run     domain.ProjectRunRecord
	execErr error
	err     error
}

type delegationRetryRunnerFake struct {
	calls int
}

type delegationAuditorFake struct {
	events []domain.ProjectRunEventRecord
}

func (f *delegationAuditorFake) AppendProjectRunEvent(_ context.Context, event domain.ProjectRunEventRecord) (domain.ProjectRunEventRecord, bool, error) {
	f.events = append(f.events, event)
	return event, true, nil
}

func (f *delegationRetryRunnerFake) RunProjectAgent(_ context.Context, request runs.RunAgentRequest, _ *runs.StreamSink) (domain.ProjectRunRecord, error, error) {
	f.calls++
	if f.calls == 1 {
		return domain.ProjectRunRecord{RunID: "child-1", Status: domain.ProjectRunStatusFailed, Error: "sandbox start failed: connection reset"}, errors.New("connection reset"), nil
	}
	return domain.ProjectRunRecord{RunID: "child-2", Status: domain.ProjectRunStatusSucceeded, Output: "done", DelegationAttempt: request.DelegationAttempt}, nil, nil
}

func (f *delegationRunnerFake) RunProjectAgent(_ context.Context, request runs.RunAgentRequest, _ *runs.StreamSink) (domain.ProjectRunRecord, error, error) {
	f.request = request
	return f.run, f.execErr, f.err
}

func TestRuntimeDelegationDerivesTrustedLineage(t *testing.T) {
	parent := domain.ProjectRunRecord{RunID: "parent-1", RootRunID: "root-1", ProjectID: "project-1", SandboxID: "sandbox-1", Status: domain.ProjectRunStatusRunning}
	runner := &delegationRunnerFake{run: domain.ProjectRunRecord{
		RunID: "child-1", ParentRunID: parent.RunID, RootRunID: parent.RootRunID, ProjectID: parent.ProjectID,
		Status: domain.ProjectRunStatusSucceeded, Output: `{"project":"移动蜜罐"}`, ResultJSON: `{"success":true}`, StructuredResultJSON: `{"project":"移动蜜罐"}`,
	}}
	app := echo.New()
	RegisterRuntimeDelegationRoutes(app, RuntimeDelegationOptions{
		Sandboxes: delegationSandboxResolverFake{binding: capproxy.SandboxBinding{SandboxID: "sandbox-1", CapsetIDs: []string{"office"}}},
		Runs:      delegationRunStoreFake{runs: []domain.ProjectRunRecord{parent}},
		Runner:    runner,
	})
	body, _ := json.Marshal(runtimeDelegationRequest{TargetAgent: "project-intelligence", Prompt: "analyze", IdempotencyKey: "delegation-1", Reason: "project analysis"})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sandboxes/sandbox-1/delegations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(capproxy.SandboxTokenMetadata, "cap-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"structuredResult":{"project":"移动蜜罐"}`)) {
		t.Fatalf("structured result missing: %s", recorder.Body.String())
	}
	if runner.request.ProjectID != parent.ProjectID || runner.request.ParentRunID != parent.RunID || runner.request.RootRunID != parent.RootRunID {
		t.Fatalf("trusted lineage request = %#v", runner.request)
	}
	if runner.request.AgentName != "project-intelligence" || runner.request.ClientRequestID != "delegation-1:1" || runner.request.DelegationAttempt != 1 {
		t.Fatalf("delegation request = %#v", runner.request)
	}
}

func TestRuntimeDelegationUsesPromptSchemaCompatibilityMode(t *testing.T) {
	parent := domain.ProjectRunRecord{RunID: "parent-1", RootRunID: "parent-1", ProjectID: "project-1", SandboxID: "sandbox-1", Status: domain.ProjectRunStatusRunning}
	runner := &delegationRunnerFake{run: domain.ProjectRunRecord{
		RunID: "child-1", Status: domain.ProjectRunStatusSucceeded,
		Output: "analysis complete\n{\"project\":\"mobile\"}",
	}}
	app := echo.New()
	RegisterRuntimeDelegationRoutes(app, RuntimeDelegationOptions{
		Sandboxes: delegationSandboxResolverFake{binding: capproxy.SandboxBinding{SandboxID: "sandbox-1"}},
		Runs:      delegationRunStoreFake{runs: []domain.ProjectRunRecord{parent}},
		Runner:    runner,
	})
	body, _ := json.Marshal(runtimeDelegationRequest{
		TargetAgent:      "project-intelligence",
		Prompt:           "analyze",
		OutputSchemaJSON: `{"type":"object","required":["project"]}`,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sandboxes/sandbox-1/delegations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(capproxy.SandboxTokenMetadata, "cap-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"structuredResult":{"project":"mobile"}`)) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if runner.request.OutputSchemaJSON != "" || !strings.Contains(runner.request.Prompt, "Structured output contract") || !strings.Contains(runner.request.Prompt, `"required":["project"]`) {
		t.Fatalf("compatibility request = %#v", runner.request)
	}
}

func TestRuntimeDelegationRejectsInvalidPromptStructuredOutput(t *testing.T) {
	parent := domain.ProjectRunRecord{RunID: "parent-1", RootRunID: "parent-1", ProjectID: "project-1", SandboxID: "sandbox-1", Status: domain.ProjectRunStatusRunning}
	runner := &delegationRunnerFake{run: domain.ProjectRunRecord{RunID: "child-1", Status: domain.ProjectRunStatusSucceeded, Output: "not json"}}
	auditor := &delegationAuditorFake{}
	app := echo.New()
	RegisterRuntimeDelegationRoutes(app, RuntimeDelegationOptions{
		Sandboxes: delegationSandboxResolverFake{binding: capproxy.SandboxBinding{SandboxID: "sandbox-1"}},
		Runs:      delegationRunStoreFake{runs: []domain.ProjectRunRecord{parent}},
		Runner:    runner,
		Auditor:   auditor,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sandboxes/sandbox-1/delegations", bytes.NewBufferString(`{"targetAgent":"worker","prompt":"work","outputSchemaJson":"{\"type\":\"object\"}"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(capproxy.SandboxTokenMetadata, "cap-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadGateway || !bytes.Contains(recorder.Body.Bytes(), []byte(`"classification":"validation"`)) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(auditor.events) < 3 || auditor.events[len(auditor.events)-1].Name != "delegation.failed" {
		t.Fatalf("events=%#v", auditor.events)
	}
}

func TestRuntimeDelegationRejectsTokenSandboxMismatch(t *testing.T) {
	app := echo.New()
	RegisterRuntimeDelegationRoutes(app, RuntimeDelegationOptions{
		Sandboxes: delegationSandboxResolverFake{binding: capproxy.SandboxBinding{SandboxID: "other", CapsetIDs: []string{"office"}}},
		Runs:      delegationRunStoreFake{},
		Runner:    &delegationRunnerFake{},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sandboxes/sandbox-1/delegations", bytes.NewBufferString(`{"targetAgent":"worker","prompt":"work"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(capproxy.SandboxTokenMetadata, "cap-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRuntimeDelegationRequiresExactlyOneActiveParent(t *testing.T) {
	app := echo.New()
	RegisterRuntimeDelegationRoutes(app, RuntimeDelegationOptions{
		Sandboxes: delegationSandboxResolverFake{binding: capproxy.SandboxBinding{SandboxID: "sandbox-1", CapsetIDs: []string{"office"}}},
		Runs: delegationRunStoreFake{runs: []domain.ProjectRunRecord{
			{RunID: "run-1", Status: domain.ProjectRunStatusSucceeded},
		}},
		Runner: &delegationRunnerFake{},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sandboxes/sandbox-1/delegations", bytes.NewBufferString(`{"targetAgent":"worker","prompt":"work"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(capproxy.SandboxTokenMetadata, "cap-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRuntimeDelegationRetriesTransientFailureOnce(t *testing.T) {
	parent := domain.ProjectRunRecord{RunID: "parent-1", RootRunID: "parent-1", ProjectID: "project-1", SandboxID: "sandbox-1", Status: domain.ProjectRunStatusRunning}
	runner := &delegationRetryRunnerFake{}
	app := echo.New()
	RegisterRuntimeDelegationRoutes(app, RuntimeDelegationOptions{
		Sandboxes: delegationSandboxResolverFake{binding: capproxy.SandboxBinding{SandboxID: "sandbox-1", CapsetIDs: []string{"office"}}},
		Runs:      delegationRunStoreFake{runs: []domain.ProjectRunRecord{parent}}, Runner: runner,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sandboxes/sandbox-1/delegations", bytes.NewBufferString(`{"targetAgent":"worker","prompt":"work","idempotencyKey":"delegation-retry"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(capproxy.SandboxTokenMetadata, "cap-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || runner.calls != 2 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, runner.calls, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"attemptRunIds":["child-1","child-2"]`)) {
		t.Fatalf("attempt history missing: %s", recorder.Body.String())
	}
}

func TestRuntimeDelegationRecordsParentTakeover(t *testing.T) {
	parent := domain.ProjectRunRecord{RunID: "parent-1", RootRunID: "parent-1", ProjectID: "project-1", SandboxID: "sandbox-1", Status: domain.ProjectRunStatusRunning, AgentName: "entry"}
	auditor := &delegationAuditorFake{}
	app := echo.New()
	RegisterRuntimeDelegationRoutes(app, RuntimeDelegationOptions{
		Sandboxes: delegationSandboxResolverFake{binding: capproxy.SandboxBinding{SandboxID: "sandbox-1", CapsetIDs: []string{"office"}}},
		Runs:      delegationRunStoreFake{runs: []domain.ProjectRunRecord{parent}}, Runner: &delegationRunnerFake{}, Auditor: auditor,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sandboxes/sandbox-1/delegations/delegation-1/takeover", bytes.NewBufferString(`{"childRunId":"child-2","reason":"entry completed fallback analysis"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(capproxy.SandboxTokenMetadata, "cap-token")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || len(auditor.events) != 1 || auditor.events[0].Name != "delegation.taken_over" {
		t.Fatalf("status=%d events=%#v body=%s", recorder.Code, auditor.events, recorder.Body.String())
	}
}
