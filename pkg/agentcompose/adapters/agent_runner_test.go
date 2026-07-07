package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sessionstore"
)

type fakeAgentDefinitionStore struct {
	agent domain.AgentDefinition
	err   error
}

func (s fakeAgentDefinitionStore) GetAgentDefinition(context.Context, string) (domain.AgentDefinition, error) {
	if s.err != nil {
		return domain.AgentDefinition{}, s.err
	}
	return s.agent, nil
}

type fakeAgentRuntime struct {
	specs        []domain.ExecSpec
	streamChunks []domain.ExecChunk
	result       domain.ExecResult
}

func (r *fakeAgentRuntime) EnsureSession(context.Context, *domain.Session, domain.VMState, domain.ProxyState) (domain.SessionVMInfo, error) {
	return domain.SessionVMInfo{}, nil
}

func (r *fakeAgentRuntime) StopSession(context.Context, *domain.Session, domain.VMState) (bool, error) {
	return false, nil
}

func (r *fakeAgentRuntime) Exec(context.Context, *domain.Session, domain.VMState, domain.ExecSpec) (domain.ExecResult, error) {
	return domain.ExecResult{}, nil
}

func (r *fakeAgentRuntime) ExecStream(_ context.Context, _ *domain.Session, _ domain.VMState, spec domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	r.specs = append(r.specs, spec)
	for _, chunk := range r.streamChunks {
		if stream != nil {
			stream(chunk)
		}
	}
	if r.result.Stdout != "" || r.result.Stderr != "" || r.result.Output != "" || r.result.ExitCode != 0 || r.result.Success {
		return r.result, nil
	}
	payload := execution.AgentResultPrefix + `{"provider":"codex","sessionId":"agent-session-1","finalText":"done","transcript":"trace","stopReason":"completed"}`
	return domain.ExecResult{Stdout: payload, Output: payload, ExitCode: 0, Success: true}, nil
}

func TestAgentRunnerExecuteAgentRunWritesSystemPromptAndParsesResult(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:             root,
		SessionRoot:          filepath.Join(root, "sessions"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "guest:latest",
		GuestWorkspacePath:   "/workspace",
		GuestStateRoot:       "/data/state",
		GuestHomePath:        "/root",
		JupyterProxyBasePath: "/agent-compose/session",
		SessionStartTimeout:  2 * time.Second,
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "agent session", "", driverpkg.RuntimeDriverBoxlite, "guest:latest", "", domain.SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	runtime := &fakeAgentRuntime{}
	runner := NewAgentRunner(config, store, nil, fakeAgentDefinitionStore{agent: domain.AgentDefinition{
		ID:           "agent-1",
		SystemPrompt: "Reply only in Chinese",
	}}, fakeRuntimeProvider{runtime: runtime})

	result, parsed, err := runner.ExecuteAgentRun(ctx, session, "codex", "agent-1", "", "", "hello", "", nil)
	if err != nil {
		t.Fatalf("ExecuteAgentRun returned error: %v", err)
	}
	if !result.Success || !parsed.Success || parsed.FinalText != "done" {
		t.Fatalf("result = %#v parsed = %#v", result, parsed)
	}
	contentBytes, err := os.ReadFile(execution.HostAgentSystemPromptPath(session))
	if err != nil {
		t.Fatalf("ReadFile(system prompt) returned error: %v", err)
	}
	content := string(contentBytes)
	if content != "Reply only in Chinese" {
		t.Fatalf("system prompt = %q", content)
	}
	if len(runtime.specs) != 1 || !strings.Contains(runtime.specs[0].Args[1], "agent-compose-runtime prompt") {
		t.Fatalf("runtime specs = %#v", runtime.specs)
	}
}

func TestAgentRunnerResolveAgentSystemPromptBranches(t *testing.T) {
	ctx := context.Background()
	session := &domain.Session{Summary: domain.SessionSummary{Tags: []domain.SessionTag{
		{Name: domain.AgentSessionTagID, Value: "agent-tagged"},
		{Name: domain.AgentSessionTagSource, Value: domain.AgentSessionTagSourceVal},
	}}}
	runner := NewAgentRunner(nil, nil, nil, fakeAgentDefinitionStore{agent: domain.AgentDefinition{SystemPrompt: "  tagged prompt  "}}, nil)
	if prompt, err := runner.ResolveAgentSystemPrompt(ctx, session, ""); err != nil || prompt != "tagged prompt" {
		t.Fatalf("tagged prompt = %q err=%v", prompt, err)
	}
	runner.agents = fakeAgentDefinitionStore{err: errors.New("store unavailable")}
	if prompt, err := runner.ResolveAgentSystemPrompt(ctx, session, "agent-tagged"); err != nil || prompt != "" {
		t.Fatalf("store error prompt = %q err=%v", prompt, err)
	}
	if prompt, err := (*AgentRunner)(nil).ResolveAgentSystemPrompt(ctx, session, "agent-tagged"); err != nil || prompt != "" {
		t.Fatalf("nil runner prompt = %q err=%v", prompt, err)
	}
	if prompt, err := NewAgentRunner(nil, nil, nil, nil, nil).ResolveAgentSystemPrompt(ctx, session, "agent-tagged"); err != nil || prompt != "" {
		t.Fatalf("nil store prompt = %q err=%v", prompt, err)
	}
	if prompt, err := runner.ResolveAgentSystemPrompt(ctx, nil, "agent-tagged"); err != nil || prompt != "" {
		t.Fatalf("nil session prompt = %q err=%v", prompt, err)
	}
	untagged := &domain.Session{Summary: domain.SessionSummary{Tags: []domain.SessionTag{{Name: domain.AgentSessionTagID, Value: "agent-tagged"}}}}
	if prompt, err := runner.ResolveAgentSystemPrompt(ctx, untagged, ""); err != nil || prompt != "" {
		t.Fatalf("untagged prompt = %q err=%v", prompt, err)
	}
}
