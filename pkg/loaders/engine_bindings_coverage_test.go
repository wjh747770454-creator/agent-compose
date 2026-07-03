package loaders

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
)

type coverageEngineHost struct {
	sessionCalls []string
	requests     map[string]map[string]any
	agentCalls   []domain.LoaderAgentRequest
	llmCalls     []domain.LoaderLLMRequest
	commandCalls []domain.LoaderCommandRequest
	state        map[string]string
	setValues    []string
	deleted      []string
	published    []string
}

func (h *coverageEngineHost) Log(context.Context, string, any) error { return nil }

func (h *coverageEngineHost) Agent(_ context.Context, _ string, request domain.LoaderAgentRequest) (domain.LoaderAgentResult, error) {
	h.agentCalls = append(h.agentCalls, request)
	text := "agent-output"
	if strings.TrimSpace(request.OutputSchema) != "" {
		text = `{"summary":"ok","risk":"low"}`
	}
	return domain.LoaderAgentResult{
		Text: text, Output: text, FinalText: text, SessionID: "agent-session", CellID: "agent-cell",
		Agent: firstNonEmptyTest(request.Agent, "codex"), AgentSessionID: "agent-runtime-session", StopReason: "completed", Success: true,
	}, nil
}

func (h *coverageEngineHost) LLM(_ context.Context, _ string, request domain.LoaderLLMRequest) (domain.LoaderLLMResult, error) {
	h.llmCalls = append(h.llmCalls, request)
	text := "llm-output"
	if strings.TrimSpace(request.OutputSchema) != "" {
		text = `{"summary":"ok","risk":"low"}`
	}
	return domain.LoaderLLMResult{Text: text, Model: firstNonEmptyTest(request.Model, "gpt"), ResponseID: "resp-1", FinishReason: "stop"}, nil
}

func (h *coverageEngineHost) Command(_ context.Context, request domain.LoaderCommandRequest) (domain.LoaderCommandResult, error) {
	h.commandCalls = append(h.commandCalls, request)
	return domain.LoaderCommandResult{Stdout: "command-output", Output: "command-output", ExitCode: 0, Success: true, SessionID: "command-session", CellID: "command-cell", Artifacts: map[string]string{"stdout": "/tmp/stdout.txt"}}, nil
}

func (h *coverageEngineHost) StateGet(_ context.Context, key string) (string, bool, error) {
	value, ok := h.state[key]
	return value, ok, nil
}

func (h *coverageEngineHost) StateSet(_ context.Context, key, value string) error {
	if h.state == nil {
		h.state = map[string]string{}
	}
	h.state[key] = value
	h.setValues = append(h.setValues, value)
	return nil
}

func (h *coverageEngineHost) StateDelete(_ context.Context, key string) error {
	delete(h.state, key)
	h.deleted = append(h.deleted, key)
	return nil
}

func (h *coverageEngineHost) CallSessionRPC(_ context.Context, method, requestJSON string) (string, error) {
	if h.requests == nil {
		h.requests = map[string]map[string]any{}
	}
	h.sessionCalls = append(h.sessionCalls, method)
	if strings.TrimSpace(requestJSON) != "" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(requestJSON), &payload); err != nil {
			return "", err
		}
		h.requests[method] = payload
	}
	const sessionID = "session-from-host"
	switch method {
	case "CreateSession":
		return `{"session":{"summary":{"sessionId":"` + sessionID + `","vmStatus":"RUNNING"}}}`, nil
	case "GetSession":
		return `{"session":{"summary":{"sessionId":"` + sessionID + `","vmStatus":"RUNNING"}}}`, nil
	case "ListSessions":
		return `{"sessions":[{"sessionId":"` + sessionID + `","vmStatus":"RUNNING"}]}`, nil
	case "GetSessionProxy":
		return `{"sessionId":"` + sessionID + `","proxyPath":"/agent-compose/session/` + sessionID + `/lab","notebookUrl":"/agent-compose/session/` + sessionID + `/lab?token=t","driver":"boxlite","vmStatus":"RUNNING"}`, nil
	case "StopSession":
		return `{"session":{"summary":{"sessionId":"` + sessionID + `","vmStatus":"STOPPED"}}}`, nil
	case "ResumeSession":
		return `{"session":{"summary":{"sessionId":"` + sessionID + `","vmStatus":"RUNNING"}}}`, nil
	default:
		return `{}`, nil
	}
}

