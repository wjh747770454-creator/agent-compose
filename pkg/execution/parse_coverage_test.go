package execution

import (
	"strings"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestParseAgentAndCommandExecResultWorkflows(t *testing.T) {
	agentPayload := AgentResultPrefix + `{"provider":"codex","sessionId":"agent-session","stopReason":"done","finalText":"final","transcript":"transcript"}`
	agent, err := ParseAgentExecResult("codex", domain.ExecResult{Stdout: "logs\n" + agentPayload, ExitCode: 0, Success: true})
	if err != nil {
		t.Fatalf("ParseAgentExecResult returned error: %v", err)
	}
	if agent.Agent != "codex" || agent.SessionID != "agent-session" || agent.DisplayOutput != "transcript" {
		t.Fatalf("agent result = %#v", agent)
	}
	if _, err := ParseAgentExecResult("codex", domain.ExecResult{Stderr: strings.Repeat("x", 300)}); err == nil || !strings.Contains(err.Error(), "...") {
		t.Fatalf("expected summarized failure, got %v", err)
	}
	if stripped := StripAgentResultPayload("hello\n" + agentPayload); stripped != "hello\n" {
		t.Fatalf("stripped = %q", stripped)
	}
	sanitized := SanitizeAgentExecResult(domain.ExecResult{Stdout: "stdout\n" + agentPayload, Output: "output\n" + agentPayload})
	if strings.Contains(sanitized.Stdout, AgentResultPrefix) || strings.Contains(sanitized.Output, AgentResultPrefix) {
		t.Fatalf("sanitized = %#v", sanitized)
	}

	commandPayload := CommandResultPrefix + `{"stdout":"out","stderr":"err","output":"out","exitCode":7,"success":false}`
	if stripped := StripCommandResultPayload("out\n" + commandPayload); stripped != "out\n" {
		t.Fatalf("command stripped = %q", stripped)
	}
	if stripped := StripCommandResultPayload(commandPayload); stripped != "" {
		t.Fatalf("command payload stripped = %q", stripped)
	}
	command, err := ParseCommandExecResult(domain.ExecResult{Stdout: "noise\n" + commandPayload})
	if err != nil {
		t.Fatalf("ParseCommandExecResult returned error: %v", err)
	}
	if command.ExitCode != 7 || command.Stdout != "out" || command.Success {
		t.Fatalf("command result = %#v", command)
	}
	if _, err := ParseCommandExecResult(domain.ExecResult{Stdout: "noise"}); err == nil {
		t.Fatalf("expected missing command payload error")
	}
}

func TestIntegrationParseAgentAndCommandExecResultWorkflows(t *testing.T) {
	TestParseAgentAndCommandExecResultWorkflows(t)
}

func TestE2EParseAgentAndCommandExecResultWorkflows(t *testing.T) {
	TestParseAgentAndCommandExecResultWorkflows(t)
}
