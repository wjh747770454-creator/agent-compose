package execution

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestLoadStoredAgentSessionIDHandlesMissingInvalidAndValidFiles(t *testing.T) {
	root := t.TempDir()
	if got := LoadStoredAgentSessionID(filepath.Join(root, "missing.json")); got != "" {
		t.Fatalf("missing session id = %q, want empty", got)
	}

	invalidPath := filepath.Join(root, "invalid.json")
	if err := os.WriteFile(invalidPath, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write invalid state: %v", err)
	}
	if got := LoadStoredAgentSessionID(invalidPath); got != "" {
		t.Fatalf("invalid session id = %q, want empty", got)
	}

	blankPath := filepath.Join(root, "blank.json")
	if err := os.WriteFile(blankPath, []byte(`{"sessionId":"   "}`), 0o644); err != nil {
		t.Fatalf("write blank state: %v", err)
	}
	if got := LoadStoredAgentSessionID(blankPath); got != "" {
		t.Fatalf("blank session id = %q, want empty", got)
	}

	validPath := filepath.Join(root, "valid.json")
	if err := os.WriteFile(validPath, []byte(`{"sessionId":"  sess-123  "}`), 0o644); err != nil {
		t.Fatalf("write valid state: %v", err)
	}
	if got := LoadStoredAgentSessionID(validPath); got != "sess-123" {
		t.Fatalf("valid session id = %q, want sess-123", got)
	}
}

func TestAgentSessionJSONLSelectionAndResumeInfo(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "session")
	session := &domain.Session{Summary: domain.SessionSummary{WorkspacePath: filepath.Join(sessionDir, "workspace")}}

	statePath := filepath.Join(sessionDir, "state", "agents", "providers", "codex.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"sessionId":" sess-1 "}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	historyPath := filepath.Join(sessionDir, "home", ".codex", "history.jsonl")
	matchingSessionPath := filepath.Join(sessionDir, "home", ".codex", "sessions", "run-sess-1.jsonl")
	otherSessionPath := filepath.Join(sessionDir, "home", ".codex", "sessions", "run-other.jsonl")
	for _, path := range []string{historyPath, matchingSessionPath, otherSessionPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	txtPath := filepath.Join(sessionDir, "home", ".codex", "sessions", "run-sess-1.txt")
	if err := os.WriteFile(txtPath, []byte("skip"), 0o644); err != nil {
		t.Fatalf("write txt session: %v", err)
	}

	if ShouldIncludeAgentJSONL(txtPath, "codex", "sess-1") {
		t.Fatal("txt path included as jsonl")
	}
	if !ShouldIncludeAgentJSONL(historyPath, "codex", "sess-1") {
		t.Fatal("codex history jsonl excluded")
	}
	if !ShouldIncludeAgentJSONL(matchingSessionPath, "codex", "sess-1") {
		t.Fatal("matching codex session jsonl excluded")
	}
	if ShouldIncludeAgentJSONL(otherSessionPath, "codex", "sess-1") {
		t.Fatal("non-matching codex session jsonl included")
	}
	if !ShouldIncludeAgentJSONL(filepath.Join(root, "claude.jsonl"), "claude", "sess-1") {
		t.Fatal("claude jsonl excluded")
	}

	paths := FindAgentSessionJSONLPaths(HostSessionHome(session), "codex", "sess-1")
	wantPaths := []string{historyPath, matchingSessionPath}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("FindAgentSessionJSONLPaths = %#v, want %#v", paths, wantPaths)
	}
	if got := FindAgentSessionJSONLPaths(HostSessionHome(session), "unknown", "sess-1"); got != nil {
		t.Fatalf("unknown provider paths = %#v, want nil", got)
	}

	info := CollectAgentResumeInfo(session, "codex", "", "/guest/session.json")
	if info == nil {
		t.Fatal("CollectAgentResumeInfo returned nil")
	}
	if info.Provider != "codex" || info.SessionID != "sess-1" || info.SessionStatePath != statePath || info.SessionManifestPath != "/guest/session.json" {
		t.Fatalf("resume info = %#v", info)
	}
	if !reflect.DeepEqual(info.SessionJSONLPaths, wantPaths) {
		t.Fatalf("resume jsonl paths = %#v, want %#v", info.SessionJSONLPaths, wantPaths)
	}

	emptySession := &domain.Session{Summary: domain.SessionSummary{WorkspacePath: filepath.Join(root, "empty", "workspace")}}
	if got := CollectAgentResumeInfo(emptySession, "", "", ""); got != nil {
		t.Fatalf("empty resume info = %#v, want nil", got)
	}
}

func TestAgentSessionRootsAndTraceDetails(t *testing.T) {
	home := t.TempDir()
	if roots := AgentSessionJSONLRoots(home, "claude"); len(roots) != 3 || !strings.Contains(roots[0], ".claude") {
		t.Fatalf("claude roots = %#v", roots)
	}
	if roots := AgentSessionJSONLRoots(home, "gemini"); len(roots) != 3 || !strings.Contains(roots[0], ".gemini") {
		t.Fatalf("gemini roots = %#v", roots)
	}
	if roots := AgentSessionJSONLRoots(home, "opencode"); roots != nil {
		t.Fatalf("opencode roots = %#v, want nil", roots)
	}

	details, consumed := CollectAgentTraceDetails("agent.tool", []string{"one", "  ", "two"})
	if details != "one" || consumed != 2 {
		t.Fatalf("tool details=%q consumed=%d, want one/2", details, consumed)
	}
	details, consumed = CollectAgentTraceDetails("agent.assistant", []string{"one", "", "[tool: run]", "two"})
	if details != "one\n" || consumed != 2 {
		t.Fatalf("assistant details=%q consumed=%d, want one newline/2", details, consumed)
	}
	details, consumed = CollectAgentTraceDetails("agent.assistant", []string{"one", "two"})
	if details != "one\ntwo" || consumed != 2 {
		t.Fatalf("terminal details=%q consumed=%d, want all lines/2", details, consumed)
	}

	events := AgentTraceEvents("[tool: run]\n$ go test\n[hook: done]\nfinished", time.Unix(10, 0).UTC())
	if len(events) != 2 {
		t.Fatalf("events = %#v, want two events", events)
	}
	if events[0].Type != "agent.tool" || events[0].Message != "run\n$ go test" {
		t.Fatalf("tool event = %#v", events[0])
	}
	if events[1].Type != "agent.hook" || events[1].Message != "done\nfinished" {
		t.Fatalf("hook event = %#v", events[1])
	}
	if _, _, ok := ParseAgentTraceMarker("[tool: ]"); ok {
		t.Fatal("empty tool marker parsed")
	}
	if _, _, ok := ParseAgentTraceMarker("[hook: ]"); ok {
		t.Fatal("empty hook marker parsed")
	}
}