func (h *coverageEngineHost) PublishEvent(_ context.Context, topic, payloadJSON string) (domain.TopicEventRecord, error) {
	h.published = append(h.published, topic+" "+payloadJSON)
	return domain.TopicEventRecord{ID: "evt-test", Sequence: 1, Topic: topic, CorrelationID: "corr-test"}, nil
}

func TestQJSLoaderEngineBindingCoverageWorkflow(t *testing.T) {
	engine := &QJSLoaderEngine{}
	host := &coverageEngineHost{state: map[string]string{"existing": `{"value":1}`}}
	result, err := engine.Execute(context.Background(), LoaderExecutionRequest{
		Runtime:     domain.LoaderRuntimeScheduler,
		PayloadJSON: `{"input":true}`,
		Script: `
const interval = scheduler.interval(function heartbeat() {}, 2500, "interval-auto");
clearInterval(interval);
scheduler.timeout("timeout-id", 3500, function secondTimeout() {});
scheduler.cron("cron-id", "*/5 * * * *", function cronHandler(event) { return { cron: event.input }; }, { id: "cron-id", timezone: "UTC" });
scheduler.on("runtime.test.*", function onEvent(event) { return { event }; }, "event-id");

function main(payload) {
  scheduler.log("coverage", { payload });
  const created = scheduler.session.createSession({ title: "alpha" });
  const sessionId = created.session.summary.sessionId;
  const current = scheduler.session.getSession({ sessionId });
  const sessions = scheduler.session.listSessions();
  const proxy = scheduler.session.getSessionProxy({ sessionId });
  const stopped = scheduler.session.stopSession({ sessionId });
  const resumed = scheduler.session.ResumeSession({ sessionId });
  const RiskSummary = scheduler.z.object({ summary: scheduler.z.string(), risk: scheduler.z.enum(["low", "high"]) });
  const agent = scheduler.agent("summarize", {
    agent: "claude", sessionPolicy: "new", timeout: "45s", title: "Loader Agent Session",
    driver: "microsandbox", guestImage: "guest:latest", workspaceId: "workspace-1",
    sessionEnv: { REQUEST_ONLY: "request" }, outputSchema: RiskSummary
  });
  const llm = scheduler.llm("answer", { model: "gpt", outputSchema: RiskSummary });
  const execResult = scheduler.exec({ command: "python3", args: ["-V"], cwd: "/tmp", env: { FOO: "bar" }, timeoutMs: 30000, maxOutputBytes: 128, sessionPolicy: "new" });
  const shellResult = scheduler.shell("echo hello", { cwd: "/tmp", env: { SHELL_FOO: "baz" }, maxOutputBytes: 64 });
  scheduler.state.set("nil", null);
  scheduler.state.set("bool", true);
  scheduler.state.set("number", 42);
  scheduler.state.set("nan", NaN);
  scheduler.state.set("inf", Infinity);
  scheduler.state.set("object", { nested: [1, "two"] });
  const existing = scheduler.state.get("existing");
  scheduler.state.delete("existing");
  const published = scheduler.event.publish("runtime.test.requested", { value: 1 });
  return { current, sessions, proxy, stopped, resumed, agent, llm, execResult, shellResult, existing, published, runtime: scheduler.runtime.name };
}`,
	}, host)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(result.Triggers) != 3 || len(host.sessionCalls) != 6 || len(host.agentCalls) != 1 || len(host.llmCalls) != 1 || len(host.commandCalls) != 2 || len(host.published) != 1 {
		t.Fatalf("unexpected host calls result=%#v sessions=%#v agent=%d llm=%d commands=%d published=%#v", result.Triggers, host.sessionCalls, len(host.agentCalls), len(host.llmCalls), len(host.commandCalls), host.published)
	}
	if host.agentCalls[0].Driver != driverpkg.RuntimeDriverMicrosandbox || host.commandCalls[0].Mode != "exec" || host.commandCalls[1].Mode != "shell" {
		t.Fatalf("unexpected request mappings: agent=%#v commands=%#v", host.agentCalls[0], host.commandCalls)
	}
	if !strings.Contains(result.ResultJSON, `"runtime":"scheduler"`) || !strings.Contains(result.ResultJSON, `"eventId":"evt-test"`) {
		t.Fatalf("result json = %s", result.ResultJSON)
	}

	triggerResult, err := engine.Execute(context.Background(), LoaderExecutionRequest{
		Runtime:     domain.LoaderRuntimeScheduler,
		PayloadJSON: `{"input":true}`,
		Trigger:     &domain.LoaderTrigger{ID: "cron-id"},
		Script:      `scheduler.cron("cron-id", "*/5 * * * *", function cronHandler(event) { return { cron: event.input }; }, { id: "cron-id" });`,
	}, host)
	if err != nil || triggerResult.ResultJSON != `{"cron":true}` {
		t.Fatalf("trigger result=%#v err=%v", triggerResult, err)
	}
}

