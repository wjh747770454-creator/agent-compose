package agentcompose

import (
	appconfig "agent-compose/pkg/config"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAgentSystemPromptReturnsEmptyWhenAgentPromptUnset(t *testing.T) {
	ctx := context.Background()
	configDB := newTestConfigStore(t)
	created, err := configDB.CreateAgentDefinition(ctx, AgentDefinition{
		ID:       "agent-empty-prompt",
		Name:     "Empty Prompt",
		Provider: "codex",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("CreateAgentDefinition returned error: %v", err)
	}
	executor := &Executor{configDB: configDB}
	session := &Session{
		Summary: SessionSummary{
			Tags: []SessionTag{
				{Name: agentSessionTagSource, Value: agentSessionTagSourceVal},
				{Name: agentSessionTagID, Value: created.ID},
			},
		},
	}

	got, err := executor.resolveAgentSystemPrompt(ctx, session, "")
	if err != nil {
		t.Fatalf("resolveAgentSystemPrompt returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveAgentSystemPrompt = %q, want empty", got)
	}
}

func TestResolveAgentSystemPromptFromProviderAgentID(t *testing.T) {
	ctx := context.Background()
	configDB := newTestConfigStore(t)
	created, err := configDB.CreateAgentDefinition(ctx, AgentDefinition{
		ID:           "agent-explicit-id",
		Name:         "Explicit",
		Provider:     "codex",
		SystemPrompt: "Use explicit agent id",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateAgentDefinition returned error: %v", err)
	}
	executor := &Executor{configDB: configDB}
	session := &Session{Summary: SessionSummary{Tags: []SessionTag{}}}

	got, err := executor.resolveAgentSystemPrompt(ctx, session, created.ID)
	if err != nil {
		t.Fatalf("resolveAgentSystemPrompt returned error: %v", err)
	}
	if got != "Use explicit agent id" {
		t.Fatalf("resolveAgentSystemPrompt = %q, want %q", got, "Use explicit agent id")
	}
}

func TestResolveAgentSystemPromptPrefersProviderAgentOverSessionTag(t *testing.T) {
	ctx := context.Background()
	configDB := newTestConfigStore(t)
	tagged, err := configDB.CreateAgentDefinition(ctx, AgentDefinition{
		ID:           "agent-tagged",
		Name:         "Tagged",
		Provider:     "codex",
		SystemPrompt: "From session tag",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateAgentDefinition returned error: %v", err)
	}
	explicit, err := configDB.CreateAgentDefinition(ctx, AgentDefinition{
		ID:           "agent-explicit",
		Name:         "Explicit",
		Provider:     "codex",
		SystemPrompt: "From provider agent id",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateAgentDefinition returned error: %v", err)
	}
	executor := &Executor{configDB: configDB}
	session := &Session{
		Summary: SessionSummary{
			Tags: []SessionTag{
				{Name: agentSessionTagSource, Value: agentSessionTagSourceVal},
				{Name: agentSessionTagID, Value: tagged.ID},
			},
		},
	}

	got, err := executor.resolveAgentSystemPrompt(ctx, session, explicit.ID)
	if err != nil {
		t.Fatalf("resolveAgentSystemPrompt returned error: %v", err)
	}
	if got != "From provider agent id" {
		t.Fatalf("resolveAgentSystemPrompt = %q, want %q", got, "From provider agent id")
	}
}

func TestResolveAgentSystemPromptReturnsEmptyWhenAgentMissing(t *testing.T) {
	ctx := context.Background()
	executor := &Executor{configDB: newTestConfigStore(t)}
	session := &Session{Summary: SessionSummary{Tags: []SessionTag{}}}

	got, err := executor.resolveAgentSystemPrompt(ctx, session, "missing-agent-id")
	if err != nil {
		t.Fatalf("resolveAgentSystemPrompt returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveAgentSystemPrompt = %q, want empty", got)
	}
}

func TestResolveAgentSystemPromptFromSessionTags(t *testing.T) {
	ctx := context.Background()
	configDB := newTestConfigStore(t)
	created, err := configDB.CreateAgentDefinition(ctx, AgentDefinition{
		ID:           "agent-with-prompt",
		Name:         "Runner",
		Provider:     "codex",
		SystemPrompt: "  Reply only in Chinese  ",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateAgentDefinition returned error: %v", err)
	}
	executor := &Executor{configDB: configDB}
	session := &Session{
		Summary: SessionSummary{
			Tags: []SessionTag{
				{Name: agentSessionTagSource, Value: agentSessionTagSourceVal},
				{Name: agentSessionTagID, Value: created.ID},
			},
		},
	}

	got, err := executor.resolveAgentSystemPrompt(ctx, session, "")
	if err != nil {
		t.Fatalf("resolveAgentSystemPrompt returned error: %v", err)
	}
	if got != "Reply only in Chinese" {
		t.Fatalf("resolveAgentSystemPrompt = %q, want %q", got, "Reply only in Chinese")
	}
}

func TestWriteAgentSystemPromptFileWritesGuestPath(t *testing.T) {
	root := t.TempDir()
	cfg := &appconfig.Config{GuestStateRoot: "/data/state"}
	session := &Session{Summary: SessionSummary{WorkspacePath: filepath.Join(root, "workspace")}}

	guestPath, err := writeAgentSystemPromptFile(cfg, session, "Codex", "Reply only in Chinese")
	if err != nil {
		t.Fatalf("writeAgentSystemPromptFile returned error: %v", err)
	}
	if guestPath == "" || !strings.HasPrefix(guestPath, "/data/state/agents/system-prompts/codex-") || !strings.HasSuffix(guestPath, ".txt") {
		t.Fatalf("guestPath = %q", guestPath)
	}
	fileName := filepath.Base(guestPath)
	hostPath := filepath.Join(root, "state", "agents", "system-prompts", fileName)
	content, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", hostPath, err)
	}
	if string(content) != "Reply only in Chinese" {
		t.Fatalf("file content = %q", string(content))
	}
}

func TestWriteAgentSystemPromptFileSkipsEmptyPrompt(t *testing.T) {
	root := t.TempDir()
	cfg := &appconfig.Config{GuestStateRoot: "/data/state"}
	session := &Session{Summary: SessionSummary{WorkspacePath: filepath.Join(root, "workspace")}}

	guestPath, err := writeAgentSystemPromptFile(cfg, session, "codex", "   ")
	if err != nil {
		t.Fatalf("writeAgentSystemPromptFile returned error: %v", err)
	}
	if guestPath != "" {
		t.Fatalf("guestPath = %q, want empty", guestPath)
	}
}

func TestBuildAgentExecSpecIncludesSystemPromptFlag(t *testing.T) {
	cfg := &appconfig.Config{
		GuestStateRoot:     "/data/state",
		GuestWorkspacePath: "/workspace",
	}
	session := &Session{Summary: SessionSummary{ID: "session-1"}}

	spec := buildAgentExecSpec(cfg, session, "codex", "/data/state/agents/prompts/prompt.txt", "", "/data/state/agents/system-prompts/sys.txt")
	command := strings.Join(spec.Args, " ")
	if !strings.Contains(command, "--system-prompt-file '/data/state/agents/system-prompts/sys.txt'") {
		t.Fatalf("command missing --system-prompt-file: %q", command)
	}
}

func TestExecuteAgentRunRetriesWithoutSystemPromptFlagForOldRuntime(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sessionID := "session-agent-system-prompt"
	cfg := &appconfig.Config{
		SessionRoot:        root,
		GuestStateRoot:     "/data/state",
		GuestWorkspacePath: "/workspace",
	}
	store := &Store{config: cfg}
	if err := os.MkdirAll(filepath.Join(root, sessionID, "vm"), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := store.SaveVMState(sessionID, VMState{Driver: "docker"}); err != nil {
		t.Fatalf("SaveVMState returned error: %v", err)
	}

	configDB := newTestConfigStore(t)
	agent, err := configDB.CreateAgentDefinition(ctx, AgentDefinition{
		ID:           "agent-retry",
		Name:         "Retry",
		Provider:     "codex",
		SystemPrompt: "Reply only in Chinese",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateAgentDefinition returned error: %v", err)
	}

	runtime := &systemPromptRetryRuntime{}
	executor := &Executor{
		config:   cfg,
		store:    store,
		configDB: configDB,
		runtimes: systemPromptRuntimeProvider{runtime: runtime},
	}
	session := &Session{
		Summary: SessionSummary{
			ID:            sessionID,
			VMStatus:      VMStatusRunning,
			WorkspacePath: filepath.Join(root, sessionID, "workspace"),
		},
	}

	result, parsed, err := executor.executeAgentRun(ctx, session, "codex", agent.ID, "hello", "", nil)
	if err != nil {
		t.Fatalf("executeAgentRun returned error: %v", err)
	}
	if !result.Success || !parsed.Success {
		t.Fatalf("executeAgentRun success = (%v, %v), want both true", result.Success, parsed.Success)
	}
	if len(runtime.commands) != 2 {
		t.Fatalf("ExecStream calls = %d, want 2", len(runtime.commands))
	}
	if !strings.Contains(runtime.commands[0], "--system-prompt-file") {
		t.Fatalf("first command missing --system-prompt-file: %q", runtime.commands[0])
	}
	if strings.Contains(runtime.commands[1], "--system-prompt-file") {
		t.Fatalf("retry command contains --system-prompt-file: %q", runtime.commands[1])
	}
}

func TestIsUnknownSystemPromptFileFlag(t *testing.T) {
	t.Parallel()
	const flag = "--system-prompt-file"
	tests := []struct {
		name   string
		err    error
		result ExecResult
		want   bool
	}{
		{
			name: "error mentions unknown option",
			err:  fmt.Errorf("unknown option '%s'", flag),
			want: true,
		},
		{
			name: "stderr mentions unknown flag",
			result: ExecResult{
				Stderr: "error: unknown flag " + flag,
			},
			want: true,
		},
		{
			name: "stderr mentions flag without unknown option wording",
			result: ExecResult{
				Stderr: flag + " is not supported",
			},
			want: false,
		},
		{
			name: "unrelated error",
			err:  fmt.Errorf("session is not running"),
			want: false,
		},
		{
			name: "empty result",
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isUnknownSystemPromptFileFlag(tt.err, tt.result); got != tt.want {
				t.Fatalf("isUnknownSystemPromptFileFlag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildAgentExecSpecOmitsSystemPromptFlagWhenEmpty(t *testing.T) {
	cfg := &appconfig.Config{
		GuestStateRoot:     "/data/state",
		GuestWorkspacePath: "/workspace",
	}
	session := &Session{Summary: SessionSummary{ID: "session-1"}}

	spec := buildAgentExecSpec(cfg, session, "codex", "/data/state/agents/prompts/prompt.txt", "", "")
	command := strings.Join(spec.Args, " ")
	if strings.Contains(command, "--system-prompt-file") {
		t.Fatalf("command contains unexpected --system-prompt-file: %q", command)
	}
}

type systemPromptRuntimeProvider struct {
	runtime BoxRuntime
}

func (p systemPromptRuntimeProvider) ForDriver(string) (BoxRuntime, error) {
	if p.runtime == nil {
		return nil, fmt.Errorf("runtime is required")
	}
	return p.runtime, nil
}

func (p systemPromptRuntimeProvider) ForSession(*Session) (BoxRuntime, error) {
	if p.runtime == nil {
		return nil, fmt.Errorf("runtime is required")
	}
	return p.runtime, nil
}

type systemPromptRetryRuntime struct {
	commands []string
}

func (r *systemPromptRetryRuntime) EnsureSession(context.Context, *Session, VMState, ProxyState) (SessionVMInfo, error) {
	return SessionVMInfo{}, nil
}

func (r *systemPromptRetryRuntime) StopSession(context.Context, *Session, VMState) (bool, error) {
	return true, nil
}

func (r *systemPromptRetryRuntime) Exec(context.Context, *Session, VMState, ExecSpec) (ExecResult, error) {
	return ExecResult{}, fmt.Errorf("unexpected Exec call")
}

func (r *systemPromptRetryRuntime) ExecStream(_ context.Context, _ *Session, _ VMState, spec ExecSpec, _ ExecStreamWriter) (ExecResult, error) {
	command := strings.Join(spec.Args, " ")
	r.commands = append(r.commands, command)
	if strings.Contains(command, "--system-prompt-file") {
		return ExecResult{Stderr: "unknown flag --system-prompt-file"}, fmt.Errorf("runtime rejected command")
	}
	payload := agentResultPrefix + `{"provider":"codex","sessionId":"agent-runtime-session","stopReason":"completed","finalText":"done","transcript":"done"}`
	return ExecResult{
		Stdout:   payload,
		Output:   payload,
		ExitCode: 0,
		Success:  true,
	}, nil
}