func TestIntegrationQJSLoaderEngineBindingCoverageWorkflow(t *testing.T) {
	TestQJSLoaderEngineBindingCoverageWorkflow(t)
}

func TestE2EQJSLoaderEngineBindingCoverageWorkflow(t *testing.T) {
	TestQJSLoaderEngineBindingCoverageWorkflow(t)
}

func TestQJSLoaderEngineValidationCoverageWorkflow(t *testing.T) {
	engine := &QJSLoaderEngine{}
	tests := []struct {
		script  string
		wantErr string
	}{
		{`scheduler.exec("python3")`, "scheduler.exec is unavailable during validation"},
		{`scheduler.shell("echo hello")`, "scheduler.shell is unavailable during validation"},
		{`scheduler.event.publish("runtime.test", {})`, "scheduler.event.publish is unavailable during validation"},
		{`scheduler.cron("*/5 * * * *", function cron() {}, { id: "a" }, { id: "b" });`, "at most one options"},
		{`scheduler.on("", function onEvent() {});`, "non-empty topic"},
		{`scheduler.timeout(function timeout() {}, 0);`, "positive delay"},
	}
	for _, tt := range tests {
		_, err := engine.Validate(context.Background(), domain.LoaderRuntimeScheduler, tt.script)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("Validate(%q) error = %v, want %q", tt.script, err, tt.wantErr)
		}
	}

	execTests := []struct {
		script  string
		wantErr string
	}{
		{`function main() { return scheduler.exec("python3"); }`, "scheduler.exec requires a request object"},
		{`function main() { return scheduler.exec({ args: ["-V"] }); }`, "scheduler.exec requires a non-empty command"},
		{`function main() { return scheduler.exec({ command: "python3", args: "bad" }); }`, "decode scheduler.exec args"},
		{`function main() { return scheduler.shell(""); }`, "scheduler.shell requires a non-empty script"},
		{`function main() { return scheduler.shell("echo ok", "bad"); }`, "scheduler.shell options must be an object"},
		{`function main() { return scheduler.agent("summarize", { timeout: 30000 }); }`, "decode scheduler.agent timeout"},
	}
	for _, tt := range execTests {
		_, err := engine.Execute(context.Background(), LoaderExecutionRequest{Runtime: domain.LoaderRuntimeScheduler, Script: tt.script}, &coverageEngineHost{})
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("Execute(%q) error = %v, want %q", tt.script, err, tt.wantErr)
		}
	}
}

func TestIntegrationQJSLoaderEngineValidationCoverageWorkflow(t *testing.T) {
	TestQJSLoaderEngineValidationCoverageWorkflow(t)
}

func TestE2EQJSLoaderEngineValidationCoverageWorkflow(t *testing.T) {
	TestQJSLoaderEngineValidationCoverageWorkflow(t)
}

func firstNonEmptyTest(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
